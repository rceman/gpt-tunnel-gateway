package mcp

func compactReceiptTask(value map[string]any) map[string]any {
	return selectProjectionFields(value,
		"schema_version", "id", "sha256", "project_id", "title", "type", "objective", "branch",
		"acceptance_criteria", "constraints", "status", "created_by", "created_at",
		"revision", "revision_sha256", "updated_at",
	)
}
func compactTaskRecord(value map[string]any) map[string]any {
	if task, ok := value["task"].(map[string]any); ok {
		result := copyProjectionMap(value)
		result["task"] = compactTask(task)
		if state, ok := value["state"].(map[string]any); ok {
			result["state"] = compactState(state)
		}
		if revision, ok := value["current_revision"].(map[string]any); ok {
			result["current_revision"] = compactRevision(revision)
		}
		delete(result, "workflow_policy")
		return result
	}
	return compactTask(value)
}
func compactNestedRecord(key string, value map[string]any) map[string]any {
	switch key {
	case "task":
		return compactTask(value)
	case "train":
		return compactTrain(value)
	case "state":
		return compactState(value)
	case "current_revision":
		return compactRevision(value)
	case "item", "attempt", "result", "receipt":
		return compactExecution(value)
	case "operation":
		return compactOperation(value)
	case "agent":
		return selectProjectionFields(value, "schema_version", "project_id", "agent_id", "role", "enabled", "recommended_reasoning", "capabilities", "created_at", "updated_at")
	case "guide":
		return selectProjectionFields(value, "schema_version", "project_id", "revision", "updated_by", "updated_at")
	case "configuration":
		return selectProjectionFields(value, "schema_version", "project_id", "revision", "execution_model", "activation_profile_ref", "updated_by", "updated_at")
	case "policy":
		return selectProjectionFields(value, "schema_version", "project_id", "revision", "workflow_stage", "integration_branch", "agent", "ci", "gates", "updated_by", "updated_at")
	case "identifiers":
		return selectProjectionFields(value, "schema_version", "project_id", "project_code", "next_task_number", "next_adr_number")
	case "adr":
		return selectProjectionFields(value, "schema_version", "id", "project_id", "title", "status", "supersedes", "created_at")
	default:
		return compactExecution(value)
	}
}
func compactTask(value map[string]any) map[string]any {
	return selectProjectionFields(value, "id", "project_id", "revision", "revision_sha256", "title", "type", "execution", "scope", "status", "created_at", "updated_at")
}
func compactTrain(value map[string]any) map[string]any {
	result := selectProjectionFields(value, "id", "project_id", "revision", "status", "created_by", "created_at", "updated_at")
	if items, ok := value["items"].([]any); ok {
		result["item_count"] = len(items)
	}
	return result
}
func compactState(value map[string]any) map[string]any {
	return selectProjectionFields(value, "task_id", "task_sha256", "status", "superseded_by", "reviewed_head", "integration_branch", "integration_head", "updated_at")
}
