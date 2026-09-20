package mcp

func transactionOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"before": outputString(), "after": outputString(), "remote": outputString(), "branch": outputString(), "paths": outputArray(outputString()),
	}, "before", "after", "remote", "branch", "paths")
}

func operationOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"hub": transactionOutputSchema(), "operation_id": outputString(), "project_id": outputString(), "task_id": outputString(), "status": outputString(),
	}, "hub", "status")
}

func durableMutationReceiptOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"operation_id": outputString(), "status": outputString(), "operation": operationOutputSchema(), "error": outputString(),
		"created_at": outputDateTime(), "updated_at": outputDateTime(),
	}, "operation_id", "status", "created_at", "updated_at")
}

func operationReadOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"operation_id": outputString(), "kind": outputString(), "status": outputString(), "project_id": outputString(),
		"result": map[string]any{"type": "object", "additionalProperties": true}, "error": outputString(), "recovery_reason": outputString(),
		"created_at": outputDateTime(), "updated_at": outputDateTime(),
	}, "operation_id", "kind", "status", "project_id", "created_at", "updated_at")
}

func taskWorkReceiptOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"operation_id": outputString(), "status": outputString(), "result": map[string]any{"type": "object", "additionalProperties": true}, "error": outputString(),
		"created_at": outputDateTime(), "updated_at": outputDateTime(),
	}, "operation_id", "status", "created_at", "updated_at")
}

func taskFinalizeReceiptOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"operation_id": outputString(), "status": outputString(), "result": map[string]any{"type": "object", "additionalProperties": true}, "error": outputString(),
		"created_at": outputDateTime(), "updated_at": outputDateTime(),
	}, "operation_id", "status", "created_at", "updated_at")
}

func agentMutationReceiptOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"operation_id": outputString(), "status": outputString(), "agent": agentObjectOutputSchema(), "operation": operationOutputSchema(), "error": outputString(),
		"created_at": outputDateTime(), "updated_at": outputDateTime(),
	}, "operation_id", "status", "created_at", "updated_at")
}

func agentIPCReceiptOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"operation_id": outputString(), "status": outputString(), "result": map[string]any{"type": "object", "additionalProperties": true}, "error": outputString(),
		"created_at": outputDateTime(), "updated_at": outputDateTime(),
	}, "operation_id", "status", "created_at", "updated_at")
}

func taskSupersedeReceiptOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"operation_id": outputString(), "status": outputString(), "task": taskOutputSchema(), "operation": operationOutputSchema(), "error": outputString(),
		"created_at": outputDateTime(), "updated_at": outputDateTime(),
	}, "operation_id", "status", "created_at", "updated_at")
}

func projectIdentifiersOutputSchema() map[string]any {
	schemaVersion := outputInteger()
	schemaVersion["const"] = float64(1)
	projectID := outputString()
	projectID["pattern"] = "^[a-z0-9][a-z0-9_-]{0,63}$"
	projectID["minLength"] = 1
	projectID["maxLength"] = 64
	projectCode := outputString()
	projectCode["pattern"] = "^[A-Z]{3}$"
	number := map[string]any{"type": "integer", "minimum": 1, "maximum": 9007199254740991}
	return closedOutput(map[string]any{
		"schema_version": schemaVersion, "project_id": projectID, "project_code": projectCode,
		"next_task_number": number, "next_adr_number": number,
	}, "schema_version", "project_id", "project_code", "next_task_number", "next_adr_number")
}
