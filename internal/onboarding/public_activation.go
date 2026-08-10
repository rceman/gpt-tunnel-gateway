package onboarding

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
)

func (o *PublicOrchestrator) advance(ctx context.Context, request Request, operationID string, receipt Receipt) (PublicResult, error) {
	if receipt.State == StateActivated {
		activation, err := o.Activation.Activate(ctx, request, operationID)
		public, journalErr := o.publicResultFromJournal(publicActivationResult(activation), request, operationID)
		if err != nil {
			return public, err
		}
		if journalErr != nil {
			return PublicResult{}, &CoordinatorError{
				Code:  ErrOnboardingRecoveryRequired.Error(),
				Cause: journalErr,
			}
		}
		return public, nil
	}
	if receipt.State == StatePrepared || receipt.State == StateHubCommitted {
		result, err := o.Coordinator.Execute(ctx, request, operationID)
		public, journalErr := o.publicResultFromJournal(publicCoordinatorResult(result), request, operationID)
		if err != nil {
			if journalErr == nil && public.State == StateActivated {
				public.JournalRepairOnly = true
				return public, nil
			}
			return public, err
		}
		if journalErr != nil {
			return PublicResult{}, &CoordinatorError{
				Code:  ErrOnboardingRecoveryRequired.Error(),
				Cause: journalErr,
			}
		}
		receipt, err = LoadOnboardingJournal(o.StateDir, operationID)
		if err != nil {
			return PublicResult{}, &CoordinatorError{
				Code:  ErrOnboardingRecoveryRequired.Error(),
				Cause: err,
			}
		}
	}
	if receipt.State == StateRecoveryRequired || receipt.State == StateActivated || receipt.State == StateHubCommitted {
		activation, err := o.Activation.Activate(ctx, request, operationID)
		public, journalErr := o.publicResultFromJournal(publicActivationResult(activation), request, operationID)
		if err != nil {
			return public, err
		}
		if journalErr != nil {
			return PublicResult{}, &CoordinatorError{
				Code:  ErrOnboardingRecoveryRequired.Error(),
				Cause: journalErr,
			}
		}
		return public, nil
	}
	return PublicResult{}, &CoordinatorError{
		Code:  ErrOnboardingRecoveryRequired.Error(),
		Cause: fmt.Errorf("unsupported onboarding state %q", receipt.State),
	}
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
	candidate := Receipt{
		SchemaVersion: 1,
		OperationID:   operationID,
		RequestSHA256: requestDigest,
		State:         StatePrepared,
		ProjectID:     request.ProjectID,
		RepositoryProof: RepositoryProof{
			Root:            request.Root,
			Remote:          request.Remote,
			RepositoryURL:   request.RepositoryURL,
			DefaultBranch:   request.DefaultBranch,
			Branch:          status.Branch,
			Head:            status.Head,
			GatewayStateDir: request.GatewayStateDir,
		},
		WorktreeProof: WorktreeProof{
			Clean:        status.Clean,
			StatusSHA256: statusHash,
		},
		SessionProof: session,
		RegistryDigests: RegistryDigests{
			ManagedBeforeSHA256: managedBefore,
			ManagedAfterSHA256:  managedAfter,
			ProjectSHA256:       digests.project,
			PlanSHA256:          digests.plan,
			IdentifiersSHA256:   digests.identifiers,
		},
		Hub: HubProof{
			Before: request.ExpectedHubRevision,
			Paths:  canonicalOnboardingPaths(request.ProjectID),
		},
		Timestamps: Timestamps{
			StartedAt:  started,
			PreparedAt: stringPtr(prepared),
			UpdatedAt:  prepared,
		},
		Recovery: Recovery{
			Status: RecoveryNotRequired,
		},
	}
	if _, err := WritePreparedJournal(o.StateDir, request, candidate); err != nil {
		return Receipt{}, err
	}
	return LoadOnboardingJournal(o.StateDir, operationID)
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
