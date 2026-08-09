package onboarding

import (
	"fmt"
)

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
	return StatusProjection{
		OperationID:    receipt.OperationID,
		ProjectID:      receipt.ProjectID,
		State:          receipt.State,
		RequestSHA256:  receipt.RequestSHA256,
		ReceiptSHA256:  digest,
		StartedAt:      receipt.Timestamps.StartedAt,
		UpdatedAt:      receipt.Timestamps.UpdatedAt,
		RecoveryStatus: string(receipt.Recovery.Status),
		RecoveryStep:   step,
		HubBefore:      receipt.Hub.Before,
		HubAfter:       after,
		HubCommitted:   hubReady,
		RegistryBefore: receipt.RegistryDigests.ManagedBeforeSHA256,
		RegistryAfter:  receipt.RegistryDigests.ManagedAfterSHA256,
		RegistryReady:  registryReady,
		MirrorReady:    mirrorReady,
		ProjectReady:   projectReady,
		SessionReady:   sessionReady,
	}, nil
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
	return PublicResult{
		OperationID:       result.OperationID,
		ProjectID:         result.ProjectID,
		State:             result.State,
		RequestSHA256:     result.RequestSHA256,
		ReceiptSHA256:     result.ReceiptSHA256,
		HubTransaction:    result.HubTransaction,
		JournalRepairOnly: result.JournalRepairOnly,
		RecoveryStatus:    string(RecoveryNotRequired),
	}
}

func publicActivationResult(result ActivationResult) PublicResult {
	return PublicResult{
		OperationID:       result.OperationID,
		ProjectID:         result.ProjectID,
		State:             result.State,
		ReceiptSHA256:     result.ReceiptSHA256,
		RegistryBefore:    result.RegistryBefore,
		RegistryAfter:     result.RegistryAfter,
		MirrorReady:       result.Mirror.Head != "",
		RecoveryStatus:    string(RecoveryNotRequired),
		JournalRepairOnly: result.JournalRepairOnly,
	}
}

func (o *PublicOrchestrator) publicResultFromJournal(result PublicResult, request Request, operationID string) (PublicResult, error) {
	digest, err := RequestDigest(request)
	if err != nil {
		return result, err
	}
	result.RequestSHA256 = digest
	receipt, err := LoadOnboardingJournal(o.StateDir, operationID)
	if err != nil {
		return result, err
	}
	if err := validateReceiptForState(receipt, request); err != nil {
		return result, err
	}
	receiptDigest, err := receiptDigestForState(receipt, request)
	if err != nil {
		return result, err
	}
	result.OperationID = receipt.OperationID
	result.ProjectID = receipt.ProjectID
	result.State = receipt.State
	result.ReceiptSHA256 = receiptDigest
	result.RecoveryStatus = string(receipt.Recovery.Status)
	result.RegistryBefore = receipt.RegistryDigests.ManagedBeforeSHA256
	result.RegistryAfter = receipt.RegistryDigests.ManagedAfterSHA256
	result.MirrorReady = receipt.MirrorProof != nil
	return result, nil
}

func stringPtr(value string) *string { return &value }
