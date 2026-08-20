package mcp

func compactRevision(value map[string]any) map[string]any {
	return selectProjectionFields(value, "id", "revision", "sha256", "status", "created_at", "updated_at")
}
func compactExecution(value map[string]any) map[string]any {
	return selectProjectionFields(value,
		"id", "status", "phase", "operation_id", "project_id", "task_id", "train_id", "item_position", "attempt_number", "attempt_status",
		"agent_id", "delivered", "exit_code", "outcome", "interrupt_outcome", "prompt_outcome", "requested", "prompt_delivered", "elapsed_ms",
		"session_state", "controller_reachable", "recoverable", "state", "count", "has_new_info", "overflow", "history_truncated",
		"created_at", "updated_at", "error", "reason",
	)
}
func compactOperation(value map[string]any) map[string]any {
	result := selectProjectionFields(value, "operation_id", "status", "kind", "project_id", "task_id", "train_id", "error", "created_at", "updated_at")
	if hub, ok := value["hub"].(map[string]any); ok {
		result["hub"] = selectProjectionFields(hub, "before", "after", "remote", "branch", "paths")
	}
	return result
}
func selectProjectionFields(value map[string]any, fields ...string) map[string]any {
	result := make(map[string]any, len(fields))
	for _, field := range fields {
		if item, ok := value[field]; ok {
			result[field] = item
		}
	}
	return result
}
func copyProjectionMap(value map[string]any) map[string]any {
	result := make(map[string]any, len(value))
	for key, item := range value {
		result[key] = item
	}
	return result
}
