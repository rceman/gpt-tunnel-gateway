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
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
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
	switch receipt.State {
	case StateHubCommitted:
		if err := ValidateHubCommittedReceipt(receipt, request); err != nil {
			return Result{}, &CoordinatorError{Code: ErrOnboardingOperationConflict.Error(), Cause: err}
		}
		digest, err := HubCommittedReceiptDigest(receipt, request)
		if err != nil {
			return Result{}, err
		}
		return Result{OperationID: operationID, ProjectID: request.ProjectID, State: StateHubCommitted, RequestSHA256: requestDigest, ReceiptSHA256: digest}, nil
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
	if err := c.Hub.Ensure(ctx); err != nil {
		return Result{}, err
	}

	currentRevision, state, err := c.inspectTarget(ctx, request, project, plan, identifiers)
	if err != nil {
		return Result{}, err
	}
	if state == targetStateConflict {
		return Result{}, &CoordinatorError{Code: ErrOnboardingRecoveryRequired.Error(), Cause: errors.New("target durable objects are partial or conflicting")}
	}
	if state == targetStateExact {
		committed := committedReceipt(receipt, request, currentRevision, project, plan, identifiers, false)
		journal, err := writeHubCommittedJournalLocked(c.StateDir, request, committed)
		if err != nil {
			return Result{}, &CoordinatorError{Code: ErrOnboardingRecoveryRequired.Error(), Cause: err}
		}
		return Result{OperationID: operationID, ProjectID: request.ProjectID, State: StateHubCommitted, RequestSHA256: requestDigest, ReceiptSHA256: journal.ReceiptSHA256, JournalRepairOnly: true}, nil
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
	committed := committedReceipt(receipt, request, transaction.After, project, plan, identifiers, true)
	journal, err := writeHubCommittedJournalLocked(c.StateDir, request, committed)
	if err != nil {
		return Result{OperationID: operationID, ProjectID: request.ProjectID, State: StatePrepared, RequestSHA256: requestDigest, Hub: transaction, HubTransaction: true}, &CoordinatorError{Code: ErrOnboardingRecoveryRequired.Error(), Cause: fmt.Errorf("hub committed at %s but journal reconciliation failed: %w", transaction.After, err)}
	}
	return Result{OperationID: operationID, ProjectID: request.ProjectID, State: StateHubCommitted, RequestSHA256: requestDigest, ReceiptSHA256: journal.ReceiptSHA256, Hub: transaction, HubTransaction: true}, nil
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

func (c *Coordinator) inspectTarget(ctx context.Context, request Request, project model.Project, plan model.Plan, identifiers model.ProjectIdentifiers) (string, targetState, error) {
	paths := canonicalOnboardingPaths(request.ProjectID)
	collision, err := c.remoteCollision(ctx, request)
	if err != nil {
		return "", targetStateConflict, err
	}
	if collision {
		return "", targetStateConflict, errors.New("ONBOARDING_RECOVERY_REQUIRED: repository or project code collision")
	}
	present := 0
	exact := 0
	for index, path := range paths {
		data, err := c.Hub.ReadFile(ctx, path)
		if err != nil {
			if isHubNotFound(err) {
				continue
			}
			return "", targetStateConflict, err
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
			return "", targetStateConflict, err
		}
		canonical, err := json.MarshalIndent(decoded, "", "  ")
		if err != nil {
			return "", targetStateConflict, err
		}
		canonical = append(canonical, '\n')
		if !bytes.Equal(data, canonical) {
			return "", targetStateConflict, errors.New("target durable object is not canonical")
		}
		want, err := digestObject(value)
		if err != nil {
			return "", targetStateConflict, err
		}
		have, err := digestObject(decoded)
		if err == nil && want == have {
			exact++
		}
	}
	if present == 0 {
		revision, err := c.Hub.RemoteRevision(ctx)
		if err != nil {
			return "", targetStateConflict, err
		}
		return revision, targetStateEmpty, nil
	}
	if present == len(paths) && exact == len(paths) {
		revision, err := c.Hub.RemoteRevision(ctx)
		if err != nil {
			return "", targetStateConflict, err
		}
		return revision, targetStateExact, nil
	}
	return "", targetStateConflict, nil
}

func isHubNotFound(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "does not exist") || strings.Contains(message, "pathspec") || strings.Contains(message, "not found") || strings.Contains(message, "fatal: path")
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
