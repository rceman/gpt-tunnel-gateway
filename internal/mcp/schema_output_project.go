package mcp

func outputString() map[string]any  { return map[string]any{"type": "string"} }
func outputBoolean() map[string]any { return map[string]any{"type": "boolean"} }
func outputInteger() map[string]any { return map[string]any{"type": "integer"} }
func outputDateTime() map[string]any {
	return map[string]any{"type": "string", "format": "date-time"}
}
func outputArray(items map[string]any) map[string]any {
	return map[string]any{"type": "array", "items": items}
}
func outputEnum(values ...string) map[string]any {
	items := make([]any, 0, len(values))
	for _, value := range values {
		items = append(items, value)
	}
	return map[string]any{"type": "string", "enum": items}
}
func closedOutput(properties map[string]any, required ...string) map[string]any {
	schema := map[string]any{"type": "object", "additionalProperties": false, "properties": properties}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func projectOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"schema_version": outputInteger(), "id": outputString(), "repository_url": outputString(),
		"default_branch": outputString(), "workflow_repository": outputString(), "workflow_commit": outputString(),
		"status": outputString(), "active_task_id": outputString(), "active_run_id": outputString(),
		"created_at": outputDateTime(), "updated_at": outputDateTime(),
	}, "schema_version", "id", "repository_url", "default_branch", "workflow_repository", "workflow_commit", "status", "created_at", "updated_at")
}

func planOutputSchema() map[string]any {
	sectionIndex := closedOutput(map[string]any{
		"id": outputString(), "title": outputString(), "short_description": outputString(), "revision": outputInteger(),
	}, "id", "title", "short_description", "revision")
	return closedOutput(map[string]any{
		"schema_version": outputInteger(), "project_id": outputString(), "revision": outputInteger(),
		"title": outputString(), "summary": outputString(), "current_objective": outputString(), "queue": outputArray(outputString()), "sections": outputArray(sectionIndex),
		"active_task_id": outputString(), "active_run_id": outputString(),
		"updated_by": outputString(), "updated_at": outputDateTime(),
	}, "schema_version", "project_id", "revision", "title", "summary", "current_objective", "queue", "sections", "updated_by", "updated_at")
}

func planStatusOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"schema_version": outputInteger(), "project_id": outputString(), "revision": outputInteger(),
		"title": outputString(), "summary": outputString(), "current_objective": outputString(), "queue": outputArray(outputString()), "sections": outputArray(outputString()),
		"active_task_id": outputString(), "active_run_id": outputString(), "updated_by": outputString(), "updated_at": outputDateTime(),
	}, "schema_version", "project_id", "revision", "title", "summary", "current_objective", "queue", "sections", "updated_by", "updated_at")
}

func planSectionOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"schema_version": outputInteger(), "project_id": outputString(), "id": outputString(), "revision": outputInteger(),
		"title": outputString(), "short_description": outputString(), "description": outputString(), "updated_by": outputString(), "updated_at": outputDateTime(),
	}, "schema_version", "project_id", "id", "revision", "title", "short_description", "description", "updated_by", "updated_at")
}

func planRenderOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"schema_version": outputInteger(), "project_id": outputString(), "revision": outputInteger(), "title": outputString(),
		"summary": outputString(), "current_objective": outputString(), "text": outputString(),
	}, "schema_version", "project_id", "revision", "title", "summary", "current_objective", "text")
}

func adrOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"schema_version": outputInteger(), "id": outputString(), "project_id": outputString(), "title": outputString(),
		"status": outputString(), "context": outputString(), "decision": outputString(), "consequences": outputString(),
		"supersedes": outputString(), "created_at": outputDateTime(),
	}, "schema_version", "id", "project_id", "title", "status", "context", "decision", "consequences", "created_at")
}

func taskOutputSchema() map[string]any {
	strings := outputArray(outputString())
	return closedOutput(map[string]any{
		"schema_version": outputInteger(), "id": outputString(), "sha256": outputString(), "project_id": outputString(),
		"title": outputString(), "objective": outputString(), "branch": outputString(), "base_revision": outputString(),
		"acceptance_criteria": strings, "constraints": strings, "required_gates": strings,
		"workflow_policy_revision": outputInteger(), "operation_class": outputString(), "effective_ci_field": outputString(), "effective_ci_mode": outputString(), "wait_for_ci": outputBoolean(), "ci_blocking": outputBoolean(), "agent_may_wait": outputBoolean(),
		"status": outputString(), "supersedes": outputString(), "created_by": outputString(), "created_at": outputDateTime(),
	}, "schema_version", "id", "sha256", "project_id", "title", "objective", "branch", "acceptance_criteria", "constraints", "status", "created_by", "created_at")
}
