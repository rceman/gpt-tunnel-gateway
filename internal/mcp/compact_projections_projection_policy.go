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
	"operation/read":            projectionClosedDefault,
	"project/search":            projectionClosedDefault,
	"project/identifiers_adopt": projectionCompactDefault, "project/identifiers_read": projectionClosedDefault,
	"project/onboard": projectionCompactDefault, "project/onboard_recover": projectionCompactDefault, "project/onboard_status": projectionClosedDefault, "project/read": projectionIntentionalPayload,
	"project/register": projectionCompactDefault, "project/remove": projectionCompactDefault, "project/remove_status": projectionClosedDefault, "project/update": projectionCompactDefault, "project/update_status": projectionClosedDefault,
	"project/status": projectionClosedDefault, "project/workflow_policy_adopt": projectionCompactDefault, "project/workflow_policy_read": projectionClosedDefault, "project/workflow_policy_update": projectionCompactDefault,
	"query/run": projectionIntentionalPayload, "rules/read": projectionIntentionalPayload, "runtime/logs": projectionIntentionalPayload, "runtime/restart": projectionCompactDefault,
	"session/end": projectionClosedDefault, "session/info": projectionClosedDefault, "session/list": projectionClosedDefault, "session/start": projectionClosedDefault, "session/update": projectionClosedDefault, "system/await": projectionClosedDefault, "system/bootstrap": projectionClosedDefault,
	"system/batch": projectionClosedDefault, "system/call": projectionClosedDefault, "system/schema": projectionClosedDefault,
	"task/correction_create": projectionCompactDefault, "task/create": projectionCompactDefault, "task/create_status": projectionClosedDefault, "task/finalize": projectionCompactDefault,
	"task/list": projectionCompactDefault, "task/read": projectionCompactDefault, "task/ready": projectionCompactDefault, "task/ready_status": projectionClosedDefault,
	"task/revision_list": projectionCompactDefault, "task/revision_read": projectionCompactDefault, "task/revision_status": projectionClosedDefault, "task/supersede": projectionCompactDefault,
	"task/supersede_status": projectionClosedDefault, "task/update": projectionCompactDefault, "task/update_status": projectionClosedDefault, "task/work": projectionCompactDefault, "task/work_status": projectionClosedDefault,
	"train/add": projectionCompactDefault, "train/add_status": projectionClosedDefault, "train/advance": projectionCompactDefault, "train/advance_status": projectionClosedDefault, "train/correction-start": projectionCompactDefault, "train/correction-start_status": projectionClosedDefault, "train/retire": projectionClosedDefault, "train/retire_status": projectionClosedDefault, "train/reconcile": projectionClosedDefault, "train/reconcile_status": projectionClosedDefault,
	"train/attempt-finalize": projectionCompactDefault, "train/attempt-finalize_status": projectionClosedDefault, "train/attempt-proof-recover": projectionCompactDefault, "train/attempt-proof-recover_status": projectionClosedDefault, "train/attempt-review": projectionCompactDefault, "train/attempt-review_status": projectionClosedDefault, "train/review-resolve": projectionCompactDefault,
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
	case "task/revision_list":
		return compactTaskRevisionListResult(value)
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
