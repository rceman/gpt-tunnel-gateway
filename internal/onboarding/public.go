package onboarding

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
)

// PublicInput is the one strict input shared by the three O4 transport
// surfaces. Authority is deliberately not serializable in this type.
type PublicInput struct {
	OperationID string  `json:"operation_id"`
	Request     Request `json:"request"`
}

type PublicResult struct {
	OperationID       string                `json:"operation_id"`
	ProjectID         string                `json:"project_id"`
	State             ReceiptState          `json:"state"`
	RequestSHA256     string                `json:"request_sha256"`
	ReceiptSHA256     string                `json:"receipt_sha256"`
	Hub               hub.TransactionResult `json:"hub"`
	HubTransaction    bool                  `json:"hub_transaction"`
	JournalRepairOnly bool                  `json:"journal_repair_only"`
	RegistryBefore    string                `json:"registry_before"`
	RegistryAfter     string                `json:"registry_after"`
	MirrorReady       bool                  `json:"mirror_ready"`
	RecoveryStatus    string                `json:"recovery_status"`
}

// StatusProjection intentionally contains no local root, gateway state path,
// mirror path, session key or other capability-bearing material.
type StatusProjection struct {
	OperationID    string       `json:"operation_id"`
	ProjectID      string       `json:"project_id"`
	State          ReceiptState `json:"state"`
	RequestSHA256  string       `json:"request_sha256"`
	ReceiptSHA256  string       `json:"receipt_sha256"`
	StartedAt      string       `json:"started_at"`
	UpdatedAt      string       `json:"updated_at"`
	RecoveryStatus string       `json:"recovery_status"`
	RecoveryStep   string       `json:"recovery_step"`
	HubBefore      string       `json:"hub_before"`
	HubAfter       string       `json:"hub_after"`
	HubCommitted   bool         `json:"hub_committed"`
	RegistryBefore string       `json:"registry_before"`
	RegistryAfter  string       `json:"registry_after"`
	RegistryReady  bool         `json:"registry_ready"`
	MirrorReady    bool         `json:"mirror_ready"`
	ProjectReady   bool         `json:"project_ready"`
	SessionReady   bool         `json:"session_ready"`
}

type PublicOrchestrator struct {
	Hub         hub.Store
	StateDir    string
	Coordinator *Coordinator
	Activation  *ActivationCoordinator
}

func NewPublicOrchestrator(store hub.Store) *PublicOrchestrator {
	return &PublicOrchestrator{
		Hub: store, StateDir: store.Config.StateDir,
		Coordinator: NewCoordinator(store), Activation: NewActivationCoordinator(store),
	}
}

func (o *PublicOrchestrator) Onboard(ctx context.Context, in PublicInput) (PublicResult, error) {
	if err := authority.RequireOnboarding(ctx); err != nil {
		return PublicResult{}, &CoordinatorError{Code: ErrOnboardingAuthorityUnavailable.Error(), Cause: err}
	}
	if err := ValidateRequest(in.Request); err != nil {
		return PublicResult{}, fmt.Errorf("invalid onboarding request: %w", err)
	}
	if err := validatePublicOperation(in.OperationID); err != nil {
		return PublicResult{}, err
	}
	receipt, err := LoadOnboardingJournal(o.StateDir, in.OperationID)
	if errors.Is(err, ErrPreparedJournalNotFound) {
		receipt, err = o.prepare(ctx, in.Request, in.OperationID)
	}
	if err != nil {
		return PublicResult{}, &CoordinatorError{Code: ErrOnboardingRecoveryRequired.Error(), Cause: err}
	}
	return o.advance(ctx, in.Request, in.OperationID, receipt)
}

func (o *PublicOrchestrator) Recover(ctx context.Context, in PublicInput) (PublicResult, error) {
	if err := authority.RequireOnboarding(ctx); err != nil {
		return PublicResult{}, &CoordinatorError{Code: ErrOnboardingAuthorityUnavailable.Error(), Cause: err}
	}
	if err := ValidateRequest(in.Request); err != nil {
		return PublicResult{}, fmt.Errorf("invalid onboarding request: %w", err)
	}
	if err := validatePublicOperation(in.OperationID); err != nil {
		return PublicResult{}, err
	}
	receipt, err := LoadOnboardingJournal(o.StateDir, in.OperationID)
	if err != nil {
		return PublicResult{}, &CoordinatorError{Code: ErrOnboardingRecoveryRequired.Error(), Cause: err}
	}
	return o.advance(ctx, in.Request, in.OperationID, receipt)
}

