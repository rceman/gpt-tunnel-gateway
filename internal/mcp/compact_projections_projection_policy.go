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
	"adr/create": projectionCompactDefault, "adr/list": projectionClosedDefault, "adr/read": projectionIntentionalPayload,
	"agent/disable": projectionCompactDefault, "agent/interrupt": projectionCompactDefault,
	"agent/list": projectionClosedDefault, "agent/prompt": projectionCompactDefault, "agent/read": projectionClosedDefault,
	"agent/recover": projectionCompactDefault,
	"agent/status":  projectionClosedDefault, "agent/tail": projectionIntentionalPayload, "agent/update": projectionCompactDefault,
	"code/diff": projectionIntentionalPayload, "code/read": projectionIntentionalPayload, "code/search": projectionIntentionalPayload,
	"code/tree": projectionIntentionalPayload, "code/worktree": projectionCompactDefault,
	"gateway/capabilities": projectionClosedDefault, "gateway/status": projectionClosedDefault,
	"hotfix/create": projectionCompactDefault, "hotfix/integrate": projectionCompactDefault,
	"operation/read": projectionClosedDefault, "operator/checkpoint": projectionCompactDefault, "operator/history": projectionIntentionalPayload, "operator/record": projectionCompactDefault,
	"project/status": projectionClosedDefault,
	"query/run":      projectionIntentionalPayload, "rules/read": projectionIntentionalPayload, "runtime/logs": projectionIntentionalPayload, "runtime/restart": projectionCompactDefault,
	"session/end": projectionClosedDefault, "session/info": projectionClosedDefault, "session/list": projectionClosedDefault, "session/start": projectionClosedDefault, "session/update": projectionClosedDefault, "system/await": projectionClosedDefault, "system/bootstrap": projectionClosedDefault,
	"system/batch": projectionClosedDefault, "system/call": projectionClosedDefault, "system/schema": projectionClosedDefault,
	"task/correction_create": projectionCompactDefault, "task/create": projectionCompactDefault, "task/finalize": projectionCompactDefault,
	"task/list": projectionCompactDefault, "task/read": projectionCompactDefault, "task/ready": projectionCompactDefault,
	"task/revision_list": projectionCompactDefault, "task/revision_read": projectionCompactDefault, "task/supersede": projectionCompactDefault,
	"task/update": projectionCompactDefault, "task/work": projectionCompactDefault,
	"train/add": projectionCompactDefault, "train/advance": projectionCompactDefault, "train/correction-start": projectionCompactDefault,
	"train/attempt-finalize": projectionCompactDefault, "train/attempt-proof-recover": projectionCompactDefault, "train/attempt-review": projectionCompactDefault, "train/review-resolve": projectionCompactDefault,
	"train/create": projectionCompactDefault, "train/cutover": projectionCompactDefault, "train/full-proof": projectionCompactDefault, "train/review-backfill": projectionCompactDefault,
	"train/integrate": projectionCompactDefault, "train/list": projectionCompactDefault, "train/read": projectionCompactDefault,
	"train/start":   projectionCompactDefault,
	"watcher/guide": projectionIntentionalPayload, "watcher/guide_update": projectionCompactDefault,
	"watcher/nudge": projectionCompactDefault, "watcher/status": projectionClosedDefault, "watcher/watch": projectionClosedDefault,
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
