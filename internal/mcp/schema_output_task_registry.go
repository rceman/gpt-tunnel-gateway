package mcp

func taskToolOutputSchemas() map[string]map[string]any {
	return map[string]map[string]any{
		"task_create":            closedOutput(map[string]any{"task": taskOutputSchema(), "operation": operationOutputSchema()}, "task", "operation"),
		"task_revision_list":     closedOutput(map[string]any{"revisions": outputArray(taskRevisionOutputSchema()), "next_cursor": outputString(), "has_more": outputBoolean()}, "revisions", "next_cursor", "has_more"),
		"task_revision_read":     taskRevisionOutputSchema(),
		"task_correction_create": closedOutput(map[string]any{"revision": taskRevisionOutputSchema(), "operation": operationOutputSchema()}, "revision", "operation"),
		"task_list":              closedOutput(map[string]any{"tasks": outputArray(taskRecordOutputSchema()), "next_cursor": outputString(), "has_more": outputBoolean()}, "tasks", "next_cursor", "has_more"),
		"task_read":              taskReadOutputSchema(),
		"task_supersede":         taskSupersedeReceiptOutputSchema(),
	}
}
