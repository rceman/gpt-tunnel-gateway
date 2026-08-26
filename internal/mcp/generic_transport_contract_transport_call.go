package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

func genericBatchInputSchema() map[string]any {
	calls := array(genericBatchCallInputSchema())
	calls["maxItems"] = genericBatchMaxItems
	return obj(map[string]any{"session": str("Existing durable project-bound session shared by every batch item."), "calls": calls}, "session", "calls")
}
func sessionlessActionPath(path string) bool {
	switch path {
	case "gateway/status", "project/list", "session/list", "session/info", "session/end":
		return true
	default:
		return false
	}
}
func unboundActionAllowed(path string) bool {
	switch path {
	case "gateway/status", "project/list", "session/list", "session/bind", "session/info", "session/end":
		return true
	default:
		return false
	}
}
func genericBatchCallInputSchema() map[string]any {
	return obj(map[string]any{
		"action": str("Server-owned action path; inspect schema for available actions."),
		"input":  map[string]any{"type": "object", "additionalProperties": true, "description": "Generic action input validated by the server-owned action contract."},
	}, "action", "input")
}
func genericSchemaInputSchema() map[string]any {
	return obj(map[string]any{
		"path":   str("Empty for the root index, a domain for its actions, or an exact action path."),
		"detail": map[string]any{"type": "boolean", "description": "Return complete action contracts for a domain.", "default": false},
	})
}
func genericCallOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"result":   map[string]any{"type": "object", "additionalProperties": true},
		"is_error": outputBoolean(),
	}, "result", "is_error")
}
func genericBatchItemOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"action":   outputString(),
		"result":   map[string]any{"type": "object", "additionalProperties": true},
		"is_error": outputBoolean(),
	}, "action", "result", "is_error")
}
func genericBatchOutputSchema() map[string]any {
	return closedOutput(map[string]any{"results": outputArray(genericBatchItemOutputSchema())}, "results")
}
func genericSchemaOutputSchema() map[string]any {
	action := closedOutput(map[string]any{
		"path": outputString(), "domain": outputString(), "name": outputString(),
		"description": outputString(), "input_schema": map[string]any{"type": "object", "additionalProperties": true},
		"output_schema": map[string]any{"type": "object", "additionalProperties": true}, "annotations": map[string]any{"type": "object", "additionalProperties": true}, "session_required": outputBoolean(),
	}, "path", "domain", "name", "description", "session_required")
	return closedOutput(map[string]any{
		"revision": outputString(), "path": outputString(), "kind": outputString(),
		"domains": outputArray(outputString()), "actions": outputArray(action),
		"contract": map[string]any{"type": "object", "additionalProperties": true},
	}, "revision", "path", "kind", "domains", "actions", "contract")
}
func (s *Server) genericCall(ctx context.Context, legacy map[string]Tool, raw json.RawMessage) (any, error) {
	var input genericCallInput
	if err := decode(raw, &input); err != nil {
		return nil, err
	}
	if input.SessionID == "" {
		return nil, fmt.Errorf("session is required")
	}
	entries := s.genericActionRegistry(legacy)
	record := durableSession.Record{}
	if input.SessionID != "" {
		var err error
		record, err = s.activeSession(input.SessionID)
		if err != nil {
			return nil, err
		}
		ctx = withSession(ctx, record)
	}
	return s.genericDispatch(ctx, entries, record, input.Action, input.Input)
}
