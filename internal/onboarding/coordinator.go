package onboarding

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

const (
	OnboardingOperationConflict = "ONBOARDING_OPERATION_CONFLICT"
	OnboardingRecoveryRequired  = "ONBOARDING_RECOVERY_REQUIRED"
)

var (
	ErrOnboardingAuthorityUnavailable = errors.New("AUTHORITY_UNAVAILABLE")
	ErrOnboardingOperationConflict    = errors.New(OnboardingOperationConflict)
	ErrOnboardingRecoveryRequired     = errors.New(OnboardingRecoveryRequired)
)

type CoordinatorError struct {
	Code  string
	Cause error
}

func (e *CoordinatorError) Error() string {
	if e.Cause == nil {
		return e.Code
	}
	return e.Code + ": " + e.Cause.Error()
}

func (e *CoordinatorError) Unwrap() error { return e.Cause }

type Coordinator struct {
	Hub      hub.Store
	StateDir string
}

type Result struct {
	OperationID       string
	ProjectID         string
	State             ReceiptState
	RequestSHA256     string
	ReceiptSHA256     string
	Hub               hub.TransactionResult
	HubTransaction    bool
	JournalRepairOnly bool
}

type registryAuthority struct {
	Before string
	After  string
}

var beforeOnboardingJournalHook = func(context.Context, hub.Store, hub.TransactionResult, string) error { return nil }

func NewCoordinator(store hub.Store) *Coordinator {
	return &Coordinator{Hub: store, StateDir: store.Config.StateDir}
}