func (o *PublicOrchestrator) Status(_ context.Context, in PublicInput) (StatusProjection, error) {
	if err := ValidateRequest(in.Request); err != nil {
		return StatusProjection{}, fmt.Errorf("invalid onboarding request: %w", err)
	}
	if err := validatePublicOperation(in.OperationID); err != nil {
		return StatusProjection{}, err
	}
	receipt, err := LoadOnboardingJournal(o.StateDir, in.OperationID)
	if err != nil {
		return StatusProjection{}, &CoordinatorError{Code: ErrOnboardingRecoveryRequired.Error(), Cause: err}
	}
	if err := validateReceiptForState(receipt, in.Request); err != nil {
		return StatusProjection{}, &CoordinatorError{Code: ErrOnboardingRecoveryRequired.Error(), Cause: err}
	}
	return publicStatusProjection(receipt, in.Request)
}

func (o *PublicOrchestrator) advance(ctx context.Context, request Request, operationID string, receipt Receipt) (PublicResult, error) {
	if receipt.State == StatePrepared || receipt.State == StateHubCommitted {
		result, err := o.Coordinator.Execute(ctx, request, operationID)
		if err != nil {
			return publicCoordinatorResult(result), err
		}
		receipt, err = LoadOnboardingJournal(o.StateDir, operationID)
		if err != nil {
			return publicCoordinatorResult(result), err
		}
	}
	if receipt.State == StateRecoveryRequired || receipt.State == StateActivated || receipt.State == StateHubCommitted {
		activation, err := o.Activation.Activate(ctx, request, operationID)
		return publicActivationResult(activation), err
	}
	return PublicResult{}, &CoordinatorError{Code: ErrOnboardingRecoveryRequired.Error(), Cause: fmt.Errorf("unsupported onboarding state %q", receipt.State)}
}

func (o *PublicOrchestrator) prepare(ctx context.Context, request Request, operationID string) (Receipt, error) {
	if o == nil || o.Activation == nil {
		return Receipt{}, errors.New("onboarding orchestrator is unavailable")
	}
	project, plan, identifiers, digests, err := buildDurableObjects(request)
	if err != nil {
		return Receipt{}, err
	}
	local := config.ProjectConfig{Root: request.Root, Remote: request.Remote, DefaultBranch: request.DefaultBranch, Mirror: config.ManagedProjectMirrorPath(request.GatewayStateDir, request.ProjectID), AirelaySessionKey: sessionKey(request)}
	if err := o.Activation.defaultProjectReadiness(ctx, request, local, project, plan, identifiers); err != nil {
		return Receipt{}, err
	}
	status, err := o.Activation.Git.WorktreeStatus(ctx, local)
	if err != nil {
		return Receipt{}, err
	}
	if status.Branch != request.DefaultBranch || !status.Clean {
		return Receipt{}, errors.New("source worktree is not clean on the default branch")
	}
	session, err := o.Activation.defaultSessionReadiness(ctx, request)
	if err != nil {
		return Receipt{}, err
	}
	managed, err := config.LoadManagedProjects(request.GatewayStateDir)
	if err != nil {
		return Receipt{}, err
	}
	managedBefore, err := managed.Digest()
	if err != nil {
		return Receipt{}, err
	}
	if managed.Revision >= config.MaxManagedProjectRegistryRevision {
		return Receipt{}, errors.New("managed project registry revision cannot advance")
	}
	next := cloneManagedRegistry(managed)
	next.Revision++
	next.Projects[request.ProjectID] = managedEntryForRequest(request)
	managedAfter, err := next.Digest()
	if err != nil {
		return Receipt{}, err
	}
	requestDigest, err := RequestDigest(request)
	if err != nil {
		return Receipt{}, err
	}
	now, err := parseReceiptTime(request.InitialPlan.UpdatedAt)
	if err != nil {
		return Receipt{}, err
	}
	started := now.Add(-time.Nanosecond).Format(time.RFC3339Nano)
	prepared := now.Format(time.RFC3339Nano)
	statusDigest := sha256.Sum256([]byte(status.Porcelain))
	statusHash := hex.EncodeToString(statusDigest[:])
	return Receipt{
		SchemaVersion: 1, OperationID: operationID, RequestSHA256: requestDigest, State: StatePrepared, ProjectID: request.ProjectID,
		RepositoryProof: RepositoryProof{Root: request.Root, Remote: request.Remote, RepositoryURL: request.RepositoryURL, DefaultBranch: request.DefaultBranch, Branch: status.Branch, Head: status.Head, GatewayStateDir: request.GatewayStateDir},
		WorktreeProof:   WorktreeProof{Clean: status.Clean, StatusSHA256: statusHash}, SessionProof: session,
		RegistryDigests: RegistryDigests{ManagedBeforeSHA256: managedBefore, ManagedAfterSHA256: managedAfter, ProjectSHA256: digests.project, PlanSHA256: digests.plan, IdentifiersSHA256: digests.identifiers},
		Hub:             HubProof{Before: request.ExpectedHubRevision, Paths: canonicalOnboardingPaths(request.ProjectID)},
		Timestamps:      Timestamps{StartedAt: started, PreparedAt: stringPtr(prepared), UpdatedAt: prepared}, Recovery: Recovery{Status: RecoveryNotRequired},
	}, nil
}

