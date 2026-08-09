package onboarding

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/gitx"
)

func (c *ActivationCoordinator) Activate(ctx context.Context, request Request, operationID string) (ActivationResult, error) {
	if err := authority.RequireOnboarding(ctx); err != nil {
		return ActivationResult{}, &CoordinatorError{
			Code:  ErrOnboardingAuthorityUnavailable.Error(),
			Cause: err,
		}
	}
	if c == nil || c.StateDir == "" || c.StateDir != request.GatewayStateDir {
		return ActivationResult{}, &CoordinatorError{
			Code:  ErrOnboardingRecoveryRequired.Error(),
			Cause: errors.New("activation state directory is unavailable or does not match request"),
		}
	}
	if err := ValidateRequest(request); err != nil {
		return ActivationResult{}, &CoordinatorError{
			Code:  ErrOnboardingRecoveryRequired.Error(),
			Cause: err,
		}
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
		return ActivationResult{}, &CoordinatorError{
			Code:  ErrOnboardingRecoveryRequired.Error(),
			Cause: err,
		}
	}
	if receipt.OperationID != operationID || receipt.RequestSHA256 != requestDigest || receipt.ProjectID != request.ProjectID {
		return ActivationResult{}, &CoordinatorError{
			Code:  ErrOnboardingOperationConflict.Error(),
			Cause: errors.New("activation journal identity does not match request"),
		}
	}
	managedLock, err := acquireManagedProjectsLock(ctx, c.StateDir)
	if err != nil {
		return ActivationResult{}, &CoordinatorError{
			Code:  ErrOnboardingRecoveryRequired.Error(),
			Cause: err,
		}
	}
	defer managedLock.Release()

	base := Coordinator{
		Hub:      c.Hub,
		StateDir: c.StateDir,
	}
	project, plan, identifiers, objectDigests, err := buildDurableObjects(request)
	if err != nil {
		return ActivationResult{}, &CoordinatorError{
			Code:  ErrOnboardingRecoveryRequired.Error(),
			Cause: err,
		}
	}
	switch receipt.State {
	case StateHubCommitted:
		if err := ValidateHubCommittedReceipt(receipt, request); err != nil {
			return ActivationResult{}, &CoordinatorError{
				Code:  ErrOnboardingRecoveryRequired.Error(),
				Cause: err,
			}
		}
	case StateRecoveryRequired:
		if err := ValidateRecoveryReceipt(receipt, request); err != nil {
			return ActivationResult{}, &CoordinatorError{
				Code:  ErrOnboardingRecoveryRequired.Error(),
				Cause: err,
			}
		}
	case StateActivated:
		if err := ValidateActivatedReceipt(receipt, request); err != nil {
			return ActivationResult{}, &CoordinatorError{
				Code:  ErrOnboardingRecoveryRequired.Error(),
				Cause: err,
			}
		}
	default:
		return ActivationResult{}, &CoordinatorError{
			Code:  ErrOnboardingRecoveryRequired.Error(),
			Cause: fmt.Errorf("activation requires hub_committed or activated journal, got %q", receipt.State),
		}
	}
	if err := base.validateCommittedHubState(ctx, request, receipt, project, plan, identifiers, objectDigests); err != nil {
		return ActivationResult{}, &CoordinatorError{
			Code:  ErrOnboardingRecoveryRequired.Error(),
			Cause: err,
		}
	}
	var registryReceipt config.ManagedProjectRegistryWriteReceipt
	if _, err := base.verifyRegistryAuthority(request, receipt, true); err != nil {
		if receipt.State == StateActivated {
			return ActivationResult{
					OperationID: operationID,
					ProjectID:   request.ProjectID,
					State:       StateActivated,
					Mirror:      mirrorProofFromReceipt(receipt),
				}, &CoordinatorError{
					Code:  ErrOnboardingRecoveryRequired.Error(),
					Cause: err,
				}
		}
		return c.persistActivationRecovery(receipt, request, operationID, registryReceipt, RecoveryStepHubCommitted, "managed registry authority verification failed", nil)
	}
	if receipt.State == StateActivated {
		digest, err := ActivatedReceiptDigest(receipt, request)
		if err != nil {
			return ActivationResult{
					OperationID: operationID,
					ProjectID:   request.ProjectID,
					State:       StateActivated,
					Mirror:      mirrorProofFromReceipt(receipt),
				}, &CoordinatorError{
					Code:  ErrOnboardingRecoveryRequired.Error(),
					Cause: err,
				}
		}
		return ActivationResult{
			OperationID:       operationID,
			ProjectID:         request.ProjectID,
			State:             StateActivated,
			ReceiptSHA256:     digest,
			RegistryBefore:    receipt.RegistryDigests.ManagedBeforeSHA256,
			RegistryAfter:     receipt.RegistryDigests.ManagedAfterSHA256,
			Mirror:            mirrorProofFromReceipt(receipt),
			JournalRepairOnly: true,
		}, nil
	}
	current, err := config.LoadManagedProjects(c.StateDir)
	if err != nil {
		return c.persistActivationRecovery(receipt, request, operationID, registryReceipt, RecoveryStepHubCommitted, "managed registry load failed", nil)
	}
	currentDigest, err := current.Digest()
	if err != nil {
		return c.persistActivationRecovery(receipt, request, operationID, registryReceipt, RecoveryStepHubCommitted, "managed registry digest failed", nil)
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
			return c.persistActivationRecovery(receipt, request, operationID, registryReceipt, RecoveryStepHubCommitted, "managed registry activation failed", nil)
		}
	}
	registryStep := RecoveryStepManagedRegistry
	managed, err := config.LoadManagedProjects(c.StateDir)
	if err != nil {
		return c.persistActivationRecovery(receipt, request, operationID, registryReceipt, registryStep, "managed registry verification failed", nil)
	}
	effective, err := config.EffectiveProjectsFromValidatedStatic(c.Hub.Config.Projects, managed, c.StateDir)
	if err != nil {
		return c.persistActivationRecovery(receipt, request, operationID, registryReceipt, registryStep, "effective managed project validation failed", nil)
	}
	local, ok := effective[request.ProjectID]
	if !ok || local.Root != request.Root || local.Remote != request.Remote || local.DefaultBranch != request.DefaultBranch || local.AirelaySessionKey != sessionKey(request) {
		return c.persistActivationRecovery(receipt, request, operationID, registryReceipt, registryStep, "activated effective project does not match request", nil)
	}
	expectedMirrorPath := filepath.Clean(config.ManagedProjectMirrorPath(c.StateDir, request.ProjectID))
	if filepath.Clean(local.Mirror) != expectedMirrorPath {
		return c.persistActivationRecovery(receipt, request, operationID, registryReceipt, registryStep, "effective managed mirror path is not canonical", nil)
	}
	mirror := c.Hooks.Mirror
	if mirror == nil {
		mirror = func(ctx context.Context, p config.ProjectConfig, repositoryURL, branch string) (gitx.MirrorVerification, error) {
			return c.Git.ReconcileManagedMirror(ctx, p, repositoryURL, branch)
		}
	}
	mirrorResult, err := mirror(ctx, local, request.RepositoryURL, request.DefaultBranch)
	if err != nil {
		return c.persistActivationRecovery(receipt, request, operationID, registryReceipt, registryStep, "managed mirror activation failed", nil)
	}
	mirrorProof := &MirrorProof{
		Path:          mirrorResult.Path,
		RepositoryURL: mirrorResult.RepositoryURL,
		Head:          mirrorResult.Head,
	}
	if filepath.Clean(mirrorResult.Path) != expectedMirrorPath || mirrorResult.RepositoryURL != request.RepositoryURL || validateMirrorProof(mirrorProof) != nil {
		return c.persistActivationRecovery(receipt, request, operationID, registryReceipt, registryStep, "managed mirror proof is invalid or non-canonical", nil)
	}
	mirrorStep := RecoveryStepManagedMirror
	projectReady := c.Hooks.ProjectReady
	if projectReady == nil {
		projectReady = c.defaultProjectReadiness
	}
	if err := projectReady(ctx, request, local, project, plan, identifiers); err != nil {
		return c.persistActivationRecovery(receipt, request, operationID, registryReceipt, mirrorStep, "project readiness failed", mirrorProof)
	}
	projectStep := RecoveryStepProjectReady
	sessionReady := c.Hooks.SessionReady
	if sessionReady == nil {
		sessionReady = c.defaultSessionReadiness
	}
	sessionProof, err := sessionReady(ctx, request)
	if err != nil {
		return c.persistActivationRecovery(receipt, request, operationID, registryReceipt, projectStep, "Airelay readiness failed", mirrorProof)
	}
	if !sameSessionProof(receipt.SessionProof, sessionProof) {
		return c.persistActivationRecovery(receipt, request, operationID, registryReceipt, RecoveryStepSessionReady, "Airelay readiness proof changed from the hub-committed receipt", mirrorProof)
	}
	now := time.Now().UTC()
	if c.Hooks.Now != nil {
		now = c.Hooks.Now().UTC()
	}
	if receipt.State == StateActivated {
		digest, err := ActivatedReceiptDigest(receipt, request)
		if err != nil {
			return ActivationResult{
					OperationID:    operationID,
					ProjectID:      request.ProjectID,
					State:          StateRecoveryRequired,
					RegistryBefore: registryReceipt.BeforeDigest,
					RegistryAfter:  registryReceipt.AfterDigest,
					Mirror:         *receipt.MirrorProof,
				}, &CoordinatorError{
					Code:  ErrOnboardingRecoveryRequired.Error(),
					Cause: err,
				}
		}
		return ActivationResult{
			OperationID:       operationID,
			ProjectID:         request.ProjectID,
			State:             StateActivated,
			ReceiptSHA256:     digest,
			RegistryBefore:    registryReceipt.BeforeDigest,
			RegistryAfter:     registryReceipt.AfterDigest,
			Mirror:            *receipt.MirrorProof,
			JournalRepairOnly: true,
		}, nil
	}
	activatedAt := now.Format(time.RFC3339Nano)
	activated := receipt
	activated.State = StateActivated
	activated.MirrorProof = mirrorProof
	if receipt.MirrorProof != nil && !sameMirrorProof(receipt.MirrorProof, activated.MirrorProof) {
		return c.persistActivationRecovery(receipt, request, operationID, registryReceipt, RecoveryStepSessionReady, "managed mirror proof changed from the recovery receipt", activated.MirrorProof)
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
		return c.persistActivationRecovery(receipt, request, operationID, registryReceipt, RecoveryStepSessionReady, "activated journal persistence failed", activated.MirrorProof)
	}
	return ActivationResult{
		OperationID:       operationID,
		ProjectID:         request.ProjectID,
		State:             StateActivated,
		ReceiptSHA256:     journal.ReceiptSHA256,
		RegistryBefore:    registryReceipt.BeforeDigest,
		RegistryAfter:     registryReceipt.AfterDigest,
		Mirror:            *activated.MirrorProof,
		JournalRepairOnly: receipt.State == StateActivated,
	}, nil
}
