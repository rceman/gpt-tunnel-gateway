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
	"adr/archive": projectionCompactDefault, "adr/create": projectionCompactDefault, "adr/history": projectionClosedDefault,
	"adr/guide": projectionClosedDefault, "adr/list": projectionClosedDefault, "adr/query": projectionClosedDefault, "adr/read": projectionIntentionalPayload, "adr/update": projectionCompactDefault,
	"agent/await": projectionClosedDefault, "agent/guide": projectionClosedDefault, "agent/interrupt": projectionCompactDefault,
	"agent/list": projectionClosedDefault, "agent/prompt": projectionCompactDefault,
	"agent/status": projectionClosedDefault, "agent/tail": projectionIntentionalPayload,
	"callback/events": projectionClosedDefault, "callback/list": projectionClosedDefault, "callback/register": projectionCompactDefault, "callback/remove": projectionCompactDefault,
	"code/diff": projectionIntentionalPayload, "code/read": projectionIntentionalPayload, "code/search": projectionIntentionalPayload,
	"code/tree": projectionIntentionalPayload, "code/worktree": projectionCompactDefault,
	"gateway/capabilities": projectionClosedDefault, "gateway/status": projectionClosedDefault,
	"journal/add": projectionCompactDefault, "journal/contract": projectionClosedDefault, "journal/guide": projectionClosedDefault, "journal/list": projectionClosedDefault, "journal/read": projectionIntentionalPayload,
	"message/cancel": projectionClosedDefault, "message/create": projectionClosedDefault, "message/list": projectionCompactDefault, "message/read": projectionIntentionalPayload,
	"milestone/guide": projectionClosedDefault, "milestone/activate": projectionCompactDefault, "milestone/append_task": projectionCompactDefault, "milestone/archive": projectionCompactDefault, "milestone/complete": projectionCompactDefault, "milestone/create": projectionCompactDefault, "milestone/history": projectionClosedDefault, "milestone/list": projectionClosedDefault, "milestone/query": projectionClosedDefault, "milestone/read": projectionIntentionalPayload, "milestone/remove_task": projectionCompactDefault, "milestone/update": projectionCompactDefault,
	"operation/await": projectionClosedDefault, "operation/read": projectionClosedDefault, "project/guide_bind": projectionClosedDefault, "project/status": projectionClosedDefault,
	"track/guide": projectionClosedDefault, "track/accept": projectionCompactDefault, "track/append_task": projectionCompactDefault, "track/archive": projectionCompactDefault, "track/cancel": projectionCompactDefault, "track/create": projectionCompactDefault, "track/history": projectionClosedDefault, "track/list": projectionClosedDefault, "track/query": projectionClosedDefault, "track/read": projectionIntentionalPayload, "track/remove_task": projectionCompactDefault, "track/submit": projectionCompactDefault, "track/update": projectionCompactDefault,
	"relation/create": projectionCompactDefault, "relation/list": projectionClosedDefault,
	"rule/guide": projectionClosedDefault, "rule/archive": projectionCompactDefault, "rule/create": projectionCompactDefault, "rule/effective": projectionIntentionalPayload, "rule/history": projectionClosedDefault,
	"rule/list": projectionClosedDefault, "rule/query": projectionClosedDefault, "rule/read": projectionIntentionalPayload, "rule/update": projectionCompactDefault,
	"runtime/logs": projectionIntentionalPayload, "runtime/restart": projectionCompactDefault,
	"session/end": projectionClosedDefault, "session/info": projectionClosedDefault, "session/list": projectionClosedDefault, "session/start": projectionClosedDefault, "system/await": projectionClosedDefault,
	"task/archive": projectionClosedDefault, "task/create": projectionClosedDefault, "task/integrate": projectionClosedDefault,
	"task/complete": projectionClosedDefault, "task/guide": projectionClosedDefault, "task/test": projectionClosedDefault,
	"task/current": projectionClosedDefault, "task/dispatch": projectionClosedDefault, "task/history": projectionClosedDefault, "task/list": projectionClosedDefault,
	"task/query": projectionClosedDefault,
	"task/read":  projectionClosedDefault, "task/review": projectionClosedDefault, "task/review_decide": projectionClosedDefault, "task/rework": projectionClosedDefault, "task/block": projectionClosedDefault, "task/resume": projectionClosedDefault, "task/refresh": projectionClosedDefault,
	"task/submit-code": projectionClosedDefault, "task/submit-rebase": projectionClosedDefault,
	"task/status": projectionClosedDefault, "task/update": projectionClosedDefault,
}

func compactProjectionAction(path string) bool {
	return projectionClasses[path] == projectionCompactDefault
}
func projectionDetailAction(path string) bool {
	return false
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
	default:
		return compactMutationResult(action, value)
	}
}
