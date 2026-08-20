package mcp

func taskTrainStatusOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"project_id": outputString(), "train_id": outputString(), "status": outputString(),
		"current_index": outputInteger(), "task_count": outputInteger(), "current_task_id": outputString(),
		"current_task_state": outputString(), "current_attempt_status": outputString(),
		"agent_state": outputString(), "wait_reason": outputString(), "next_task_id": outputString(),
		"tail": outputString(), "next_cursor": outputString(), "has_more": outputBoolean(),
	}, "project_id", "train_id", "status", "current_index", "task_count", "has_more")
}

func trainV2AttemptFinalizeReceiptOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"operation_id": outputString(), "status": outputString(), "result": trainV2OutputSchema(), "error": outputString(),
		"created_at": outputDateTime(), "updated_at": outputDateTime(),
	}, "operation_id", "status", "created_at", "updated_at")
}

func trainV2AttemptReviewReceiptOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"operation_id": outputString(), "status": outputString(), "result": trainV2OutputSchema(), "error": outputString(),
		"created_at": outputDateTime(), "updated_at": outputDateTime(),
	}, "operation_id", "status", "created_at", "updated_at")
}

func trainV2ReviewResolutionOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"train": trainV2OutputSchema(), "resolution": trainV2OutputSchema(), "hub": map[string]any{"type": "object", "additionalProperties": true},
	}, "train", "resolution")
}

func trainV2AdmissionReceiptOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"operation_id": outputString(), "kind": outputString(), "status": outputString(), "train": trainV2OutputSchema(), "operation": operationOutputSchema(), "error": outputString(),
		"created_at": outputDateTime(), "updated_at": outputDateTime(),
	}, "operation_id", "kind", "status", "created_at", "updated_at")
}

func trainV2CutoverReceiptOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"operation_id": outputString(), "status": outputString(), "receipt": trainV2OutputSchema(), "operation": operationOutputSchema(), "error": outputString(),
		"created_at": outputDateTime(), "updated_at": outputDateTime(),
	}, "operation_id", "status", "created_at", "updated_at")
}
