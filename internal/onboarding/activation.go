package onboarding

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/airelay"
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/gitx"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

type ActivationResult struct {
	OperationID       string
	ProjectID         string
	State             ReceiptState
	ReceiptSHA256     string
	RegistryBefore    string
	RegistryAfter     string
	Mirror            MirrorProof
	JournalRepairOnly bool
}

type ActivationHooks struct {
	RegistryWrite func(string, string, config.ManagedProjectRegistry) (config.ManagedProjectRegistryWriteReceipt, error)
	Mirror        func(context.Context, config.ProjectConfig, string, string) (gitx.MirrorVerification, error)
	ProjectReady  func(context.Context, Request, config.ProjectConfig, model.Project, model.Plan, model.ProjectIdentifiers) error
	SessionReady  func(context.Context, Request) (SessionProof, error)
	JournalWrite  func(string, Request, Receipt) (HubCommittedJournalWriteReceipt, error)
	Now           func() time.Time
}

type ActivationCoordinator struct {
	Hub      hub.Store
	StateDir string
	Git      gitx.Runner
	Airelay  airelay.Client
	Hooks    ActivationHooks
}

func NewActivationCoordinator(store hub.Store) *ActivationCoordinator {
	return &ActivationCoordinator{
		Hub:      store,
		StateDir: store.Config.StateDir,
		Git:      gitx.Runner{MaxReadBytes: store.Config.MaxReadBytes, MaxDiffBytes: store.Config.MaxDiffBytes, MaxListItems: store.Config.MaxListItems},
		Airelay:  airelay.Client{Command: store.Config.AirelayCommand, Timeout: time.Duration(store.Config.DispatchTimeoutSeconds) * time.Second, MaxMessageBytes: 256},
	}
}

