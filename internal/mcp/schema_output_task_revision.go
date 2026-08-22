package mcp

func taskRevisionOutputSchema() map[string]any {
	sha := outputString()
	sha["pattern"] = "^[0-9a-f]{64}$"
	commit := outputString()
	commit["pattern"] = "^[0-9a-f]{40}$"
	return closedOutput(map[string]any{
		"schema_version": outputInteger(), "id": outputString(), "task_id": outputString(), "task_revision": outputInteger(),
		"revision_sha256": sha, "parent_task_revision": outputInteger(), "parent_task_sha256": sha,
		"project_id": outputString(), "title": outputString(), "objective": outputString(), "branch": outputString(), "base_revision": commit,
		"acceptance_criteria": outputArray(outputString()), "constraints": outputArray(outputString()), "required_gates": outputArray(outputString()),
		"workflow_policy_revision": outputInteger(), "operation_class": outputString(), "effective_ci_field": outputString(), "effective_ci_mode": outputString(),
		"wait_for_ci": outputBoolean(), "ci_blocking": outputBoolean(), "agent_may_wait": outputBoolean(), "status": outputString(),
		"source_train_id": outputString(), "source_item_position": outputInteger(), "source_attempt_number": outputInteger(), "created_by": outputString(), "created_at": outputDateTime(),
	}, "schema_version", "id", "task_id", "task_revision", "revision_sha256", "project_id", "title", "objective", "branch", "acceptance_criteria", "constraints", "status", "created_by", "created_at")
}

func taskRevisionStatusOutputSchema() map[string]any {
	sha := outputString()
	sha["pattern"] = "^[0-9a-f]{64}$"
	commit := outputString()
	commit["pattern"] = "^[0-9a-f]{40}$"
	return closedOutput(map[string]any{
		"schema_version": outputInteger(), "id": outputString(), "task_id": outputString(), "task_revision": outputInteger(),
		"revision_sha256": sha, "parent_task_revision": outputInteger(), "status": outputString(), "branch": outputString(), "base_revision": commit,
		"source_train_id": outputString(), "source_item_position": outputInteger(), "source_attempt_number": outputInteger(), "created_at": outputDateTime(),
	}, "schema_version", "id", "task_id", "task_revision", "revision_sha256", "status", "branch", "created_at")
}

func taskCorrectionInputSchema() map[string]any {
	return obj(map[string]any{
		"task_id": str("Stable task identifier"), "source_revision_id": str("Exact terminal source revision"),
		"source_train_id": str("Exact source Train"), "source_item_position": integer("Exact source item position", 1, 1000000), "source_attempt_number": integer("Exact source attempt", 1, 1000000),
		"title": str("Optional bounded corrected title"), "objective": str("Optional bounded corrected objective"),
		"acceptance_criteria": array(str("Acceptance criterion")), "constraints": array(str("Task constraint")),
		"required_gates": array(str("Required gate")), "created_by": str("Delivery identity"),
		"expected_hub_revision": str("Optimistic Hub revision"),
	}, "task_id", "source_revision_id", "source_train_id", "source_item_position", "source_attempt_number", "created_by")
}

func taskStateOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"schema_version": outputInteger(), "task_id": outputString(), "task_sha256": outputString(),
		"status": outputString(), "superseded_by": outputString(), "reviewed_head": outputString(),
		"deferred_reason": outputString(), "integration_branch": outputString(), "integration_head": outputString(), "updated_at": outputDateTime(),
	}, "schema_version", "task_id", "task_sha256", "status", "updated_at")
}

func runOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"schema_version": outputInteger(), "id": outputString(), "task_id": outputString(), "task_sha256": outputString(),
		"task_revision": outputInteger(), "task_revision_sha256": outputString(), "task_run_number": outputInteger(),
		"project_id": outputString(), "gateway_id": outputString(), "branch": outputString(), "train_id": outputString(), "lane_branch": outputString(),
		"agent_id": outputString(), "requested_reasoning": outputString(), "resolved_reasoning": outputString(), "agent_fallback": outputBoolean(), "agent_fallback_reason": outputString(),
		"base_revision": outputString(), "hub_revision": outputString(), "status": outputString(),
		"dispatch_message": outputString(), "dispatch_exit_code": outputInteger(), "dispatch_stdout": outputString(), "dispatch_stderr": outputString(),
		"created_at": outputDateTime(), "dispatched_at": outputDateTime(),
		"reprompt_count": outputInteger(), "last_reprompt_at": outputDateTime(), "finished_at": outputDateTime(),
	}, "schema_version", "id", "task_id", "task_sha256", "project_id", "gateway_id", "branch", "base_revision", "hub_revision", "status", "created_at")
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
	return closedOutput(map[string]any{"task_id": outputString(), "task_sha256": outputString()}, "task_id", "task_sha256")
}
