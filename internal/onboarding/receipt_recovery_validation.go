package onboarding

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
)

func validateRecoveryRequired(recovery Recovery) error {
	if recovery.Status != RecoveryRequired {
		return errors.New("recovery-required receipt must have recovery.status recovery_required")
	}
	if recovery.LastCompletedState == nil || *recovery.LastCompletedState != StateHubCommitted {
		return errors.New("recovery-required receipt must identify hub_committed as last completed state")
	}
	if recovery.LastDurableStep == nil {
		return errors.New("recovery-required receipt must identify last_durable_step")
	}
	if recoveryStepRank(*recovery.LastDurableStep) == 0 {
		return fmt.Errorf("recovery-required receipt has unsupported last_durable_step %q", *recovery.LastDurableStep)
	}
	if recovery.Reason == nil || strings.TrimSpace(*recovery.Reason) == "" || utf8.RuneCountInString(*recovery.Reason) > 500 || strings.ContainsAny(*recovery.Reason, "\x00\r\n") {
		return errors.New("recovery-required receipt must contain a bounded reason")
	}
	if recovery.SafeCorrectiveAction == nil || *recovery.SafeCorrectiveAction != RecoveryActionResumeActivation {
		return errors.New("recovery-required receipt must require resume_activation")
	}
	if recovery.RolledBackAt != nil || recovery.RollbackProof != nil {
		return errors.New("recovery-required receipt must not claim rollback")
	}
	return nil
}

func validateRecoveryEvidence(receipt Receipt) error {
	if receipt.State != StateRecoveryRequired || receipt.Recovery.LastDurableStep == nil {
		return errors.New("recovery-required receipt has no durable step")
	}
	stepRank := recoveryStepRank(*receipt.Recovery.LastDurableStep)
	if stepRank >= recoveryStepRank(RecoveryStepManagedMirror) && receipt.MirrorProof == nil {
		return errors.New("recovery-required receipt requires mirror_proof for its completed step")
	}
	if stepRank < recoveryStepRank(RecoveryStepManagedMirror) && receipt.MirrorProof != nil {
		return errors.New("recovery-required receipt must not contain mirror_proof before managed mirror readiness")
	}
	return nil
}

func recoveryStepRank(step RecoveryStep) int {
	switch step {
	case RecoveryStepHubCommitted:
		return 1
	case RecoveryStepManagedRegistry:
		return 2
	case RecoveryStepManagedMirror:
		return 3
	case RecoveryStepProjectReady:
		return 4
	case RecoveryStepSessionReady:
		return 5
	default:
		return 0
	}
}

func validateActivatedTimestamps(timestamps Timestamps) error {
	if timestamps.PreparedAt == nil || timestamps.HubCommittedAt == nil || timestamps.ActivatedAt == nil || timestamps.RolledBackAt != nil {
		return errors.New("activated receipt timestamps are invalid")
	}
	started, err := parseReceiptTime(timestamps.StartedAt)
	if err != nil {
		return fmt.Errorf("receipt timestamps.started_at: %w", err)
	}
	prepared, err := parseReceiptTime(*timestamps.PreparedAt)
	if err != nil {
		return fmt.Errorf("receipt timestamps.prepared_at: %w", err)
	}
	committed, err := parseReceiptTime(*timestamps.HubCommittedAt)
	if err != nil {
		return fmt.Errorf("receipt timestamps.hub_committed_at: %w", err)
	}
	activated, err := parseReceiptTime(*timestamps.ActivatedAt)
	if err != nil {
		return fmt.Errorf("receipt timestamps.activated_at: %w", err)
	}
	updated, err := parseReceiptTime(timestamps.UpdatedAt)
	if err != nil {
		return fmt.Errorf("receipt timestamps.updated_at: %w", err)
	}
	if !started.Before(prepared) || prepared.After(committed) || committed.After(activated) || activated.After(updated) {
		return errors.New("activated receipt timestamp order is invalid")
	}
	return nil
}

func validateRecoveryTimestamps(timestamps Timestamps) error {
	if timestamps.PreparedAt == nil || timestamps.HubCommittedAt == nil || timestamps.ActivatedAt != nil || timestamps.RolledBackAt != nil {
		return errors.New("recovery-required receipt timestamps are invalid")
	}
	started, err := parseReceiptTime(timestamps.StartedAt)
	if err != nil {
		return fmt.Errorf("receipt timestamps.started_at: %w", err)
	}
	prepared, err := parseReceiptTime(*timestamps.PreparedAt)
	if err != nil {
		return fmt.Errorf("receipt timestamps.prepared_at: %w", err)
	}
	committed, err := parseReceiptTime(*timestamps.HubCommittedAt)
	if err != nil {
		return fmt.Errorf("receipt timestamps.hub_committed_at: %w", err)
	}
	updated, err := parseReceiptTime(timestamps.UpdatedAt)
	if err != nil {
		return fmt.Errorf("receipt timestamps.updated_at: %w", err)
	}
	if !started.Before(prepared) || prepared.After(committed) || committed.After(updated) {
		return errors.New("recovery-required receipt timestamp order is invalid")
	}
	return nil
}