func (c *ActivationCoordinator) Activate(ctx context.Context, request Request, operationID string) (ActivationResult, error) {
	if err := service.RequireWorkflowPolicyAuthority(ctx); err != nil {
		return ActivationResult{}, &CoordinatorError{Code: ErrOnboardingAuthorityUnavailable.Error(), Cause: err}
	}
	if c == nil || c.StateDir == "" || c.StateDir != request.GatewayStateDir {
		return ActivationResult{}, &CoordinatorError{Code: ErrOnboardingRecoveryRequired.Error(), Cause: errors.New("activation state directory is unavailable or does not match request")}
	}
	if err := ValidateRequest(request); err != nil {
		return ActivationResult{}, &CoordinatorError{Code: ErrOnboardingRecoveryRequired.Error(), Cause: err}
	}
	if err := validatePreparedJournalOperationID(operationID); err != nil {
		return ActivationResult{}, err
	}
	requestDigest, err := RequestDigest(request)
	if err != nil {
		return ActivationResult{}, err
	}
	operationLock, err := acquirePreparedJournalLock(c.StateDir, operationID)
	if err != nil {
		return ActivationResult{}, err
	}
	defer operationLock.Release()
	receipt, err := LoadOnboardingJournal(c.StateDir, operationID)
	if err != nil {
		return ActivationResult{}, &CoordinatorError{Code: ErrOnboardingRecoveryRequired.Error(), Cause: err}
	}
	if receipt.OperationID != operationID || receipt.RequestSHA256 != requestDigest || receipt.ProjectID != request.ProjectID {
		return ActivationResult{}, &CoordinatorError{Code: ErrOnboardingOperationConflict.Error(), Cause: errors.New("activation journal identity does not match request")}
	}
	managedLock, err := acquireManagedProjectsLock(ctx, c.StateDir)
	if err != nil {
		return ActivationResult{}, &CoordinatorError{Code: ErrOnboardingRecoveryRequired.Error(), Cause: err}
	}
	defer managedLock.Release()

	base := Coordinator{Hub: c.Hub, StateDir: c.StateDir}
	project, plan, identifiers, objectDigests, err := buildDurableObjects(request)
	if err != nil {
		return ActivationResult{}, &CoordinatorError{Code: ErrOnboardingRecoveryRequired.Error(), Cause: err}
	}
	switch receipt.State {
	case StateHubCommitted:
		if err := ValidateHubCommittedReceipt(receipt, request); err != nil {
			return ActivationResult{}, &CoordinatorError{Code: ErrOnboardingRecoveryRequired.Error(), Cause: err}
		}
	case StateActivated:
		if err := ValidateActivatedReceipt(receipt, request); err != nil {
			return ActivationResult{}, &CoordinatorError{Code: ErrOnboardingRecoveryRequired.Error(), Cause: err}
		}
	default:
		return ActivationResult{}, &CoordinatorError{Code: ErrOnboardingRecoveryRequired.Error(), Cause: fmt.Errorf("activation requires hub_committed or activated journal, got %q", receipt.State)}
	}
	if err := base.validateCommittedHubState(ctx, request, receipt, project, plan, identifiers, objectDigests); err != nil {
		return ActivationResult{}, &CoordinatorError{Code: ErrOnboardingRecoveryRequired.Error(), Cause: err}
	}
	authority, err := base.verifyRegistryAuthority(request, receipt, true)
	if err != nil {
		return ActivationResult{}, err
	}
	current, err := config.LoadManagedProjects(c.StateDir)
	if err != nil {
		return ActivationResult{}, &CoordinatorError{Code: ErrOnboardingRecoveryRequired.Error(), Cause: err}
	}
	currentDigest, err := current.Digest()
	if err != nil {
		return ActivationResult{}, &CoordinatorError{Code: ErrOnboardingRecoveryRequired.Error(), Cause: err}
	}
	registryReceipt := config.ManagedProjectRegistryWriteReceipt{BeforeDigest: currentDigest, AfterDigest: currentDigest, BeforeRevision: current.Revision, AfterRevision: current.Revision}
	if currentDigest != receipt.RegistryDigests.ManagedAfterSHA256 {
		next := cloneManagedRegistry(current)
		next.Revision++
		next.Projects[request.ProjectID] = managedEntryForRequest(request)
		writer := c.Hooks.RegistryWrite
		if writer == nil {
			writer = config.WriteManagedProjectRegistryLocked
		}
		registryReceipt, err = writer(c.StateDir, currentDigest, next)
		if err != nil {
			return ActivationResult{OperationID: operationID, ProjectID: request.ProjectID, State: StateRecoveryRequired, RegistryBefore: currentDigest, RegistryAfter: authority.After}, &CoordinatorError{Code: ErrOnboardingRecoveryRequired.Error(), Cause: fmt.Errorf("managed registry activation failed: %w", err)}
		}
	}
	managed, err := config.LoadManagedProjects(c.StateDir)
	if err != nil {
		return ActivationResult{OperationID: operationID, ProjectID: request.ProjectID, State: StateRecoveryRequired, RegistryBefore: registryReceipt.BeforeDigest, RegistryAfter: registryReceipt.AfterDigest}, &CoordinatorError{Code: ErrOnboardingRecoveryRequired.Error(), Cause: err}
	}
	effective, err := config.EffectiveProjectsFromValidatedStatic(c.Hub.Config.Projects, managed, c.StateDir)
	if err != nil {
		return ActivationResult{OperationID: operationID, ProjectID: request.ProjectID, State: StateRecoveryRequired, RegistryBefore: registryReceipt.BeforeDigest, RegistryAfter: registryReceipt.AfterDigest}, &CoordinatorError{Code: ErrOnboardingRecoveryRequired.Error(), Cause: err}
	}
	local, ok := effective[request.ProjectID]
	if !ok || local.Root != request.Root || local.Remote != request.Remote || local.DefaultBranch != request.DefaultBranch || local.AirelaySessionKey != sessionKey(request) {
		return ActivationResult{OperationID: operationID, ProjectID: request.ProjectID, State: StateRecoveryRequired, RegistryBefore: registryReceipt.BeforeDigest, RegistryAfter: registryReceipt.AfterDigest}, &CoordinatorError{Code: ErrOnboardingRecoveryRequired.Error(), Cause: errors.New("activated effective project does not match request")}
	}
	mirror := c.Hooks.Mirror
	if mirror == nil {
		mirror = func(ctx context.Context, p config.ProjectConfig, repositoryURL, branch string) (gitx.MirrorVerification, error) {
			return c.Git.ReconcileManagedMirror(ctx, p, repositoryURL, branch)
		}
	}
	mirrorResult, err := mirror(ctx, local, request.RepositoryURL, request.DefaultBranch)
	if err != nil {
		return ActivationResult{OperationID: operationID, ProjectID: request.ProjectID, State: StateRecoveryRequired, RegistryBefore: registryReceipt.BeforeDigest, RegistryAfter: registryReceipt.AfterDigest}, &CoordinatorError{Code: ErrOnboardingRecoveryRequired.Error(), Cause: fmt.Errorf("managed mirror activation failed: %w", err)}
	}
	projectReady := c.Hooks.ProjectReady
	if projectReady == nil {
		projectReady = c.defaultProjectReadiness
	}
	if err := projectReady(ctx, request, local, project, plan, identifiers); err != nil {
		return ActivationResult{OperationID: operationID, ProjectID: request.ProjectID, State: StateRecoveryRequired, RegistryBefore: registryReceipt.BeforeDigest, RegistryAfter: registryReceipt.AfterDigest, Mirror: MirrorProof{Path: mirrorResult.Path, RepositoryURL: mirrorResult.RepositoryURL, Head: mirrorResult.Head}}, &CoordinatorError{Code: ErrOnboardingRecoveryRequired.Error(), Cause: fmt.Errorf("project readiness failed: %w", err)}
	}
	sessionReady := c.Hooks.SessionReady
	if sessionReady == nil {
		sessionReady = c.defaultSessionReadiness
	}
	sessionProof, err := sessionReady(ctx, request)
	if err != nil {
		return ActivationResult{OperationID: operationID, ProjectID: request.ProjectID, State: StateRecoveryRequired, RegistryBefore: registryReceipt.BeforeDigest, RegistryAfter: registryReceipt.AfterDigest, Mirror: MirrorProof{Path: mirrorResult.Path, RepositoryURL: mirrorResult.RepositoryURL, Head: mirrorResult.Head}}, &CoordinatorError{Code: ErrOnboardingRecoveryRequired.Error(), Cause: fmt.Errorf("Airelay readiness failed: %w", err)}
	}
	now := time.Now().UTC()
	if c.Hooks.Now != nil {
		now = c.Hooks.Now().UTC()
	}
	if receipt.State == StateActivated {
		digest, err := ActivatedReceiptDigest(receipt, request)
		if err != nil {
			return ActivationResult{OperationID: operationID, ProjectID: request.ProjectID, State: StateRecoveryRequired, RegistryBefore: registryReceipt.BeforeDigest, RegistryAfter: registryReceipt.AfterDigest, Mirror: *receipt.MirrorProof}, &CoordinatorError{Code: ErrOnboardingRecoveryRequired.Error(), Cause: err}
		}
		return ActivationResult{OperationID: operationID, ProjectID: request.ProjectID, State: StateActivated, ReceiptSHA256: digest, RegistryBefore: registryReceipt.BeforeDigest, RegistryAfter: registryReceipt.AfterDigest, Mirror: *receipt.MirrorProof, JournalRepairOnly: true}, nil
	}
	activatedAt := now.Format(time.RFC3339Nano)
	activated := receipt
	activated.State = StateActivated
	activated.MirrorProof = &MirrorProof{Path: mirrorResult.Path, RepositoryURL: mirrorResult.RepositoryURL, Head: mirrorResult.Head}
	activated.SessionProof = sessionProof
	activated.Timestamps.ActivatedAt = &activatedAt
	activated.Timestamps.UpdatedAt = activatedAt
	activated.Recovery = Recovery{Status: "not_required"}
	journalWriter := c.Hooks.JournalWrite
	if journalWriter == nil {
		journalWriter = writeActivatedJournalLocked
	}
	journal, err := journalWriter(c.StateDir, request, activated)
	if err != nil {
		return ActivationResult{OperationID: operationID, ProjectID: request.ProjectID, State: StateRecoveryRequired, RegistryBefore: registryReceipt.BeforeDigest, RegistryAfter: registryReceipt.AfterDigest, Mirror: *activated.MirrorProof}, &CoordinatorError{Code: ErrOnboardingRecoveryRequired.Error(), Cause: fmt.Errorf("activation journal persistence failed after registry activation: %w", err)}
	}
	return ActivationResult{OperationID: operationID, ProjectID: request.ProjectID, State: StateActivated, ReceiptSHA256: journal.ReceiptSHA256, RegistryBefore: registryReceipt.BeforeDigest, RegistryAfter: registryReceipt.AfterDigest, Mirror: *activated.MirrorProof, JournalRepairOnly: receipt.State == StateActivated}, nil
}