// Execute consumes a strict prepared request/journal pair. The operation lock
// spans journal inspection, collision checks and journal replacement; the Hub
// transaction supplies the optimistic repository lock and revision guard.
func (c *Coordinator) Execute(ctx context.Context, request Request, operationID string) (Result, error) {
	if err := service.RequireWorkflowPolicyAuthority(ctx); err != nil {
		return Result{}, &CoordinatorError{Code: ErrOnboardingAuthorityUnavailable.Error(), Cause: err}
	}
	if c == nil || c.StateDir == "" {
		return Result{}, fmt.Errorf("onboarding coordinator state directory is unavailable")
	}
	if c.StateDir != request.GatewayStateDir {
		return Result{}, fmt.Errorf("coordinator state directory does not match request gateway_state_dir")
	}
	if err := ValidateRequest(request); err != nil {
		return Result{}, fmt.Errorf("invalid onboarding request: %w", err)
	}
	if err := validatePreparedJournalOperationID(operationID); err != nil {
		return Result{}, err
	}
	requestDigest, err := RequestDigest(request)
	if err != nil {
		return Result{}, err
	}
	operationLock, err := acquirePreparedJournalLock(c.StateDir, operationID)
	if err != nil {
		return Result{}, err
	}
	defer operationLock.Release()

	receipt, err := LoadOnboardingJournal(c.StateDir, operationID)
	if err != nil {
		return Result{}, err
	}
	if receipt.OperationID != operationID || receipt.RequestSHA256 != requestDigest || receipt.ProjectID != request.ProjectID {
		return Result{}, &CoordinatorError{Code: ErrOnboardingOperationConflict.Error(), Cause: errors.New("journal identity does not match request")}
	}
	managedProjectsLock, err := acquireManagedProjectsLock(ctx, c.StateDir)
	if err != nil {
		return Result{}, err
	}
	defer managedProjectsLock.Release()
	switch receipt.State {
	case StateHubCommitted:
		if err := ValidateHubCommittedReceipt(receipt, request); err != nil {
			return Result{}, &CoordinatorError{Code: ErrOnboardingOperationConflict.Error(), Cause: err}
		}
		if _, err := c.verifyRegistryAuthority(request, receipt, true); err != nil {
			return Result{}, err
		}
		project, plan, identifiers, objectDigests, err := buildDurableObjects(request)
		if err != nil {
			return Result{}, err
		}
		if err := c.validateCommittedHubState(ctx, request, receipt, project, plan, identifiers, objectDigests); err != nil {
			return Result{}, &CoordinatorError{Code: ErrOnboardingRecoveryRequired.Error(), Cause: err}
		}
		digest, err := HubCommittedReceiptDigest(receipt, request)
		if err != nil {
			return Result{}, err
		}
		return Result{OperationID: operationID, ProjectID: request.ProjectID, State: StateHubCommitted, RequestSHA256: requestDigest, ReceiptSHA256: digest, Hub: receiptHubTransaction(receipt, c.Hub)}, nil
	case StatePrepared:
		if err := ValidatePreparedReceipt(receipt, request); err != nil {
			return Result{}, &CoordinatorError{Code: ErrOnboardingOperationConflict.Error(), Cause: err}
		}
	default:
		return Result{}, &CoordinatorError{Code: ErrOnboardingRecoveryRequired.Error(), Cause: fmt.Errorf("unsupported onboarding journal state %q", receipt.State)}
	}

	project, plan, identifiers, objectDigests, err := buildDurableObjects(request)
	if err != nil {
		return Result{}, err
	}
	if receipt.RegistryDigests.ProjectSHA256 != objectDigests.project || receipt.RegistryDigests.PlanSHA256 != objectDigests.plan || receipt.RegistryDigests.IdentifiersSHA256 != objectDigests.identifiers {
		return Result{}, &CoordinatorError{Code: ErrOnboardingRecoveryRequired.Error(), Cause: errors.New("prepared receipt registry digests do not match canonical durable objects")}
	}
	if _, err := c.verifyRegistryAuthority(request, receipt, false); err != nil {
		return Result{}, err
	}
	if err := c.Hub.Ensure(ctx); err != nil {
		return Result{}, err
	}

	currentRevision, state, afterRevision, err := c.inspectTarget(ctx, request, project, plan, identifiers)
	if err != nil {
		return Result{}, err
	}
	if state == targetStateConflict {
		return Result{}, &CoordinatorError{Code: ErrOnboardingRecoveryRequired.Error(), Cause: errors.New("target durable objects are partial or conflicting")}
	}
	if state == targetStateExact {
		if currentRevision == request.ExpectedHubRevision {
			return Result{}, &CoordinatorError{Code: ErrOnboardingRecoveryRequired.Error(), Cause: errors.New("exact target objects already exist at the expected pre-transaction revision")}
		}
		committed := committedReceipt(receipt, request, afterRevision, project, plan, identifiers, false)
		journal, err := writeHubCommittedJournalLocked(c.StateDir, request, committed)
		if err != nil {
			return Result{}, &CoordinatorError{Code: ErrOnboardingRecoveryRequired.Error(), Cause: err}
		}
		return Result{OperationID: operationID, ProjectID: request.ProjectID, State: StateHubCommitted, RequestSHA256: requestDigest, ReceiptSHA256: journal.ReceiptSHA256, Hub: receiptHubTransaction(committed, c.Hub), JournalRepairOnly: true}, nil
	}
	if currentRevision != request.ExpectedHubRevision {
		return Result{}, &CoordinatorError{Code: ErrOnboardingRecoveryRequired.Error(), Cause: fmt.Errorf("HUB_REVISION_CONFLICT expected=%s actual=%s", request.ExpectedHubRevision, currentRevision)}
	}

	transaction, err := c.Hub.Transact(ctx, request.ExpectedHubRevision, "gateway: onboard project "+request.ProjectID, func(worktree string) ([]string, error) {
		if err := validateWorktreeTarget(worktree, request, project, plan, identifiers); err != nil {
			return nil, err
		}
		paths := canonicalOnboardingPaths(request.ProjectID)
		if err := hub.WriteJSON(worktree, paths[0], project); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, paths[1], plan); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, paths[2], identifiers); err != nil {
			return nil, err
		}
		return paths, nil
	})
	if err != nil {
		return Result{}, err
	}
	lastChange, err := c.commonPathLastChange(ctx, request.ProjectID)
	if err != nil || lastChange != transaction.After {
		if err == nil {
			err = fmt.Errorf("committed onboarding paths last-change %s does not match Hub transaction %s", lastChange, transaction.After)
		}
		return Result{OperationID: operationID, ProjectID: request.ProjectID, State: StateRecoveryRequired, RequestSHA256: requestDigest, Hub: transaction, HubTransaction: true}, &CoordinatorError{Code: ErrOnboardingRecoveryRequired.Error(), Cause: err}
	}
	committed := committedReceipt(receipt, request, transaction.After, project, plan, identifiers, true)
	if err := beforeOnboardingJournalHook(ctx, c.Hub, transaction, request.ProjectID); err != nil {
		return Result{OperationID: operationID, ProjectID: request.ProjectID, State: StateRecoveryRequired, RequestSHA256: requestDigest, Hub: transaction, HubTransaction: true}, &CoordinatorError{Code: ErrOnboardingRecoveryRequired.Error(), Cause: err}
	}
	if err := c.validateCommittedHubState(ctx, request, committed, project, plan, identifiers, objectDigests); err != nil {
		return Result{OperationID: operationID, ProjectID: request.ProjectID, State: StateRecoveryRequired, RequestSHA256: requestDigest, Hub: transaction, HubTransaction: true}, &CoordinatorError{Code: ErrOnboardingRecoveryRequired.Error(), Cause: err}
	}
	journal, err := writeHubCommittedJournalLocked(c.StateDir, request, committed)
	if err != nil {
		return Result{OperationID: operationID, ProjectID: request.ProjectID, State: StateRecoveryRequired, RequestSHA256: requestDigest, Hub: transaction, HubTransaction: true}, &CoordinatorError{Code: ErrOnboardingRecoveryRequired.Error(), Cause: fmt.Errorf("hub committed at %s but journal reconciliation failed: %w", transaction.After, err)}
	}
	return Result{OperationID: operationID, ProjectID: request.ProjectID, State: StateHubCommitted, RequestSHA256: requestDigest, ReceiptSHA256: journal.ReceiptSHA256, Hub: transaction, HubTransaction: true}, nil
}

