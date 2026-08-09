package onboarding

import (
	"context"
	"errors"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
)

// PublicInput is the one strict input shared by the three O4 transport
// surfaces. Authority is deliberately not serializable in this type.
type PublicInput struct {
	OperationID string  `json:"operation_id"`
	Request     Request `json:"request"`
}

type PublicResult struct {
	OperationID   string       `json:"operation_id"`
	ProjectID     string       `json:"project_id"`
	State         ReceiptState `json:"state"`
	RequestSHA256 string       `json:"request_sha256"`
	ReceiptSHA256 string       `json:"receipt_sha256"`

	HubTransaction    bool   `json:"hub_transaction"`
	JournalRepairOnly bool   `json:"journal_repair_only"`
	RegistryBefore    string `json:"registry_before"`
	RegistryAfter     string `json:"registry_after"`
	MirrorReady       bool   `json:"mirror_ready"`
	RecoveryStatus    string `json:"recovery_status"`
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
		Hub:         store,
		StateDir:    store.Config.StateDir,
		Coordinator: NewCoordinator(store),
		Activation:  NewActivationCoordinator(store),
	}
}

func (o *PublicOrchestrator) Onboard(ctx context.Context, in PublicInput) (PublicResult, error) {
	if err := authority.RequireOnboarding(ctx); err != nil {
		return PublicResult{}, &CoordinatorError{
			Code:  ErrOnboardingAuthorityUnavailable.Error(),
			Cause: err,
		}
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
		if err != nil {
			// Another identical caller may have persisted and advanced the
			// journal after the initial not-found snapshot. Re-read the
			// durable receipt; normal request binding and state validation in
			// advance still reject conflicts.
			if persisted, loadErr := LoadOnboardingJournal(o.StateDir, in.OperationID); loadErr == nil {
				receipt, err = persisted, nil
			}
		}
	}
	if err != nil {
		return PublicResult{}, &CoordinatorError{
			Code:  ErrOnboardingRecoveryRequired.Error(),
			Cause: err,
		}
	}
	return o.advance(ctx, in.Request, in.OperationID, receipt)
}

func (o *PublicOrchestrator) Recover(ctx context.Context, in PublicInput) (PublicResult, error) {
	if err := authority.RequireOnboarding(ctx); err != nil {
		return PublicResult{}, &CoordinatorError{
			Code:  ErrOnboardingAuthorityUnavailable.Error(),
			Cause: err,
		}
	}
	if err := ValidateRequest(in.Request); err != nil {
		return PublicResult{}, fmt.Errorf("invalid onboarding request: %w", err)
	}
	if err := validatePublicOperation(in.OperationID); err != nil {
		return PublicResult{}, err
	}
	receipt, err := LoadOnboardingJournal(o.StateDir, in.OperationID)
	if err != nil {
		return PublicResult{}, &CoordinatorError{
			Code:  ErrOnboardingRecoveryRequired.Error(),
			Cause: err,
		}
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
		return StatusProjection{}, &CoordinatorError{
			Code:  ErrOnboardingRecoveryRequired.Error(),
			Cause: err,
		}
	}
	if err := validateReceiptForState(receipt, in.Request); err != nil {
		return StatusProjection{}, &CoordinatorError{
			Code:  ErrOnboardingRecoveryRequired.Error(),
			Cause: err,
		}
	}
	return publicStatusProjection(receipt, in.Request)
}