func managedEntryForRequest(request Request) config.ManagedProjectEntry {
	return config.ManagedProjectEntry{Root: request.Root, RepositoryURL: request.RepositoryURL, Remote: request.Remote, DefaultBranch: request.DefaultBranch, AirelaySessionKey: sessionKey(request)}
}

func sessionKey(request Request) string {
	if request.Airelay.SessionKey == nil {
		return ""
	}
	return *request.Airelay.SessionKey
}

func (c *ActivationCoordinator) defaultProjectReadiness(ctx context.Context, request Request, local config.ProjectConfig, project model.Project, plan model.Plan, identifiers model.ProjectIdentifiers) error {
	if err := model.ValidateProject(project); err != nil {
		return err
	}
	if err := model.ValidatePlan(plan); err != nil {
		return err
	}
	if err := model.ValidateProjectIdentifiers(identifiers); err != nil {
		return err
	}
	if local.Root != request.Root || local.Remote != request.Remote || local.DefaultBranch != request.DefaultBranch || local.AirelaySessionKey != sessionKey(request) {
		return errors.New("effective project metadata does not match request")
	}
	status, err := c.Git.WorktreeStatus(ctx, local)
	if err != nil {
		return err
	}
	if !status.Clean {
		return errors.New("project worktree is dirty")
	}
	remoteURL, err := c.Git.RemoteURL(ctx, local)
	if err != nil {
		return err
	}
	if remoteURL != request.RepositoryURL {
		return errors.New("project remote URL does not match request")
	}
	branch, err := c.Git.RemoteDefaultBranch(ctx, local)
	if err != nil {
		return err
	}
	if branch != request.DefaultBranch {
		return errors.New("project remote default branch does not match request")
	}
	return nil
}

func (c *ActivationCoordinator) defaultSessionReadiness(ctx context.Context, request Request) (SessionProof, error) {
	if !request.Airelay.SessionRequired {
		return SessionProof{Required: false, Status: "not_required"}, nil
	}
	key := sessionKey(request)
	status, err := c.Airelay.Status(ctx, key)
	if err != nil {
		return SessionProof{}, err
	}
	if !status.ControllerReachable || status.State == "error" {
		return SessionProof{}, errors.New("Airelay controller or session is not ready")
	}
	protocol := PositiveInteger(1)
	if parsed, err := strconv.ParseUint(status.ProtocolVersion, 10, 64); err == nil && parsed >= 1 && parsed <= MaxSafeInteger {
		protocol = PositiveInteger(parsed)
	}
	return SessionProof{Required: true, SessionKey: &key, Status: "active", ControllerProtocolVersion: &protocol}, nil
}
