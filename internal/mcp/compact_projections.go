package mcp

import (
	"encoding/json"
	"fmt"
)

type projectionClass uint8

const (
	projectionCompactDefault projectionClass = iota + 1
	projectionClosedDefault
	projectionIntentionalPayload
)

// projectionClasses is the complete classification of the active generic
// action registry. Keep this table explicit: a newly registered action must
// choose a bounded default or be deliberately documented as a payload action.
var projectionClasses = map[string]projectionClass{
	"adr/create": projectionCompactDefault, "adr/create_status": projectionClosedDefault, "adr/list": projectionClosedDefault, "adr/read": projectionIntentionalPayload,
	"agent/disable": projectionCompactDefault, "agent/disable_status": projectionClosedDefault, "agent/interrupt": projectionCompactDefault, "agent/interrupt_status": projectionClosedDefault,
	"agent/list": projectionClosedDefault, "agent/prompt": projectionCompactDefault, "agent/prompt_status": projectionClosedDefault, "agent/read": projectionClosedDefault,
	"agent/recover": projectionCompactDefault, "agent/recover_status": projectionClosedDefault, "agent/register": projectionCompactDefault, "agent/register_status": projectionClosedDefault,
	"agent/status": projectionClosedDefault, "agent/tail": projectionIntentionalPayload, "agent/update": projectionCompactDefault, "agent/update_status": projectionClosedDefault,
	"gateway/capabilities": projectionClosedDefault, "gateway/status": projectionClosedDefault,
	"git/compare": projectionIntentionalPayload, "git/diff": projectionIntentionalPayload, "git/log": projectionIntentionalPayload, "git/merge_base": projectionIntentionalPayload,
	"git/read_file": projectionIntentionalPayload, "git/refresh": projectionClosedDefault, "git/refs": projectionIntentionalPayload, "git/show": projectionIntentionalPayload,
	"git/tree": projectionIntentionalPayload, "git/worktree_diff": projectionIntentionalPayload, "git/worktree_status": projectionClosedDefault,
	"operator/checkpoint": projectionCompactDefault, "operator/history": projectionIntentionalPayload, "operator/record": projectionCompactDefault,
	"project/identifiers_adopt": projectionCompactDefault, "project/identifiers_read": projectionClosedDefault, "project/list": projectionClosedDefault,
	"project/onboard": projectionCompactDefault, "project/onboard_recover": projectionCompactDefault, "project/onboard_status": projectionClosedDefault, "project/read": projectionIntentionalPayload,
	"project/register": projectionCompactDefault, "project/remove": projectionCompactDefault, "project/remove_status": projectionClosedDefault, "project/update": projectionCompactDefault, "project/update_status": projectionClosedDefault,
	"project/workflow_policy_adopt": projectionCompactDefault, "project/workflow_policy_read": projectionClosedDefault, "project/workflow_policy_update": projectionCompactDefault,
	"query/run": projectionIntentionalPayload, "rules/read": projectionIntentionalPayload, "runtime/logs": projectionIntentionalPayload, "runtime/restart": projectionCompactDefault,
	"session/end": projectionClosedDefault, "session/info": projectionClosedDefault, "session/list": projectionClosedDefault, "session/start": projectionClosedDefault, "session/update": projectionClosedDefault,
	"system/batch": projectionClosedDefault, "system/call": projectionClosedDefault, "system/schema": projectionClosedDefault,
	"task/correction_create": projectionCompactDefault, "task/create": projectionCompactDefault, "task/create_status": projectionClosedDefault, "task/finalize": projectionCompactDefault,
	"task/list": projectionCompactDefault, "task/read": projectionCompactDefault, "task/ready": projectionCompactDefault, "task/ready_status": projectionClosedDefault,
	"task/revision_list": projectionCompactDefault, "task/revision_read": projectionCompactDefault, "task/revision_status": projectionClosedDefault, "task/supersede": projectionCompactDefault,
	"task/supersede_status": projectionClosedDefault, "task/update": projectionCompactDefault, "task/update_status": projectionClosedDefault, "task/work": projectionCompactDefault, "task/work_status": projectionClosedDefault,
	"train/add": projectionCompactDefault, "train/add_status": projectionClosedDefault, "train/advance": projectionCompactDefault, "train/advance_status": projectionClosedDefault,
	"train/attempt-finalize": projectionCompactDefault, "train/attempt-finalize_status": projectionClosedDefault, "train/attempt-proof-recover": projectionCompactDefault, "train/attempt-proof-recover_status": projectionClosedDefault, "train/attempt-review": projectionCompactDefault, "train/attempt-review_status": projectionClosedDefault,
	"train/create": projectionCompactDefault, "train/create_status": projectionClosedDefault, "train/cutover": projectionCompactDefault, "train/cutover_status": projectionClosedDefault, "train/full-proof": projectionCompactDefault, "train/full-proof_status": projectionClosedDefault, "train/review-backfill": projectionCompactDefault, "train/review-backfill_status": projectionClosedDefault,
	"train/integrate": projectionCompactDefault, "train/integrate_status": projectionCompactDefault, "train/list": projectionCompactDefault, "train/read": projectionCompactDefault,
	"train/start": projectionCompactDefault, "train/start_status": projectionCompactDefault,
	"watcher/guide": projectionIntentionalPayload, "watcher/guide_update": projectionCompactDefault, "watcher/guide_update_status": projectionClosedDefault,
	"watcher/nudge": projectionCompactDefault, "watcher/nudge_status": projectionClosedDefault, "watcher/status": projectionClosedDefault, "watcher/watch": projectionClosedDefault,
	"workflow/rules": projectionIntentionalPayload,
}