const (
	managedProjectsLockAttempts = 200
	managedProjectsLockDelay    = 5 * time.Millisecond
)

func acquireManagedProjectsLock(ctx context.Context, stateDir string) (*lockfile.Lock, error) {
	var lastErr error
	for attempt := 0; attempt < managedProjectsLockAttempts; attempt++ {
		managedLock, err := lockfile.Acquire(filepath.Join(stateDir, "locks"), "managed-projects")
		if err == nil {
			return managedLock, nil
		}
		lastErr = err
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			return nil, err
		}
		if attempt+1 == managedProjectsLockAttempts {
			break
		}
		timer := time.NewTimer(managedProjectsLockDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return nil, fmt.Errorf("acquire managed project lock after bounded contention retry: %w", lastErr)
}

type targetState string

const (
	targetStateEmpty    targetState = "empty"
	targetStateExact    targetState = "exact"
	targetStateConflict targetState = "conflict"
)

type durableObjects struct {
	project     model.Project
	plan        model.Plan
	identifiers model.ProjectIdentifiers
}

type objectDigests struct {
	project, plan, identifiers string
}

func buildDurableObjects(request Request) (model.Project, model.Plan, model.ProjectIdentifiers, objectDigests, error) {
	updatedAt, err := parseReceiptTime(request.InitialPlan.UpdatedAt)
	if err != nil {
		return model.Project{}, model.Plan{}, model.ProjectIdentifiers{}, objectDigests{}, err
	}
	project := model.Project{SchemaVersion: model.SchemaVersion, ID: request.ProjectID, RepositoryURL: request.RepositoryURL, DefaultBranch: request.DefaultBranch, Status: "active", CreatedAt: updatedAt, UpdatedAt: updatedAt}
	if request.Workflow != nil {
		project.WorkflowRepository = request.Workflow.Repository
		project.WorkflowCommit = request.Workflow.Commit
	}
	plan := model.Plan{SchemaVersion: model.PlanSchemaVersion, ProjectID: request.ProjectID, Revision: int(request.InitialPlan.Revision), Title: request.InitialPlan.Title, Summary: request.InitialPlan.Summary, CurrentObjective: request.InitialPlan.CurrentObjective, Queue: append([]string(nil), request.InitialPlan.Queue...), UpdatedBy: request.InitialPlan.UpdatedBy, UpdatedAt: updatedAt}
	for _, section := range request.InitialPlan.Sections {
		plan.Sections = append(plan.Sections, model.PlanSectionIndex{ID: section.ID, Title: section.Title, ShortDescription: section.ShortDescription, Revision: int(section.Revision)})
	}
	identifiers := model.ProjectIdentifiers{SchemaVersion: model.SchemaVersion, ProjectID: request.ProjectID, ProjectCode: request.ProjectCode, NextTaskNumber: 1, NextADRNumber: 1}
	if err := model.ValidateProject(project); err != nil {
		return model.Project{}, model.Plan{}, model.ProjectIdentifiers{}, objectDigests{}, err
	}
	if err := model.ValidatePlan(plan); err != nil {
		return model.Project{}, model.Plan{}, model.ProjectIdentifiers{}, objectDigests{}, err
	}
	if err := model.ValidateProjectIdentifiers(identifiers); err != nil {
		return model.Project{}, model.Plan{}, model.ProjectIdentifiers{}, objectDigests{}, err
	}
	projectDigest, err := digestObject(project)
	if err != nil {
		return model.Project{}, model.Plan{}, model.ProjectIdentifiers{}, objectDigests{}, err
	}
	planDigest, err := digestObject(plan)
	if err != nil {
		return model.Project{}, model.Plan{}, model.ProjectIdentifiers{}, objectDigests{}, err
	}
	identifiersDigest, err := digestObject(identifiers)
	if err != nil {
		return model.Project{}, model.Plan{}, model.ProjectIdentifiers{}, objectDigests{}, err
	}
	return project, plan, identifiers, objectDigests{project: projectDigest, plan: planDigest, identifiers: identifiersDigest}, nil
}

func digestObject(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func canonicalOnboardingPaths(projectID string) []string {
	return []string{
		fmt.Sprintf("gpt-tunnel/v1/projects/%s/project.json", projectID),
		fmt.Sprintf("gpt-tunnel/v1/projects/%s/plan/current.json", projectID),
		fmt.Sprintf("gpt-tunnel/v1/projects/%s/identifiers.json", projectID),
	}
}

func (c *Coordinator) inspectTarget(ctx context.Context, request Request, project model.Project, plan model.Plan, identifiers model.ProjectIdentifiers) (string, targetState, string, error) {
	paths := canonicalOnboardingPaths(request.ProjectID)
	collision, err := c.remoteCollision(ctx, request)
	if err != nil {
		return "", targetStateConflict, "", err
	}
	if collision {
		return "", targetStateConflict, "", errors.New("ONBOARDING_RECOVERY_REQUIRED: repository or project code collision")
	}
	present := 0
	exact := 0
	for index, path := range paths {
		data, err := c.Hub.ReadFile(ctx, path)
		if err != nil {
			if isHubNotFound(err) {
				continue
			}
			return "", targetStateConflict, "", err
		}
		present++
		var value any
		switch index {
		case 0:
			value = project
		case 1:
			value = plan
		case 2:
			value = identifiers
		}
		decoded, err := decodeHubObject(data, index)
		if err != nil {
			return "", targetStateConflict, "", err
		}
		canonical, err := json.MarshalIndent(decoded, "", "  ")
		if err != nil {
			return "", targetStateConflict, "", err
		}
		canonical = append(canonical, '\n')
		if !bytes.Equal(data, canonical) {
			return "", targetStateConflict, "", errors.New("target durable object is not canonical")
		}
		want, err := digestObject(value)
		if err != nil {
			return "", targetStateConflict, "", err
		}
		have, err := digestObject(decoded)
		if err == nil && want == have {
			exact++
		}
	}
	if present == 0 {
		revision, err := c.Hub.RemoteRevision(ctx)
		if err != nil {
			return "", targetStateConflict, "", err
		}
		return revision, targetStateEmpty, "", nil
	}
	if present == len(paths) && exact == len(paths) {
		revision, err := c.Hub.RemoteRevision(ctx)
		if err != nil {
			return "", targetStateConflict, "", err
		}
		after, err := c.commonPathLastChange(ctx, request.ProjectID)
		if err != nil {
			return "", targetStateConflict, "", err
		}
		return revision, targetStateExact, after, nil
	}
	return "", targetStateConflict, "", nil
}

func isHubNotFound(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "does not exist") || strings.Contains(message, "pathspec") || strings.Contains(message, "not found") || strings.Contains(message, "fatal: path")
}

func (c *Coordinator) commonPathLastChange(ctx context.Context, projectID string) (string, error) {
	paths := canonicalOnboardingPaths(projectID)
	var common string
	for _, path := range paths {
		lastChange, err := c.Hub.LastChange(ctx, path)
		if err != nil {
			return "", err
		}
		if common == "" {
			common = lastChange
			continue
		}
		if common != lastChange {
			return "", fmt.Errorf("onboarding paths have different last-change commits: %s versus %s", common, lastChange)
		}
	}
	if common == "" {
		return "", errors.New("onboarding paths have no common last-change commit")
	}
	return common, nil
}

func (c *Coordinator) validateCommittedHubState(ctx context.Context, request Request, receipt Receipt, project model.Project, plan model.Plan, identifiers model.ProjectIdentifiers, digests objectDigests) error {
	objects := []any{project, plan, identifiers}
	for index, path := range canonicalOnboardingPaths(request.ProjectID) {
		data, err := c.Hub.ReadFile(ctx, path)
		if err != nil {
			return fmt.Errorf("read committed onboarding object %s: %w", path, err)
		}
		decoded, err := decodeHubObject(data, index)
		if err != nil {
			return fmt.Errorf("decode committed onboarding object %s: %w", path, err)
		}
		canonical, err := json.MarshalIndent(decoded, "", "  ")
		if err != nil {
			return err
		}
		canonical = append(canonical, '\n')
		if !bytes.Equal(data, canonical) {
			return fmt.Errorf("committed onboarding object %s is not canonical", path)
		}
		want := []string{digests.project, digests.plan, digests.identifiers}[index]
		have, err := digestObject(decoded)
		if err != nil || have != want {
			return fmt.Errorf("committed onboarding object %s digest does not match receipt", path)
		}
		if !objectsMatch(decoded, objects[index]) {
			return fmt.Errorf("committed onboarding object %s does not match request", path)
		}
	}
	lastChange, err := c.commonPathLastChange(ctx, request.ProjectID)
	if err != nil {
		return err
	}
	if receipt.Hub.After == nil || lastChange != *receipt.Hub.After {
		return fmt.Errorf("committed onboarding last-change commit %s does not match recorded hub.after", lastChange)
	}
	return nil
}

func objectsMatch(left, right any) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}

