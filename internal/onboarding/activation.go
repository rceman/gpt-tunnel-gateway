package onboarding

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/airelay"
	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/gitx"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
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
	RecoveryWrite func(string, Request, Receipt) (HubCommittedJournalWriteReceipt, error)
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
	if err := authority.RequireOnboarding(ctx); err != nil {
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
	case StateRecoveryRequired:
		if err := ValidateRecoveryReceipt(receipt, request); err != nil {
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
	var registryReceipt config.ManagedProjectRegistryWriteReceipt
	persistRecovery := func(step RecoveryStep, reason string, mirrorProof *MirrorProof) (ActivationResult, error) {
		candidate := receipt
		candidate.State = StateRecoveryRequired
		if mirrorProof != nil {
			copyProof := *mirrorProof
			candidate.MirrorProof = &copyProof
		}
		lastState := StateHubCommitted
		lastStep := step
		if receipt.State == StateRecoveryRequired && receipt.Recovery.LastDurableStep != nil && recoveryStepRank(*receipt.Recovery.LastDurableStep) > recoveryStepRank(lastStep) {
			lastStep = *receipt.Recovery.LastDurableStep
		}
		action := RecoveryActionResumeActivation
		candidate.Recovery = Recovery{Status: RecoveryRequired, LastCompletedState: &lastState, LastDurableStep: &lastStep, Reason: &reason, SafeCorrectiveAction: &action}
		now := time.Now().UTC()
		if c.Hooks.Now != nil {
			now = c.Hooks.Now().UTC()
		}
		candidate.Timestamps.UpdatedAt = now.Format(time.RFC3339Nano)
		writer := c.Hooks.RecoveryWrite
		if writer == nil {
			writer = writeRecoveryJournalLocked
		}
		journal, writeErr := writer(c.StateDir, request, candidate)
		result := ActivationResult{OperationID: operationID, ProjectID: request.ProjectID, State: StateRecoveryRequired, ReceiptSHA256: journal.ReceiptSHA256, RegistryBefore: registryReceipt.BeforeDigest, RegistryAfter: registryReceipt.AfterDigest}
		if candidate.MirrorProof != nil {
			result.Mirror = *candidate.MirrorProof
		}
		if writeErr != nil {
			return result, &CoordinatorError{Code: ErrOnboardingRecoveryRequired.Error(), Cause: fmt.Errorf("%s; recovery_required journal persistence failed: %w", reason, writeErr)}
		}
		return result, &CoordinatorError{Code: ErrOnboardingRecoveryRequired.Error(), Cause: errors.New(reason)}
	}
	if _, err := base.verifyRegistryAuthority(request, receipt, true); err != nil {
		if receipt.State == StateActivated {
			return ActivationResult{OperationID: operationID, ProjectID: request.ProjectID, State: StateActivated, Mirror: mirrorProofFromReceipt(receipt)}, &CoordinatorError{Code: ErrOnboardingRecoveryRequired.Error(), Cause: err}
		}
		return persistRecovery(RecoveryStepHubCommitted, "managed registry authority verification failed", nil)
	}
	if receipt.State == StateActivated {
		digest, err := ActivatedReceiptDigest(receipt, request)
		if err != nil {
			return ActivationResult{OperationID: operationID, ProjectID: request.ProjectID, State: StateActivated, Mirror: mirrorProofFromReceipt(receipt)}, &CoordinatorError{Code: ErrOnboardingRecoveryRequired.Error(), Cause: err}
		}
		return ActivationResult{OperationID: operationID, ProjectID: request.ProjectID, State: StateActivated, ReceiptSHA256: digest, RegistryBefore: receipt.RegistryDigests.ManagedBeforeSHA256, RegistryAfter: receipt.RegistryDigests.ManagedAfterSHA256, Mirror: mirrorProofFromReceipt(receipt), JournalRepairOnly: true}, nil
	}
	current, err := config.LoadManagedProjects(c.StateDir)
	if err != nil {
		return persistRecovery(RecoveryStepHubCommitted, "managed registry load failed", nil)
	}
	currentDigest, err := current.Digest()
	if err != nil {
		return persistRecovery(RecoveryStepHubCommitted, "managed registry digest failed", nil)
	}
	registryReceipt = config.ManagedProjectRegistryWriteReceipt{BeforeDigest: currentDigest, AfterDigest: currentDigest, BeforeRevision: current.Revision, AfterRevision: current.Revision}
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
			return persistRecovery(RecoveryStepHubCommitted, "managed registry activation failed", nil)
		}
	}
	registryStep := RecoveryStepManagedRegistry
	managed, err := config.LoadManagedProjects(c.StateDir)
	if err != nil {
		return persistRecovery(registryStep, "managed registry verification failed", nil)
	}
	effective, err := config.EffectiveProjectsFromValidatedStatic(c.Hub.Config.Projects, managed, c.StateDir)
	if err != nil {
		return persistRecovery(registryStep, "effective managed project validation failed", nil)
	}
	local, ok := effective[request.ProjectID]
	if !ok || local.Root != request.Root || local.Remote != request.Remote || local.DefaultBranch != request.DefaultBranch || local.AirelaySessionKey != sessionKey(request) {
		return persistRecovery(registryStep, "activated effective project does not match request", nil)
	}
	expectedMirrorPath := filepath.Clean(config.ManagedProjectMirrorPath(c.StateDir, request.ProjectID))
	if filepath.Clean(local.Mirror) != expectedMirrorPath {
		return persistRecovery(registryStep, "effective managed mirror path is not canonical", nil)
	}
	mirror := c.Hooks.Mirror
	if mirror == nil {
		mirror = func(ctx context.Context, p config.ProjectConfig, repositoryURL, branch string) (gitx.MirrorVerification, error) {
			return c.Git.ReconcileManagedMirror(ctx, p, repositoryURL, branch)
		}
	}
	mirrorResult, err := mirror(ctx, local, request.RepositoryURL, request.DefaultBranch)
	if err != nil {
		return persistRecovery(registryStep, "managed mirror activation failed", nil)
	}
	mirrorProof := &MirrorProof{Path: mirrorResult.Path, RepositoryURL: mirrorResult.RepositoryURL, Head: mirrorResult.Head}
	if filepath.Clean(mirrorResult.Path) != expectedMirrorPath || mirrorResult.RepositoryURL != request.RepositoryURL || validateMirrorProof(mirrorProof) != nil {
		return persistRecovery(registryStep, "managed mirror proof is invalid or non-canonical", nil)
	}
	mirrorStep := RecoveryStepManagedMirror
	projectReady := c.Hooks.ProjectReady
	if projectReady == nil {
		projectReady = c.defaultProjectReadiness
	}
	if err := projectReady(ctx, request, local, project, plan, identifiers); err != nil {
		return persistRecovery(mirrorStep, "project readiness failed", mirrorProof)
	}
	projectStep := RecoveryStepProjectReady
	sessionReady := c.Hooks.SessionReady
	if sessionReady == nil {
		sessionReady = c.defaultSessionReadiness
	}
	sessionProof, err := sessionReady(ctx, request)
	if err != nil {
		return persistRecovery(projectStep, "Airelay readiness failed", mirrorProof)
	}
	if !sameSessionProof(receipt.SessionProof, sessionProof) {
		return persistRecovery(RecoveryStepSessionReady, "Airelay readiness proof changed from the hub-committed receipt", mirrorProof)
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
	activated.MirrorProof = mirrorProof
	if receipt.MirrorProof != nil && !sameMirrorProof(receipt.MirrorProof, activated.MirrorProof) {
		return persistRecovery(RecoveryStepSessionReady, "managed mirror proof changed from the recovery receipt", activated.MirrorProof)
	}
	activated.Timestamps.ActivatedAt = &activatedAt
	activated.Timestamps.UpdatedAt = activatedAt
	activated.Recovery = Recovery{Status: "not_required"}
	journalWriter := c.Hooks.JournalWrite
	if journalWriter == nil {
		journalWriter = writeActivatedJournalLocked
	}
	journal, err := journalWriter(c.StateDir, request, activated)
	if err != nil {
		return persistRecovery(RecoveryStepSessionReady, "activated journal persistence failed", activated.MirrorProof)
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

func mirrorProofFromReceipt(receipt Receipt) MirrorProof {
	if receipt.MirrorProof == nil {
		return MirrorProof{}
	}
	return *receipt.MirrorProof
}

func sameSessionProof(left, right SessionProof) bool {
	if left.Required != right.Required || left.Status != right.Status || left.SessionKey == nil != (right.SessionKey == nil) || left.ControllerProtocolVersion == nil != (right.ControllerProtocolVersion == nil) {
		return false
	}
	if left.SessionKey != nil && *left.SessionKey != *right.SessionKey {
		return false
	}
	if left.ControllerProtocolVersion != nil && *left.ControllerProtocolVersion != *right.ControllerProtocolVersion {
		return false
	}
	return true
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
	switch status.State {
	case "running", "waiting", "idle":
	default:
		return SessionProof{}, errors.New("Airelay session is not explicitly ready")
	}
	if !status.ControllerReachable || status.ExitCode != 0 {
		return SessionProof{}, errors.New("Airelay session is not explicitly ready")
	}
	protocol := PositiveInteger(1)
	parsed, err := strconv.ParseUint(status.ProtocolVersion, 10, 64)
	if err != nil || parsed < 1 || parsed > MaxSafeInteger {
		return SessionProof{}, errors.New("Airelay status has no valid positive protocol version")
	}
	protocol = PositiveInteger(parsed)
	return SessionProof{Required: true, SessionKey: &key, Status: "active", ControllerProtocolVersion: &protocol}, nil
}
