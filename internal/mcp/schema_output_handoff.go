package mcp

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
