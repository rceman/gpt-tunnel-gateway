package onboarding

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

func validateActivationTransition(prior, candidate Receipt, request Request) error {
	if prior.State != StateHubCommitted && prior.State != StateRecoveryRequired {
		return fmt.Errorf("activation requires an existing hub-committed or recovery-required journal, got %q", prior.State)
	}
	if err := ValidateActivatedReceipt(candidate, request); err != nil {
		return err
	}
	if !sameHubCommittedEvidence(prior, candidate) {
		return errors.New("activated receipt must preserve exact hub-committed identity and proofs")
	}
	if prior.MirrorProof != nil && !sameMirrorProof(prior.MirrorProof, candidate.MirrorProof) {
		return errors.New("activated receipt must preserve existing mirror evidence")
	}
	return nil
}

func validateRecoveryTransition(prior, candidate Receipt, request Request) error {
	if prior.State != StateHubCommitted && prior.State != StateRecoveryRequired {
		return fmt.Errorf("recovery requires an existing hub-committed or recovery-required journal, got %q", prior.State)
	}
	if err := ValidateRecoveryReceipt(candidate, request); err != nil {
		return err
	}
	if !sameHubCommittedEvidence(prior, candidate) {
		return errors.New("recovery receipt must preserve exact hub-committed identity and proofs")
	}
	if prior.State == StateRecoveryRequired && recoveryStepRank(*candidate.Recovery.LastDurableStep) < recoveryStepRank(*prior.Recovery.LastDurableStep) {
		return errors.New("recovery receipt cannot move last_durable_step backward")
	}
	if prior.State == StateRecoveryRequired {
		priorUpdated, err := parseReceiptTime(prior.Timestamps.UpdatedAt)
		if err != nil {
			return err
		}
		candidateUpdated, err := parseReceiptTime(candidate.Timestamps.UpdatedAt)
		if err != nil {
			return err
		}
		if candidateUpdated.Before(priorUpdated) {
			return errors.New("recovery receipt timestamps.updated_at cannot move backward")
		}
	}
	priorStep := *candidate.Recovery.LastDurableStep
	if recoveryStepRank(priorStep) >= recoveryStepRank(RecoveryStepManagedMirror) && candidate.MirrorProof == nil {
		return errors.New("recovery receipt requires mirror_proof for its completed step")
	}
	if recoveryStepRank(priorStep) < recoveryStepRank(RecoveryStepManagedMirror) && candidate.MirrorProof != nil {
		return errors.New("recovery receipt must not contain mirror_proof before managed mirror readiness")
	}
	if prior.MirrorProof != nil && !sameMirrorProof(prior.MirrorProof, candidate.MirrorProof) {
		return errors.New("recovery receipt must preserve existing mirror evidence")
	}
	return nil
}

func sameHubCommittedEvidence(left, right Receipt) bool {
	left = hubCommittedEvidence(left)
	right = hubCommittedEvidence(right)
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}

func hubCommittedEvidence(receipt Receipt) Receipt {
	receipt.State = StateHubCommitted
	receipt.MirrorProof = nil
	receipt.Timestamps.UpdatedAt = ""
	receipt.Timestamps.ActivatedAt = nil
	receipt.Timestamps.RolledBackAt = nil
	receipt.Recovery = Recovery{Status: RecoveryNotRequired}
	return receipt
}

func sameMirrorProof(left, right *MirrorProof) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func validateReceiptRequestBinding(receipt Receipt, request Request) error {
	expectedRequestDigest, err := RequestDigest(request)
	if err != nil {
		return fmt.Errorf("compute request digest: %w", err)
	}
	if receipt.RequestSHA256 != expectedRequestDigest || receipt.ProjectID != request.ProjectID {
		return errors.New("receipt does not match request identity")
	}
	proof := receipt.RepositoryProof
	if proof.Root != request.Root || proof.Remote != request.Remote || proof.RepositoryURL != request.RepositoryURL || proof.DefaultBranch != request.DefaultBranch || proof.Branch != request.DefaultBranch || proof.GatewayStateDir != request.GatewayStateDir {
		return errors.New("receipt repository proof does not match request")
	}
	if err := validateSessionProof(receipt.SessionProof, request.Airelay); err != nil {
		return err
	}
	if receipt.Hub.Before != request.ExpectedHubRevision {
		return errors.New("receipt hub.before does not match request expected hub revision")
	}
	return nil
}
