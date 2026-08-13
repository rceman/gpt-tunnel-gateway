package mcp

func planToolOutputSchemas() map[string]map[string]any {
	return map[string]map[string]any{
		"project_register":    operationOutputSchema(),
		"plan_read":           planOutputSchema(),
		"plan_cutover":        operationOutputSchema(),
		"plan_update":         operationOutputSchema(),
		"plan_section_read":   planSectionOutputSchema(),
		"plan_section_create": operationOutputSchema(),
		"plan_section_update": operationOutputSchema(),
		"plan_section_delete": operationOutputSchema(),
		"plan_render":         planRenderOutputSchema(),
		"plan_history": closedOutput(map[string]any{"history": outputArray(closedOutput(map[string]any{
			"sha": outputString(), "date": outputString(), "author": outputString(), "subject": outputString(),
		}, "sha", "date", "author", "subject")), "next_cursor": outputString(), "has_more": outputBoolean()}, "history", "next_cursor", "has_more"),
		"adr_list":               closedOutput(map[string]any{"adrs": outputArray(adrOutputSchema()), "next_cursor": outputString(), "has_more": outputBoolean()}, "adrs", "next_cursor", "has_more"),
		"adr_read":               adrOutputSchema(),
		"adr_create":             operationOutputSchema(),
		"task_create":            closedOutput(map[string]any{"task": taskOutputSchema(), "operation": operationOutputSchema()}, "task", "operation"),
		"task_revision_list":     closedOutput(map[string]any{"revisions": outputArray(taskRevisionOutputSchema()), "next_cursor": outputString(), "has_more": outputBoolean()}, "revisions", "next_cursor", "has_more"),
		"task_revision_read":     taskRevisionOutputSchema(),
		"task_revision_status":   taskRevisionStatusOutputSchema(),
		"task_correction_create": closedOutput(map[string]any{"revision": taskRevisionOutputSchema(), "operation": operationOutputSchema()}, "revision", "operation"),
		"task_list":              closedOutput(map[string]any{"tasks": outputArray(taskRecordOutputSchema()), "next_cursor": outputString(), "has_more": outputBoolean()}, "tasks", "next_cursor", "has_more"),
		"task_read":              taskReadOutputSchema(),
		"task_supersede":         closedOutput(map[string]any{"task": taskOutputSchema(), "operation": operationOutputSchema()}, "task", "operation"),
	}
}
