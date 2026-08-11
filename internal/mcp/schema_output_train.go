package mcp

func taskTrainStatusOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"project_id": outputString(), "train_id": outputString(), "status": outputString(),
		"current_index": outputInteger(), "task_count": outputInteger(), "current_task_id": outputString(),
		"current_run_id": outputString(), "current_task_state": outputString(), "current_run_status": outputString(),
		"agent_state": outputString(), "wait_reason": outputString(), "next_task_id": outputString(),
		"tail": outputString(), "next_cursor": outputString(), "has_more": outputBoolean(),
	}, "project_id", "train_id", "status", "current_index", "task_count", "has_more")
}