func receiptHubTransaction(receipt Receipt, store hub.Store) hub.TransactionResult {
	after := ""
	if receipt.Hub.After != nil {
		after = *receipt.Hub.After
	}
	return hub.TransactionResult{Before: receipt.Hub.Before, After: after, Remote: hub.RemoteName, Branch: store.Config.Hub.Branch, Paths: append([]string(nil), receipt.Hub.Paths...)}
}

func (c *Coordinator) verifyRegistryAuthority(request Request, receipt Receipt, committed bool) (registryAuthority, error) {
	if request.Airelay.SessionKey == nil || !request.Airelay.SessionRequired {
		return registryAuthority{}, &CoordinatorError{Code: ErrOnboardingRecoveryRequired.Error(), Cause: errors.New("managed registry projection requires a required Airelay session key")}
	}
	current, err := config.LoadManagedProjects(c.StateDir)
	if err != nil {
		return registryAuthority{}, &CoordinatorError{Code: ErrOnboardingRecoveryRequired.Error(), Cause: fmt.Errorf("load managed project registry: %w", err)}
	}
	before, err := current.Digest()
	if err != nil {
		return registryAuthority{}, err
	}
	entry := config.ManagedProjectEntry{Root: request.Root, RepositoryURL: request.RepositoryURL, Remote: request.Remote, DefaultBranch: request.DefaultBranch, AirelaySessionKey: *request.Airelay.SessionKey}
	mirror := config.ManagedProjectMirrorPath(c.StateDir, request.ProjectID)
	for id, existing := range current.Projects {
		if id == request.ProjectID {
			if committed && before == receipt.RegistryDigests.ManagedAfterSHA256 && managedEntryEqual(existing, entry) {
				continue
			}
			return registryAuthority{}, &CoordinatorError{Code: ErrOnboardingRecoveryRequired.Error(), Cause: fmt.Errorf("managed registry project ID collision: %s", request.ProjectID)}
		}
		if existing.Root == entry.Root || config.ManagedProjectMirrorPath(c.StateDir, id) == mirror || existing.AirelaySessionKey == entry.AirelaySessionKey || existing.RepositoryURL == entry.RepositoryURL {
			return registryAuthority{}, &CoordinatorError{Code: ErrOnboardingRecoveryRequired.Error(), Cause: fmt.Errorf("managed registry collision with project %s", id)}
		}
	}

	if committed && before == receipt.RegistryDigests.ManagedAfterSHA256 {
		if existing, ok := current.Projects[request.ProjectID]; !ok || !managedEntryEqual(existing, entry) {
			return registryAuthority{}, &CoordinatorError{Code: ErrOnboardingRecoveryRequired.Error(), Cause: errors.New("managed registry after digest does not contain the exact onboarding entry")}
		}
		if _, err := config.EffectiveProjectsFromValidatedStatic(c.Hub.Config.Projects, current, c.StateDir); err != nil {
			return registryAuthority{}, &CoordinatorError{Code: ErrOnboardingRecoveryRequired.Error(), Cause: err}
		}
		return registryAuthority{Before: before, After: before}, nil
	}
	if before != receipt.RegistryDigests.ManagedBeforeSHA256 {
		return registryAuthority{}, &CoordinatorError{Code: ErrOnboardingRecoveryRequired.Error(), Cause: fmt.Errorf("managed registry before digest mismatch: got %s want %s", before, receipt.RegistryDigests.ManagedBeforeSHA256)}
	}
	if current.Revision >= config.MaxManagedProjectRegistryRevision {
		return registryAuthority{}, &CoordinatorError{Code: ErrOnboardingRecoveryRequired.Error(), Cause: errors.New("managed registry revision cannot advance")}
	}
	next := cloneManagedRegistry(current)
	next.Revision++
	next.Projects[request.ProjectID] = entry
	if _, err := config.EffectiveProjectsFromValidatedStatic(c.Hub.Config.Projects, next, c.StateDir); err != nil {
		return registryAuthority{}, &CoordinatorError{Code: ErrOnboardingRecoveryRequired.Error(), Cause: err}
	}
	after, err := next.Digest()
	if err != nil {
		return registryAuthority{}, err
	}
	if after != receipt.RegistryDigests.ManagedAfterSHA256 {
		return registryAuthority{}, &CoordinatorError{Code: ErrOnboardingRecoveryRequired.Error(), Cause: fmt.Errorf("managed registry after digest mismatch: got %s want %s", after, receipt.RegistryDigests.ManagedAfterSHA256)}
	}
	return registryAuthority{Before: before, After: after}, nil
}

