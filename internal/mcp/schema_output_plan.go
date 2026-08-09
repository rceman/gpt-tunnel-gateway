package mcp

import (
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func workflowPolicyStatusOutputSchema() map[string]any {
	ci := closedOutput(map[string]any{"task": outputEnum(model.WorkflowCIModeDisabled, model.WorkflowCIModeObserve, model.WorkflowCIModeRequire), "task_merge": outputEnum(model.WorkflowCIModeDisabled, model.WorkflowCIModeObserve, model.WorkflowCIModeRequire), "release": outputEnum(model.WorkflowCIModeDisabled, model.WorkflowCIModeObserve, model.WorkflowCIModeRequire)}, "task", "task_merge", "release")
	return closedOutput(map[string]any{"state": outputEnum("adopted", "missing", "invalid"), "revision": outputInteger(), "workflow_stage": outputString(), "integration_branch": outputString(), "agent_wait_for_ci": outputBoolean(), "ci": ci, "active_operation_class": outputString(), "active_ci_mode": outputString(), "ci_blocking": outputBoolean(), "conflicts": outputArray(outputString()), "corrective_action": outputString()}, "state", "revision", "workflow_stage", "integration_branch", "agent_wait_for_ci", "ci", "active_operation_class", "active_ci_mode", "ci_blocking", "conflicts", "corrective_action")
}

func projectProgressOutputSchema() map[string]any {
	task := closedOutput(map[string]any{"id": outputString(), "title": outputString(), "status": outputString(), "created_at": outputDateTime()}, "id", "title", "status", "created_at")
	run := closedOutput(map[string]any{"id": outputString(), "task_id": outputString(), "status": outputString(), "branch": outputString(), "base_revision": outputString(), "created_at": outputDateTime(), "dispatched_at": outputDateTime(), "finished_at": outputDateTime()}, "id", "task_id", "status", "branch", "base_revision", "created_at")
	return closedOutput(map[string]any{
		"latest_task": task, "latest_run": run,
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
	summary := runReviewSummaryOutputSchema()
	return closedOutput(map[string]any{"task": taskOutputSchema(), "state": taskStateOutputSchema(), "current_revision": taskRevisionOutputSchema(), "run_summaries": outputArray(summary), "workflow_policy": workflowPolicyOutputSchema()}, "task", "state")
}

func runReviewSummaryOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"run_id": outputString(), "agent_status": outputString(), "delivery_status": outputString(),
		"delivery_report_id": outputString(), "delivery_outcome": outputEnum(model.ReviewOutcomeAccepted, model.ReviewOutcomeRejected, model.ReviewOutcomeBlocked, model.ReviewOutcomeInconclusive),
		"reviewed_head": outputString(), "blocker": outputString(), "next_action": outputString(), "history_only": outputBoolean(),
	}, "run_id", "agent_status", "delivery_status", "history_only")
}

func taskPacketOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"task": taskOutputSchema(), "current_revision": taskRevisionOutputSchema(), "run": taskPacketRunOutputSchema(), "project": projectOutputSchema(), "plan": planOutputSchema(), "workflow_policy": workflowPolicyOutputSchema(),
		"run_summaries":    outputArray(runReviewSummaryOutputSchema()),
		"repository_root":  outputString(),
		"finalize_command": outputString(), "text": outputString(),
	}, "task", "run", "project", "plan", "workflow_policy", "repository_root", "finalize_command", "text")
}

func taskReadOutputSchema() map[string]any {
	inactive := closedOutput(map[string]any{
		"task": taskOutputSchema(), "state": taskStateOutputSchema(), "current_revision": taskRevisionOutputSchema(), "run_summaries": outputArray(runReviewSummaryOutputSchema()), "workflow_policy": workflowPolicyOutputSchema(), "active_run": outputBoolean(),
	}, "task", "state", "active_run")
	return map[string]any{"type": "object", "oneOf": []any{taskPacketOutputSchema(), inactive}}
}
