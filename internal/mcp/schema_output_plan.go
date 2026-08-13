package mcp

import (
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func workflowPolicyStatusOutputSchema() map[string]any {
	ci := closedOutput(map[string]any{"task": outputEnum(model.WorkflowCIModeDisabled, model.WorkflowCIModeObserve, model.WorkflowCIModeRequire), "task_merge": outputEnum(model.WorkflowCIModeDisabled, model.WorkflowCIModeObserve, model.WorkflowCIModeRequire), "release": outputEnum(model.WorkflowCIModeDisabled, model.WorkflowCIModeObserve, model.WorkflowCIModeRequire)}, "task", "task_merge", "release")
	return closedOutput(map[string]any{"state": outputEnum("adopted", "missing", "invalid"), "revision": outputInteger(), "workflow_stage": outputString(), "integration_branch": outputString(), "agent_wait_for_ci": outputBoolean(), "ci": ci, "gates": outputArray(outputEnum(model.WorkflowGateFormat, model.WorkflowGateCheck, model.WorkflowGateTest)), "active_operation_class": outputString(), "active_ci_mode": outputString(), "ci_blocking": outputBoolean(), "conflicts": outputArray(outputString()), "corrective_action": outputString()}, "state", "revision", "workflow_stage", "integration_branch", "agent_wait_for_ci", "ci", "gates", "active_operation_class", "active_ci_mode", "ci_blocking", "conflicts", "corrective_action")
}

func projectConfigurationStatusOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"state":         outputEnum("valid", "missing", "invalid"),
		"revision":      outputInteger(),
		"configuration": map[string]any{"type": "object", "additionalProperties": true},
		"conflicts":     outputArray(outputString()),
	}, "state", "revision", "conflicts")
}

func projectProgressOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"agent_state":          outputEnum("idle", "running", "waiting_for_input", "compacting", "compacted_resuming", "compacted_idle", "capacity_blocked", "rate_limited", "completion_pending", "finalization_pending", "stalled", "error", "unknown"),
		"controller_reachable": outputBoolean(), "airelay_version": outputString(), "protocol_version": outputString(), "capacity_warnings": outputArray(outputString()), "exit_code": outputInteger(), "error": outputString(),
		"last_meaningful_activity": outputDateTime(), "last_meaningful_activity_age_seconds": outputInteger(), "tail": outputString(), "blocker_classification": outputString(), "recommended_next_action": outputString(), "component_errors": outputArray(outputString()),
	}, "agent_state", "controller_reachable", "capacity_warnings", "exit_code", "last_meaningful_activity_age_seconds", "tail", "blocker_classification", "recommended_next_action", "component_errors")
}

func worktreeStatusOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"branch": outputString(), "head": outputString(), "upstream": outputString(), "ahead": outputInteger(), "behind": outputInteger(),
		"porcelain": outputString(), "clean": outputBoolean(),
	}, "branch", "head", "ahead", "behind", "porcelain", "clean")
}

func taskRecordOutputSchema() map[string]any {
	return closedOutput(map[string]any{"task": taskOutputSchema(), "state": taskStateOutputSchema(), "current_revision": taskRevisionOutputSchema(), "workflow_policy": workflowPolicyOutputSchema()}, "task", "state")
}

func taskPacketOutputSchema() map[string]any {
	currentRevision := map[string]any{"anyOf": []any{taskRevisionOutputSchema(), map[string]any{"type": "null"}}}
	return closedOutput(map[string]any{
		"task": taskOutputSchema(), "current_revision": currentRevision, "project": projectOutputSchema(), "plan": planOutputSchema(), "workflow_policy": workflowPolicyOutputSchema(),
		"repository_root": outputString(),
		"text":            outputString(),
	}, "task", "project", "plan", "workflow_policy", "repository_root", "text")
}

func taskReadOutputSchema() map[string]any {
	inactive := closedOutput(map[string]any{
		"task": taskOutputSchema(), "state": taskStateOutputSchema(), "current_revision": taskRevisionOutputSchema(), "workflow_policy": workflowPolicyOutputSchema(),
	}, "task", "state")
	return map[string]any{"type": "object", "oneOf": []any{taskPacketOutputSchema(), inactive}}
}
