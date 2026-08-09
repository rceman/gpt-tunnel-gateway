package model

import (
	"fmt"
	"strings"
)

func ValidateTask(v Task) error {
	if v.SchemaVersion != SchemaVersion || !idRE.MatchString(v.ProjectID) || v.ID == "" {
		return fmt.Errorf("invalid task identity")
	}
	if len(v.Title) < 3 || len(v.Title) > 300 || len(v.Objective) < 3 || len(v.Objective) > 200000 {
		return fmt.Errorf("invalid task content")
	}
	if err := ValidateBranch(v.Branch); err != nil {
		return err
	}
	if v.BaseRevision != "" && !shaRE.MatchString(v.BaseRevision) {
		return fmt.Errorf("base_revision must be a lowercase 40-character SHA")
	}
	if len(v.AcceptanceCriteria) > 200 || len(v.Constraints) > 200 || len(v.RequiredGates) > 100 {
		return fmt.Errorf("too many task entries")
	}
	if v.OperationClass != "" {
		effective := EffectiveWorkflowPolicy{
			WorkflowPolicyRevision: v.WorkflowPolicyRevision,
			OperationClass:         v.OperationClass,
			EffectiveCIField:       v.EffectiveCIField,
			EffectiveCIMode:        v.EffectiveCIMode,
			WaitForCI:              v.WaitForCI,
			CIBlocking:             v.CIBlocking,
			AgentMayWait:           v.AgentMayWait,
		}
		if err := ValidateEffectiveWorkflowPolicy(effective); err != nil {
			return err
		}
	} else if !legacyWorkflowPolicyProjection(v) {
		return fmt.Errorf("mixed legacy and workflow-policy task projection")
	}
	for _, s := range append(append([]string{}, v.AcceptanceCriteria...), v.Constraints...) {
		if len(s) > 20000 {
			return fmt.Errorf("task entry too large")
		}
	}
	if v.SHA256 != "" {
		if err := ValidateTaskHash(v); err != nil {
			return err
		}
	}
	return nil
}

func ValidateTaskState(v TaskState, task Task) error {
	if v.SchemaVersion != SchemaVersion || v.TaskID != task.ID || v.TaskSHA256 != task.SHA256 || v.UpdatedAt.IsZero() {
		return fmt.Errorf("invalid task state identity")
	}
	switch v.Status {
	case "created", "ready", "dispatched", "cancelled", "superseded", "completed":
		if v.ReviewedHead != "" || v.DeferredReason != "" || v.IntegrationBranch != "" || v.IntegrationHead != "" {
			return fmt.Errorf("task lifecycle fields are not valid for status %q", v.Status)
		}
	case "merge_ready", "deferred", "merged":
		if err := ValidateCommitSHA(v.ReviewedHead); err != nil {
			return fmt.Errorf("reviewed_head: %w", err)
		}
		if v.Status == "deferred" {
			if err := validateDeferredReason(v.DeferredReason); err != nil {
				return err
			}
		} else if v.DeferredReason != "" {
			return fmt.Errorf("deferred_reason is only valid for deferred tasks")
		}
		if v.Status == "merged" {
			if v.IntegrationBranch != "main" && v.IntegrationBranch != "develop" {
				return fmt.Errorf("integration_branch must be main or develop for merged tasks")
			}
			if err := ValidateCommitSHA(v.IntegrationHead); err != nil {
				return fmt.Errorf("integration_head: %w", err)
			}
		} else if v.IntegrationBranch != "" || v.IntegrationHead != "" {
			return fmt.Errorf("integration receipt is only valid for merged tasks")
		}
	default:
		return fmt.Errorf("invalid task state status")
	}
	return nil
}

func ValidateCommitSHA(s string) error {
	if !shaRE.MatchString(s) {
		return fmt.Errorf("must be a lowercase 40-character SHA")
	}
	return nil
}

func validateDeferredReason(reason string) error {
	if strings.TrimSpace(reason) == "" {
		return fmt.Errorf("deferred_reason must be non-empty")
	}
	if strings.ContainsRune(reason, '\x00') {
		return fmt.Errorf("deferred_reason must not contain NUL")
	}
	if len([]byte(reason)) > MaxDeferredReasonBytes {
		return fmt.Errorf("deferred_reason exceeds %d bytes", MaxDeferredReasonBytes)
	}
	return nil
}

func ValidateRun(v Run) error {
	if v.SchemaVersion != SchemaVersion || v.ID == "" || v.TaskID == "" || !idRE.MatchString(v.ProjectID) {
		return fmt.Errorf("invalid run identity")
	}
	switch v.Status {
	case "created", "dispatching", "dispatched", "awaiting_result", "cancel_requested", "succeeded", "failed", "needs_gpt_revision":
	default:
		return fmt.Errorf("invalid run status")
	}
	if len(v.DispatchMessage) > 512 {
		return fmt.Errorf("dispatch message too large")
	}
	if v.CompletionPath == "" {
		return fmt.Errorf("completion_path is required")
	}
	if !sha256RE(v.TaskSHA256) {
		return fmt.Errorf("invalid task hash")
	}
	if v.TaskRevision != 0 || v.TaskRevisionSHA256 != "" || v.TaskRunNumber != 0 {
		if v.TaskRevision < 1 || !sha256RE(v.TaskRevisionSHA256) || v.TaskRunNumber == 0 || v.TaskRunNumber > MaxSafeInteger {
			return fmt.Errorf("invalid revision-aware run binding")
		}
		revisionID, err := FormatTaskRevisionID(v.TaskID, v.TaskRevision)
		if err != nil {
			return err
		}
		want, err := FormatTaskRevisionRunID(revisionID, v.TaskRunNumber)
		if err != nil || v.ID != want {
			return fmt.Errorf("run id does not match revision-aware binding")
		}
	}
	return nil
}