func validatePublicOperation(operationID string) error {
	_, err := PreparedJournalPath("/tmp", operationID)
	return err
}

func validateReceiptForState(receipt Receipt, request Request) error {
	switch receipt.State {
	case StatePrepared:
		return ValidatePreparedReceipt(receipt, request)
	case StateHubCommitted:
		return ValidateHubCommittedReceipt(receipt, request)
	case StateActivated:
		return ValidateActivatedReceipt(receipt, request)
	case StateRecoveryRequired:
		return ValidateRecoveryReceipt(receipt, request)
	default:
		return fmt.Errorf("unsupported onboarding journal state %q", receipt.State)
	}
}

func publicStatusProjection(receipt Receipt, request Request) (StatusProjection, error) {
	digest, err := receiptDigestForState(receipt, request)
	if err != nil {
		return StatusProjection{}, err
	}
	var after string
	if receipt.Hub.After != nil {
		after = *receipt.Hub.After
	}
	step := ""
	if receipt.Recovery.LastDurableStep != nil {
		step = string(*receipt.Recovery.LastDurableStep)
	}
	hubReady, registryReady, mirrorReady, projectReady, sessionReady := readinessForReceipt(receipt)
	return StatusProjection{OperationID: receipt.OperationID, ProjectID: receipt.ProjectID, State: receipt.State, RequestSHA256: receipt.RequestSHA256, ReceiptSHA256: digest, StartedAt: receipt.Timestamps.StartedAt, UpdatedAt: receipt.Timestamps.UpdatedAt, RecoveryStatus: string(receipt.Recovery.Status), RecoveryStep: step, HubBefore: receipt.Hub.Before, HubAfter: after, HubCommitted: hubReady, RegistryBefore: receipt.RegistryDigests.ManagedBeforeSHA256, RegistryAfter: receipt.RegistryDigests.ManagedAfterSHA256, RegistryReady: registryReady, MirrorReady: mirrorReady, ProjectReady: projectReady, SessionReady: sessionReady}, nil
}

func readinessForReceipt(receipt Receipt) (bool, bool, bool, bool, bool) {
	if receipt.State == StateActivated {
		return true, true, true, true, true
	}
	if receipt.State == StateHubCommitted {
		return true, false, false, false, false
	}
	if receipt.State != StateRecoveryRequired || receipt.Recovery.LastDurableStep == nil {
		return false, false, false, false, false
	}
	switch *receipt.Recovery.LastDurableStep {
	case RecoveryStepSessionReady:
		return true, true, true, true, true
	case RecoveryStepProjectReady:
		return true, true, true, true, false
	case RecoveryStepManagedMirror:
		return true, true, true, false, false
	case RecoveryStepManagedRegistry:
		return true, true, false, false, false
	case RecoveryStepHubCommitted:
		return true, false, false, false, false
	default:
		return false, false, false, false, false
	}
}

func receiptDigestForState(receipt Receipt, request Request) (string, error) {
	switch receipt.State {
	case StatePrepared:
		return PreparedReceiptDigest(receipt, request)
	case StateHubCommitted:
		return HubCommittedReceiptDigest(receipt, request)
	case StateRecoveryRequired:
		return RecoveryReceiptDigest(receipt, request)
	case StateActivated:
		return ActivatedReceiptDigest(receipt, request)
	default:
		return "", fmt.Errorf("unsupported onboarding journal state %q", receipt.State)
	}
}

func publicCoordinatorResult(result Result) PublicResult {
	return PublicResult{OperationID: result.OperationID, ProjectID: result.ProjectID, State: result.State, RequestSHA256: result.RequestSHA256, ReceiptSHA256: result.ReceiptSHA256, Hub: result.Hub, HubTransaction: result.HubTransaction, JournalRepairOnly: result.JournalRepairOnly}
}

func publicActivationResult(result ActivationResult) PublicResult {
	return PublicResult{OperationID: result.OperationID, ProjectID: result.ProjectID, State: result.State, ReceiptSHA256: result.ReceiptSHA256, RegistryBefore: result.RegistryBefore, RegistryAfter: result.RegistryAfter, MirrorReady: result.Mirror.Head != "", JournalRepairOnly: result.JournalRepairOnly}
}

func stringPtr(value string) *string { return &value }