func compactProjectionAction(path string) bool {
	return projectionClasses[path] == projectionCompactDefault
}

func projectionDetailAction(path string) bool {
	switch path {
	case "task/list", "task/read", "train/list", "train/read":
		return true
	default:
		return false
	}
}

func withProjectionDetail(schema map[string]any) map[string]any {
	if schema == nil {
		return nil
	}
	result := make(map[string]any, len(schema)+1)
	for key, value := range schema {
		result[key] = value
	}
	properties, _ := schema["properties"].(map[string]any)
	if properties != nil {
		copyProperties := make(map[string]any, len(properties)+1)
		for key, value := range properties {
			copyProperties[key] = value
		}
		copyProperties["detail"] = map[string]any{
			"type":        "boolean",
			"description": "Return the complete durable payload instead of the compact projection.",
			"default":     false,
		}
		result["properties"] = copyProperties
	}
	if branches, ok := schema["oneOf"].([]any); ok {
		copyBranches := make([]any, len(branches))
		for i, branch := range branches {
			if object, ok := branch.(map[string]any); ok {
				copyBranches[i] = withProjectionDetail(object)
			} else {
				copyBranches[i] = branch
			}
		}
		result["oneOf"] = copyBranches
	}
	return result
}

func stripProjectionDetail(raw json.RawMessage) (json.RawMessage, bool, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, false, err
	}
	detailRaw, ok := fields["detail"]
	if !ok {
		return raw, false, nil
	}
	var detail bool
	if err := json.Unmarshal(detailRaw, &detail); err != nil {
		return nil, false, fmt.Errorf("detail must be a boolean: %w", err)
	}
	delete(fields, "detail")
	clean, err := json.Marshal(fields)
	if err != nil {
		return nil, false, err
	}
	return clean, detail, nil
}

func compactActionResult(action string, value map[string]any, detail bool) map[string]any {
	if detail || !compactProjectionAction(action) {
		return value
	}
	switch action {
	case "task/list":
		return compactTaskListResult(value)
	case "task/read":
		return compactTaskReadResult(value)
	case "train/list":
		return compactTrainListResult(value)
	case "train/read":
		return compactTrainReadResult(value)
	default:
		return compactMutationResult(action, value)
	}
}

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

func compactReceiptTask(value map[string]any) map[string]any {
	return selectProjectionFields(value,
		"schema_version", "id", "sha256", "project_id", "title", "objective", "branch",
		"acceptance_criteria", "constraints", "status", "created_by", "created_at",
		"revision", "revision_sha256", "operation_class", "updated_at",
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
	return selectProjectionFields(value, "id", "project_id", "revision", "revision_sha256", "title", "status", "operation_class", "created_at", "updated_at")
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
