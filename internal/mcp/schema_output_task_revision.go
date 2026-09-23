package mcp

func taskRevisionOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"schema_version": outputInteger(), "id": outputString(), "task_id": outputString(), "task_revision": outputInteger(),
		"parent_task_revision": outputInteger(),
		"project_id":           outputString(), "title": outputString(), "type": outputEnum("task", "bug", "perf", "chore"), "objective": outputString(), "branch": outputString(), "base_revision": publicGitFingerprintOrEmptyOutputSchema(),
		"acceptance_criteria": outputArray(outputString()), "constraints": outputArray(outputString()), "required_gates": outputArray(outputString()),
		"workflow_policy_revision": outputInteger(), "effective_ci_field": outputString(), "effective_ci_mode": outputString(),
		"wait_for_ci": outputBoolean(), "ci_blocking": outputBoolean(), "agent_may_wait": outputBoolean(), "status": outputString(),
		"source_train_id": outputString(), "source_item_position": outputInteger(), "source_attempt_number": outputInteger(), "created_by": outputString(), "created_at": outputDateTime(),
	}, "schema_version", "id", "task_id", "task_revision", "project_id", "title", "objective", "branch", "acceptance_criteria", "constraints", "status", "created_by", "created_at")
}

func taskRevisionStatusOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"schema_version": outputInteger(), "id": outputString(), "task_id": outputString(), "task_revision": outputInteger(),
		"parent_task_revision": outputInteger(), "status": outputString(), "branch": outputString(), "base_revision": publicGitFingerprintOrEmptyOutputSchema(),
		"source_train_id": outputString(), "source_item_position": outputInteger(), "source_attempt_number": outputInteger(), "created_at": outputDateTime(),
	}, "schema_version", "id", "task_id", "task_revision", "status", "branch", "created_at")
}

func taskCorrectionInputSchema() map[string]any {
	return obj(map[string]any{
		"task_id": str("Stable task identifier"), "source_revision_id": str("Exact terminal source revision"),
		"source_train_id": str("Exact source Train"), "source_item_position": integer("Exact source item position", 1, 1000000), "source_attempt_number": integer("Exact source attempt", 1, 1000000),
		"title": str("Optional bounded corrected title"), "objective": str("Optional bounded corrected objective"),
		"acceptance_criteria": array(str("Acceptance criterion")), "constraints": array(str("Task constraint")),
		"required_gates": array(str("Required gate")), "created_by": str("Planner identity"),
		"expected_hub_revision": str("Optimistic Hub revision"),
	}, "task_id", "source_revision_id", "source_train_id", "source_item_position", "source_attempt_number", "created_by")
}

func taskStateOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"schema_version": outputInteger(), "task_id": outputString(),
		"status": outputString(), "superseded_by": outputString(), "reviewed_head": publicGitFingerprintOrEmptyOutputSchema(),
		"deferred_reason": outputString(), "integration_branch": outputString(), "integration_head": publicGitFingerprintOrEmptyOutputSchema(), "updated_at": outputDateTime(),
	}, "schema_version", "task_id", "status", "updated_at")
}

func runOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"schema_version": outputInteger(), "id": outputString(), "task_id": outputString(),
		"task_revision": outputInteger(), "task_run_number": outputInteger(),
		"project_id": outputString(), "gateway_id": outputString(), "branch": outputString(), "lane_branch": outputString(),
		"agent_id": outputString(), "requested_reasoning": outputString(), "resolved_reasoning": outputString(), "agent_fallback": outputBoolean(), "agent_fallback_reason": outputString(),
		"base_revision": publicGitFingerprintOrEmptyOutputSchema(), "hub_revision": publicGitFingerprintOrEmptyOutputSchema(), "status": outputString(),
		"dispatch_message": outputString(), "dispatch_exit_code": outputInteger(), "dispatch_stdout": outputString(), "dispatch_stderr": outputString(),
		"created_at": outputDateTime(), "dispatched_at": outputDateTime(),
		"reprompt_count": outputInteger(), "last_reprompt_at": outputDateTime(), "finished_at": outputDateTime(),
	}, "schema_version", "id", "task_id", "project_id", "gateway_id", "branch", "base_revision", "hub_revision", "status", "created_at")
}

func ownerSummarySchema() map[string]any {
	status := outputEnum("working", "completed", "blocked", "decision_required")
	completed := outputArray(outputString())
	completed["maxItems"] = 3
	return closedOutput(map[string]any{
		"status": status, "goal": outputString(), "currently_doing": outputString(),
		"why_it_matters": outputString(), "completed_so_far": completed,
		"next_step": outputString(), "owner_action_required": map[string]any{"anyOf": []any{outputString(), map[string]any{"type": "null"}}},
	}, "status", "goal", "currently_doing", "why_it_matters", "completed_so_far", "next_step", "owner_action_required")
}

func taskRefSchema() map[string]any {
	return closedOutput(map[string]any{"task_id": outputString()}, "task_id")
}