func cloneManagedRegistry(current config.ManagedProjectRegistry) config.ManagedProjectRegistry {
	next := current
	next.Projects = make(map[string]config.ManagedProjectEntry, len(current.Projects)+1)
	for id, entry := range current.Projects {
		next.Projects[id] = entry
	}
	return next
}

func managedEntryEqual(left, right config.ManagedProjectEntry) bool {
	return left.Root == right.Root && left.RepositoryURL == right.RepositoryURL && left.Remote == right.Remote && left.DefaultBranch == right.DefaultBranch && left.AirelaySessionKey == right.AirelaySessionKey
}

func (c *Coordinator) remoteCollision(ctx context.Context, request Request) (bool, error) {
	projectPaths, err := c.Hub.List(ctx, "gpt-tunnel/v1/projects", "/project.json")
	if err != nil {
		return false, err
	}
	for _, path := range projectPaths {
		data, err := c.Hub.ReadFile(ctx, path)
		if err != nil {
			return false, err
		}
		var project model.Project
		if err := decodeStrictHubFile(data, &project); err != nil {
			return false, err
		}
		if err := model.ValidateProject(project); err != nil {
			return false, err
		}
		if project.ID == request.ProjectID && path != canonicalOnboardingPaths(request.ProjectID)[0] {
			return true, nil
		}
		if project.RepositoryURL == request.RepositoryURL && project.ID != request.ProjectID {
			return true, nil
		}
	}
	identifierPaths, err := c.Hub.List(ctx, "gpt-tunnel/v1/projects", "/identifiers.json")
	if err != nil {
		return false, err
	}
	for _, path := range identifierPaths {
		data, err := c.Hub.ReadFile(ctx, path)
		if err != nil {
			return false, err
		}
		var identifiers model.ProjectIdentifiers
		if err := decodeStrictHubFile(data, &identifiers); err != nil {
			return false, err
		}
		if err := model.ValidateProjectIdentifiers(identifiers); err != nil {
			return false, err
		}
		if identifiers.ProjectID == request.ProjectID && path != canonicalOnboardingPaths(request.ProjectID)[2] {
			return true, nil
		}
		if identifiers.ProjectID != request.ProjectID && identifiers.ProjectCode == request.ProjectCode {
			return true, nil
		}
	}
	return false, nil
}

