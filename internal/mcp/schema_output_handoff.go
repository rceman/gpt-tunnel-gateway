package mcp

func deliveryHandoffSchema() map[string]any {
	return closedOutput(map[string]any{
		"schema_version": outputInteger(), "id": outputString(), "project_id": outputString(), "task_id": outputString(), "run_id": outputString(), "task_sha256": outputString(),
		"status": outputString(), "owner_summary": ownerSummarySchema(), "technical_evidence": map[string]any{"type": "object"},
		"current_report_id": outputString(), "supersedes_handoff_id": outputString(), "superseded_by_handoff_id": outputString(),
		"plan_revision": outputInteger(), "hub_revision": outputString(), "task_refs": outputArray(taskRefSchema()), "train_refs": outputArray(outputString()),
		"plan_section_refs": outputArray(outputString()), "operator_event_refs": outputArray(outputString()), "expected_repo_base": outputString(), "expected_repo_head": outputString(),
		"first_action": outputString(), "stop_boundary": outputString(), "prohibited_operations": outputArray(outputString()), "instruction_body": outputString(),
		"role_refs": outputArray(outputString()), "delegation_refs": outputArray(outputString()), "author_role": outputString(), "consumer_role": outputString(),
		"canonical_digest": outputString(), "created_by": outputString(), "acknowledged_by": outputString(), "started_by": outputString(),
		"cancelled_by": outputString(), "cancel_reason": outputString(), "created_at": outputDateTime(), "updated_at": outputDateTime(),
		"acknowledged_at": outputDateTime(), "started_at": outputDateTime(), "cancelled_at": outputDateTime(),
	}, "schema_version", "id", "project_id", "task_id", "run_id", "task_sha256", "status", "owner_summary", "technical_evidence", "plan_revision", "hub_revision", "task_refs", "train_refs", "plan_section_refs", "operator_event_refs", "expected_repo_base", "expected_repo_head", "first_action", "stop_boundary", "prohibited_operations", "instruction_body", "role_refs", "delegation_refs", "author_role", "consumer_role", "canonical_digest", "created_by", "created_at", "updated_at")
}

func deliveryHandoffStatusSchema() map[string]any {
	return closedOutput(map[string]any{
		"schema_version": outputInteger(), "id": outputString(), "project_id": outputString(), "task_id": outputString(), "run_id": outputString(),
		"status": outputString(), "owner_summary": ownerSummarySchema(), "current_report_id": outputString(), "supersedes_handoff_id": outputString(), "superseded_by_handoff_id": outputString(),
		"created_at": outputDateTime(), "updated_at": outputDateTime(),
	}, "schema_version", "id", "project_id", "task_id", "run_id", "status", "owner_summary", "created_at", "updated_at")
}

func plannerReportSchema() map[string]any {
	return closedOutput(map[string]any{
		"schema_version": outputInteger(), "id": outputString(), "project_id": outputString(), "handoff_id": outputString(), "task_id": outputString(), "run_id": outputString(), "task_sha256": outputString(),
		"report_type": outputString(), "owner_summary": ownerSummarySchema(), "technical_evidence": map[string]any{"type": "object"}, "supersedes_report_id": outputString(),
		"published_by": outputString(), "published_at": outputDateTime(),
	}, "schema_version", "id", "project_id", "handoff_id", "task_id", "run_id", "task_sha256", "report_type", "owner_summary", "technical_evidence", "published_by", "published_at")
}

func plannerReportStatusSchema() map[string]any {
	return closedOutput(map[string]any{
		"schema_version": outputInteger(), "id": outputString(), "project_id": outputString(), "handoff_id": outputString(), "task_id": outputString(), "run_id": outputString(),
		"report_type": outputString(), "owner_summary": ownerSummarySchema(), "supersedes_report_id": outputString(), "published_by": outputString(), "published_at": outputDateTime(), "status": outputString(),
	}, "schema_version", "id", "project_id", "handoff_id", "task_id", "run_id", "report_type", "owner_summary", "published_by", "published_at", "status")
}

func plannerReportStateSchema() map[string]any {
	return closedOutput(map[string]any{
		"schema_version": outputInteger(), "report_id": outputString(), "report_sha256": outputString(), "status": outputString(), "acknowledged_by": outputString(), "resolved_by": outputString(),
		"updated_at": outputDateTime(), "acknowledged_at": outputDateTime(), "resolved_at": outputDateTime(),
	}, "schema_version", "report_id", "report_sha256", "status", "updated_at")
}

func nullableInputString(desc string) map[string]any {
	return map[string]any{"anyOf": []any{str(desc), map[string]any{"type": "null"}}}
}

func ownerSummaryInputSchema() map[string]any {
	status := str("Owner-facing status")
	status["enum"] = []string{"working", "completed", "blocked", "decision_required"}
	completed := array(str("Completed item"))
	completed["maxItems"] = 3
	return obj(map[string]any{
		"status": status, "goal": str("Owner-facing goal"), "currently_doing": str("Current owner-facing activity"),
		"why_it_matters": str("Owner-facing importance"), "completed_so_far": completed, "next_step": str("Next owner-facing step"),
		"owner_action_required": nullableInputString("Optional owner action"),
	}, "status", "goal", "currently_doing", "why_it_matters", "completed_so_far", "next_step", "owner_action_required")
}

func taskRefsInputSchema() map[string]any {
	return array(obj(map[string]any{"task_id": str("Task identifier"), "task_sha256": str("Exact durable task hash")}, "task_id", "task_sha256"))
}