func validateActivatedRecovery(recovery Recovery) error {
	return validateCommittedRecovery(recovery)
}

func ValidateHubCommittedReceipt(receipt Receipt, request Request) error {
	if err := ValidateRequest(request); err != nil {
		return fmt.Errorf("invalid onboarding request: %w", err)
	}
	if err := ValidateHubCommittedReceiptIntrinsic(receipt); err != nil {
		return err
	}
	if err := validateReceiptRequestBinding(receipt, request); err != nil {
		return err
	}
	if receipt.CreatedProject.WorkflowRepository != nil {
		if request.Workflow == nil || *receipt.CreatedProject.WorkflowRepository != request.Workflow.Repository || receipt.CreatedProject.WorkflowCommit == nil || *receipt.CreatedProject.WorkflowCommit != request.Workflow.Commit {
			return errors.New("created_project workflow does not match request")
		}
	} else if request.Workflow != nil {
		return errors.New("created_project workflow is missing")
	}
	if receipt.CreatedPlan.Revision != request.InitialPlan.Revision || receipt.CreatedPlan.ProjectID != request.InitialPlan.ProjectID {
		return errors.New("created_plan does not match request initial plan")
	}
	if receipt.CreatedIdentifiers.ProjectCode != request.ProjectCode {
		return errors.New("created_identifiers project code does not match request")
	}
	return nil
}

func ValidateActivatedReceipt(receipt Receipt, request Request) error {
	if err := ValidateRequest(request); err != nil {
		return fmt.Errorf("invalid onboarding request: %w", err)
	}
	if err := ValidateActivatedReceiptIntrinsic(receipt); err != nil {
		return err
	}
	if err := validateReceiptRequestBinding(receipt, request); err != nil {
		return err
	}
	if receipt.CreatedProject.WorkflowRepository != nil {
		if request.Workflow == nil || *receipt.CreatedProject.WorkflowRepository != request.Workflow.Repository || receipt.CreatedProject.WorkflowCommit == nil || *receipt.CreatedProject.WorkflowCommit != request.Workflow.Commit {
			return errors.New("created_project workflow does not match request")
		}
	} else if request.Workflow != nil {
		return errors.New("created_project workflow is missing")
	}
	if receipt.CreatedPlan.Revision != request.InitialPlan.Revision || receipt.CreatedPlan.ProjectID != request.InitialPlan.ProjectID {
		return errors.New("created_plan does not match request initial plan")
	}
	if receipt.CreatedIdentifiers.ProjectCode != request.ProjectCode {
		return errors.New("created_identifiers project code does not match request")
	}
	if receipt.MirrorProof.RepositoryURL != request.RepositoryURL {
		return errors.New("mirror_proof repository URL does not match request")
	}
	if filepath.Clean(receipt.MirrorProof.Path) != filepath.Clean(config.ManagedProjectMirrorPath(request.GatewayStateDir, request.ProjectID)) {
		return errors.New("mirror_proof path does not match canonical managed mirror")
	}
	return nil
}

func ValidateRecoveryReceipt(receipt Receipt, request Request) error {
	if err := ValidateRequest(request); err != nil {
		return fmt.Errorf("invalid onboarding request: %w", err)
	}
	if err := validatePostHubReceiptIntrinsic(receipt, StateRecoveryRequired); err != nil {
		return err
	}
	if err := validateRecoveryEvidence(receipt); err != nil {
		return err
	}
	if err := validateReceiptRequestBinding(receipt, request); err != nil {
		return err
	}
	if receipt.CreatedProject.WorkflowRepository != nil {
		if request.Workflow == nil || *receipt.CreatedProject.WorkflowRepository != request.Workflow.Repository || receipt.CreatedProject.WorkflowCommit == nil || *receipt.CreatedProject.WorkflowCommit != request.Workflow.Commit {
			return errors.New("created_project workflow does not match request")
		}
	} else if request.Workflow != nil {
		return errors.New("created_project workflow is missing")
	}
	if receipt.CreatedPlan.Revision != request.InitialPlan.Revision || receipt.CreatedPlan.ProjectID != request.InitialPlan.ProjectID {
		return errors.New("created_plan does not match request initial plan")
	}
	if receipt.CreatedIdentifiers.ProjectCode != request.ProjectCode {
		return errors.New("created_identifiers project code does not match request")
	}
	if receipt.MirrorProof != nil {
		if receipt.MirrorProof.RepositoryURL != request.RepositoryURL {
			return errors.New("mirror_proof repository URL does not match request")
		}
		if filepath.Clean(receipt.MirrorProof.Path) != filepath.Clean(config.ManagedProjectMirrorPath(request.GatewayStateDir, request.ProjectID)) {
			return errors.New("mirror_proof path does not match canonical managed mirror")
		}
	}
	return nil
}