func decodeHubObject(data []byte, index int) (any, error) {
	var destination any
	switch index {
	case 0:
		destination = &model.Project{}
	case 1:
		destination = &model.Plan{}
	case 2:
		destination = &model.ProjectIdentifiers{}
	default:
		return nil, errors.New("invalid onboarding object index")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("hub object has trailing content: %w", err)
	}
	switch value := destination.(type) {
	case *model.Project:
		if err := model.ValidateProject(*value); err != nil {
			return nil, err
		}
		return *value, nil
	case *model.Plan:
		if err := model.ValidatePlan(*value); err != nil {
			return nil, err
		}
		return *value, nil
	case *model.ProjectIdentifiers:
		if err := model.ValidateProjectIdentifiers(*value); err != nil {
			return nil, err
		}
		return *value, nil
	}
	return nil, errors.New("invalid onboarding object")
}

func validateWorktreeTarget(worktree string, request Request, project model.Project, plan model.Plan, identifiers model.ProjectIdentifiers) error {
	paths := canonicalOnboardingPaths(request.ProjectID)
	for _, path := range paths {
		if _, err := os.Lstat(filepath.Join(worktree, filepath.FromSlash(path))); err == nil {
			return fmt.Errorf("ONBOARDING_RECOVERY_REQUIRED: target path already exists: %s", path)
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	entries, err := scanWorktreeRecords(worktree)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Project.ID == project.ID || entry.Project.RepositoryURL == project.RepositoryURL || entry.Identifiers.ProjectCode == identifiers.ProjectCode {
			return fmt.Errorf("ONBOARDING_RECOVERY_REQUIRED: durable project or project code collision")
		}
	}
	_ = plan
	return nil
}

type worktreeRecord struct {
	Project     model.Project
	Identifiers model.ProjectIdentifiers
}

func scanWorktreeRecords(worktree string) ([]worktreeRecord, error) {
	root := filepath.Join(worktree, "gpt-tunnel", "v1", "projects")
	projects := map[string]model.Project{}
	identifiers := map[string]model.ProjectIdentifiers{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			if os.IsNotExist(walkErr) {
				return nil
			}
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		slash := filepath.ToSlash(path)
		if strings.HasSuffix(slash, "/project.json") {
			var value model.Project
			if err := decodeStrictHubFile(data, &value); err != nil {
				return err
			}
			if err := model.ValidateProject(value); err != nil {
				return err
			}
			projects[value.ID] = value
		} else if strings.HasSuffix(slash, "/identifiers.json") {
			var value model.ProjectIdentifiers
			if err := decodeStrictHubFile(data, &value); err != nil {
				return err
			}
			if err := model.ValidateProjectIdentifiers(value); err != nil {
				return err
			}
			identifiers[value.ProjectID] = value
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(projects))
	for id := range projects {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	result := make([]worktreeRecord, 0, len(ids))
	for _, id := range ids {
		result = append(result, worktreeRecord{Project: projects[id], Identifiers: identifiers[id]})
	}
	return result, nil
}

func decodeStrictHubFile(data []byte, out any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("hub object has trailing content")
		}
		return fmt.Errorf("hub object has trailing content: %w", err)
	}
	return nil
}

func committedReceipt(prepared Receipt, request Request, after string, project model.Project, plan model.Plan, identifiers model.ProjectIdentifiers, transaction bool) Receipt {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	preparedAt := prepared.Timestamps.PreparedAt
	return Receipt{
		SchemaVersion: prepared.SchemaVersion, OperationID: prepared.OperationID, RequestSHA256: prepared.RequestSHA256, State: StateHubCommitted, ProjectID: prepared.ProjectID,
		RepositoryProof: prepared.RepositoryProof, WorktreeProof: prepared.WorktreeProof, SessionProof: prepared.SessionProof, RegistryDigests: prepared.RegistryDigests,
		Hub:                HubProof{Before: prepared.Hub.Before, After: &after, Paths: append([]string(nil), prepared.Hub.Paths...)},
		CreatedProject:     &CreatedProject{ProjectID: project.ID, RepositoryURL: project.RepositoryURL, DefaultBranch: project.DefaultBranch, Status: project.Status, WorkflowRepository: optionalString(project.WorkflowRepository), WorkflowCommit: optionalString(project.WorkflowCommit)},
		CreatedPlan:        &CreatedPlan{SchemaVersion: PositiveInteger(plan.SchemaVersion), ProjectID: plan.ProjectID, Revision: PositiveInteger(plan.Revision), Path: canonicalOnboardingPaths(request.ProjectID)[1]},
		CreatedIdentifiers: &CreatedIdentifiers{SchemaVersion: PositiveInteger(identifiers.SchemaVersion), ProjectID: identifiers.ProjectID, ProjectCode: identifiers.ProjectCode, NextTaskNumber: PositiveInteger(identifiers.NextTaskNumber), NextADRNumber: PositiveInteger(identifiers.NextADRNumber)},
		Timestamps:         Timestamps{StartedAt: prepared.Timestamps.StartedAt, UpdatedAt: now, PreparedAt: preparedAt, HubCommittedAt: stringPointer(now)},
		Recovery:           Recovery{Status: "not_required"},
	}
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func stringPointer(value string) *string { return &value }
