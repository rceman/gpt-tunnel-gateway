package onboarding

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"syscall"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
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

func onboardingRecoveryError(err error) error {
	if err == nil {
		return nil
	}
	var coordinatorErr *CoordinatorError
	if errors.As(err, &coordinatorErr) {
		return err
	}
	return &CoordinatorError{
		Code:  ErrOnboardingRecoveryRequired.Error(),
		Cause: err,
	}
}

func NewCoordinator(store hub.Store) *Coordinator {
	return &Coordinator{
		Hub:      store,
		StateDir: store.Config.StateDir,
	}
}

// Execute consumes a strict prepared request/journal pair. The operation lock
// spans journal inspection, collision checks and journal replacement; the Hub
// transaction supplies the optimistic repository lock and revision guard.
func (c *Coordinator) Execute(ctx context.Context, request Request, operationID string) (Result, error) {
	if err := authority.RequireOnboarding(ctx); err != nil {
		return Result{}, &CoordinatorError{
			Code:  ErrOnboardingAuthorityUnavailable.Error(),
			Cause: err,
		}
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
		return Result{}, &CoordinatorError{
			Code:  ErrOnboardingRecoveryRequired.Error(),
			Cause: err,
		}
	}
	if receipt.OperationID != operationID || receipt.RequestSHA256 != requestDigest || receipt.ProjectID != request.ProjectID {
		return Result{}, &CoordinatorError{
			Code:  ErrOnboardingOperationConflict.Error(),
			Cause: errors.New("journal identity does not match request"),
		}
	}
	managedProjectsLock, err := acquireManagedProjectsLock(ctx, c.StateDir)
	if err != nil {
		return Result{}, err
	}
	defer managedProjectsLock.Release()
	switch receipt.State {
	case StateHubCommitted:
		if err := ValidateHubCommittedReceipt(receipt, request); err != nil {
			return Result{}, &CoordinatorError{
				Code:  ErrOnboardingRecoveryRequired.Error(),
				Cause: err,
			}
		}
		if _, err := c.verifyRegistryAuthority(request, receipt, true); err != nil {
			return Result{}, err
		}
		project, plan, identifiers, objectDigests, err := buildDurableObjects(request)
		if err != nil {
			return Result{}, err
		}
		if err := c.ensureCommittedPlanSections(ctx, request, receipt, project, plan, identifiers, objectDigests); err != nil {
			return Result{}, &CoordinatorError{
				Code:  ErrOnboardingRecoveryRequired.Error(),
				Cause: err,
			}
		}
		digest, err := HubCommittedReceiptDigest(receipt, request)
		if err != nil {
			return Result{}, err
		}
		return Result{
			OperationID:   operationID,
			ProjectID:     request.ProjectID,
			State:         StateHubCommitted,
			RequestSHA256: requestDigest,
			ReceiptSHA256: digest,
			Hub:           receiptHubTransaction(receipt, c.Hub),
		}, nil
	case StatePrepared:
		if err := ValidatePreparedReceipt(receipt, request); err != nil {
			return Result{}, &CoordinatorError{
				Code:  ErrOnboardingRecoveryRequired.Error(),
				Cause: err,
			}
		}
	default:
		return Result{}, &CoordinatorError{
			Code:  ErrOnboardingRecoveryRequired.Error(),
			Cause: fmt.Errorf("unsupported onboarding journal state %q", receipt.State),
		}
	}

	project, plan, identifiers, objectDigests, err := buildDurableObjects(request)
	if err != nil {
		return Result{}, err
	}
	if receipt.RegistryDigests.ProjectSHA256 != objectDigests.project || receipt.RegistryDigests.PlanSHA256 != objectDigests.plan || receipt.RegistryDigests.IdentifiersSHA256 != objectDigests.identifiers {
		return Result{}, &CoordinatorError{
			Code:  ErrOnboardingRecoveryRequired.Error(),
			Cause: errors.New("prepared receipt registry digests do not match canonical durable objects"),
		}
	}
	if _, err := c.verifyRegistryAuthority(request, receipt, false); err != nil {
		return Result{}, err
	}
	if err := c.Hub.Ensure(ctx); err != nil {
		return Result{}, err
	}

	currentRevision, state, afterRevision, err := c.inspectTarget(ctx, request, project, plan, identifiers)
	if err != nil {
		return Result{}, onboardingRecoveryError(err)
	}
	if state == targetStateConflict {
		return Result{}, &CoordinatorError{
			Code:  ErrOnboardingRecoveryRequired.Error(),
			Cause: errors.New("target durable objects are partial or conflicting"),
		}
	}
	if state == targetStateExact {
		if currentRevision == request.ExpectedHubRevision {
			return Result{}, &CoordinatorError{
				Code:  ErrOnboardingRecoveryRequired.Error(),
				Cause: errors.New("exact target objects already exist at the expected pre-transaction revision"),
			}
		}
		committed := committedReceipt(receipt, request, afterRevision, project, plan, identifiers, false)
		journal, err := writeHubCommittedJournalLocked(c.StateDir, request, committed)
		if err != nil {
			return Result{}, &CoordinatorError{
				Code:  ErrOnboardingRecoveryRequired.Error(),
				Cause: err,
			}
		}
		return Result{
			OperationID:       operationID,
			ProjectID:         request.ProjectID,
			State:             StateHubCommitted,
			RequestSHA256:     requestDigest,
			ReceiptSHA256:     journal.ReceiptSHA256,
			Hub:               receiptHubTransaction(committed, c.Hub),
			JournalRepairOnly: true,
		}, nil
	}
	if currentRevision != request.ExpectedHubRevision {
		return Result{}, &CoordinatorError{
			Code:  ErrOnboardingRecoveryRequired.Error(),
			Cause: fmt.Errorf("HUB_REVISION_CONFLICT expected=%s actual=%s", request.ExpectedHubRevision, currentRevision),
		}
	}

	transaction, err := c.Hub.Transact(ctx, request.ExpectedHubRevision, "gateway: onboard project "+request.ProjectID, func(worktree string) ([]string, error) {
		if err := validateWorktreeTarget(worktree, request, project, plan, identifiers); err != nil {
			return nil, &CoordinatorError{
				Code:  ErrOnboardingRecoveryRequired.Error(),
				Cause: err,
			}
		}
		sections, err := buildPlanSections(request)
		if err != nil {
			return nil, err
		}
		return writeOnboardingObjects(worktree, onboardingObjects(request, project, plan, identifiers, sections))
	})
	if err != nil {
		var coordinatorErr *CoordinatorError
		if errors.As(err, &coordinatorErr) {
			return Result{}, err
		}
		return Result{}, &CoordinatorError{
			Code:  ErrOnboardingRecoveryRequired.Error(),
			Cause: err,
		}
	}
	lastChange, err := c.commonPathLastChange(ctx, request)
	if err != nil || lastChange != transaction.After {
		if err == nil {
			err = fmt.Errorf("committed onboarding paths last-change %s does not match Hub transaction %s", lastChange, transaction.After)
		}
		return Result{
				OperationID:    operationID,
				ProjectID:      request.ProjectID,
				State:          StateRecoveryRequired,
				RequestSHA256:  requestDigest,
				Hub:            transaction,
				HubTransaction: true,
			}, &CoordinatorError{
				Code:  ErrOnboardingRecoveryRequired.Error(),
				Cause: err,
			}
	}
	committed := committedReceipt(receipt, request, transaction.After, project, plan, identifiers, true)
	if err := beforeOnboardingJournalHook(ctx, c.Hub, transaction, request.ProjectID); err != nil {
		return Result{
				OperationID:    operationID,
				ProjectID:      request.ProjectID,
				State:          StateRecoveryRequired,
				RequestSHA256:  requestDigest,
				Hub:            transaction,
				HubTransaction: true,
			}, &CoordinatorError{
				Code:  ErrOnboardingRecoveryRequired.Error(),
				Cause: err,
			}
	}
	if err := c.validateCommittedHubState(ctx, request, committed, project, plan, identifiers, objectDigests); err != nil {
		return Result{
				OperationID:    operationID,
				ProjectID:      request.ProjectID,
				State:          StateRecoveryRequired,
				RequestSHA256:  requestDigest,
				Hub:            transaction,
				HubTransaction: true,
			}, &CoordinatorError{
				Code:  ErrOnboardingRecoveryRequired.Error(),
				Cause: err,
			}
	}
	journal, err := writeHubCommittedJournalLocked(c.StateDir, request, committed)
	if err != nil {
		return Result{
				OperationID:    operationID,
				ProjectID:      request.ProjectID,
				State:          StateRecoveryRequired,
				RequestSHA256:  requestDigest,
				Hub:            transaction,
				HubTransaction: true,
			}, &CoordinatorError{
				Code:  ErrOnboardingRecoveryRequired.Error(),
				Cause: fmt.Errorf("hub committed at %s but journal reconciliation failed: %w", transaction.After, err),
			}
	}
	return Result{
		OperationID:    operationID,
		ProjectID:      request.ProjectID,
		State:          StateHubCommitted,
		RequestSHA256:  requestDigest,
		ReceiptSHA256:  journal.ReceiptSHA256,
		Hub:            transaction,
		HubTransaction: true,
	}, nil
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

type worktreeRecord struct {
	Project     model.Project
	Identifiers *model.ProjectIdentifiers
}
