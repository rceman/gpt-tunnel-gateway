package mcp

func compactTaskListResult(value map[string]any) map[string]any {
	result := copyProjectionMap(value)
	if tasks, ok := value["tasks"].([]any); ok {
		compact := make([]any, len(tasks))
		for i, task := range tasks {
			if object, ok := task.(map[string]any); ok {
				compact[i] = compactTaskRecord(object)
			} else {
				compact[i] = task
			}
		}
		result["tasks"] = compact
	}
	return result
}

func compactTaskRevisionListResult(value map[string]any) map[string]any {
	result := copyProjectionMap(value)
	if revisions, ok := value["revisions"].([]any); ok {
		compact := make([]any, len(revisions))
		for i, revision := range revisions {
			if object, ok := revision.(map[string]any); ok {
				compact[i] = compactRevision(object)
			} else {
				compact[i] = revision
			}
		}
		result["revisions"] = compact
	}
	return result
}

func compactTaskReadResult(value map[string]any) map[string]any {
	result := copyProjectionMap(value)
	for _, key := range []string{"task", "state", "current_revision", "workflow_policy", "train", "item", "attempt"} {
		if object, ok := value[key].(map[string]any); ok {
			result[key] = compactNestedRecord(key, object)
		}
	}
	for _, key := range []string{"project_configuration", "workflow_policy", "repository_root", "text"} {
		delete(result, key)
	}
	return result
}
func compactTrainListResult(value map[string]any) map[string]any {
	result := copyProjectionMap(value)
	if trains, ok := value["trains"].([]any); ok {
		compact := make([]any, len(trains))
		for i, train := range trains {
			if object, ok := train.(map[string]any); ok {
				compact[i] = compactTrain(object)
			} else {
				compact[i] = train
			}
		}
		result["trains"] = compact
	}
	return result
}
func compactTrainReadResult(value map[string]any) map[string]any {
	result := copyProjectionMap(value)
	if train, ok := value["train"].(map[string]any); ok {
		result["train"] = compactTrain(train)
	} else if _, ok := value["items"]; ok {
		return compactTrain(value)
	}
	return result
}
func compactMutationResult(action string, value map[string]any) map[string]any {
	result := copyProjectionMap(value)
	for _, key := range []string{"task", "train", "result", "receipt", "operation", "item", "attempt", "agent", "guide", "configuration", "policy", "identifiers", "adr"} {
		if object, ok := value[key].(map[string]any); ok {
			if key == "result" && action == "agent/prompt" {
				if delivered, ok := object["delivered"].(bool); ok && delivered {
					result[key] = selectProjectionFields(object, "project_id")
					continue
				}
			}
			if key == "task" && (action == "task/supersede" || action == "task/supersede_status") {
				// Some mutation receipts expose a closed taskOutputSchema. Keep
				// that schema's required fields while still dropping optional
				// preparation/detail fields from the nested projection.
				result[key] = compactReceiptTask(object)
			} else {
				result[key] = compactNestedRecord(key, object)
			}
		}
	}
	return result
}
