package onboarding

import (
	"errors"
	"fmt"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
)

func (c *ActivationCoordinator) persistActivationRecovery(receipt Receipt, request Request, operationID string, registryReceipt config.ManagedProjectRegistryWriteReceipt, step RecoveryStep, reason string, mirrorProof *MirrorProof) (ActivationResult, error) {
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
	candidate.Recovery = Recovery{
		Status:               RecoveryRequired,
		LastCompletedState:   &lastState,
		LastDurableStep:      &lastStep,
		Reason:               &reason,
		SafeCorrectiveAction: &action,
	}
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
	result := ActivationResult{
		OperationID:    operationID,
		ProjectID:      request.ProjectID,
		State:          StateRecoveryRequired,
		ReceiptSHA256:  journal.ReceiptSHA256,
		RegistryBefore: registryReceipt.BeforeDigest,
		RegistryAfter:  registryReceipt.AfterDigest,
	}
	if candidate.MirrorProof != nil {
		result.Mirror = *candidate.MirrorProof
	}
	if writeErr != nil {
		return result, &CoordinatorError{
			Code:  ErrOnboardingRecoveryRequired.Error(),
			Cause: fmt.Errorf("%s; recovery_required journal persistence failed: %w", reason, writeErr),
		}
	}
	return result, &CoordinatorError{
		Code:  ErrOnboardingRecoveryRequired.Error(),
		Cause: errors.New(reason),
	}
}
