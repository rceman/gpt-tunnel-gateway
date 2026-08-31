package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

func inheritSessionProject(schema map[string]any, projectID string, raw json.RawMessage) (json.RawMessage, error) {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, fmt.Errorf("input must be an object")
	}
	bound, err := bindProjectValue(schema, value, projectID)
	if err != nil {
		return nil, err
	}
	return json.Marshal(bound)
}

func bindProjectValue(schema map[string]any, value any, projectID string) (any, error) {
	properties, _ := schema["properties"].(map[string]any)
	switch current := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(current)+1)
		for key, child := range current {
			childSchema, _ := properties[key].(map[string]any)
			bound, err := bindProjectValue(childSchema, child, projectID)
			if err != nil {
				return nil, err
			}
			result[key] = bound
		}
		if _, ok := properties["project_id"]; ok {
			if supplied, exists := current["project_id"]; exists {
				if suppliedString, ok := supplied.(string); !ok || suppliedString != projectID {
					return nil, fmt.Errorf("project_id does not match session project")
				}
			} else if containsRequired(stringList(schema["required"]), "project_id") {
				result["project_id"] = projectID
			}
		}
		return result, nil
	case []any:
		items, _ := schema["items"].(map[string]any)
		result := make([]any, len(current))
		for i, child := range current {
			bound, err := bindProjectValue(items, child, projectID)
			if err != nil {
				return nil, err
			}
			result[i] = bound
		}
		return result, nil
	default:
		return value, nil
	}
}

func containsRequired(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
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
	case "gateway/status", "project/list", "session/list", "session/info", "session/end":
		return true
	default:
		return false
	}
}
func genericSchemaInputSchema() map[string]any {
	return obj(map[string]any{
		"path": str("Empty for the root index, a domain for its actions, or an exact action path."),
	})
}

func genericSchemaPublicInputSchema() map[string]any {
	session := str("Existing durable project-bound session identifier.")
	session["minLength"] = 1
	return obj(map[string]any{
		"session": session,
		"path":    str("Optional root, domain, or exact action path."),
	}, "session")
}
func genericCallOutputSchema() map[string]any {
	metrics := closedOutput(map[string]any{
		"time":   integer("Elapsed execution time in milliseconds.", 0, 1<<31-1),
		"tokens": integer("Serialized response token estimate.", 0, 1<<31-1),
	}, "time", "tokens")
	errorObject := closedOutput(map[string]any{
		"code":    outputString(),
		"message": outputString(),
	}, "code", "message")
	success := closedOutput(map[string]any{
		"ok":         map[string]any{"const": true},
		"result":     map[string]any{"type": "object", "additionalProperties": true},
		"pagination": map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{"next_cursor": outputString()}, "required": []string{"next_cursor"}},
		"metrics":    metrics,
	}, "ok", "result", "metrics")
	failure := closedOutput(map[string]any{
		"ok":      map[string]any{"const": false},
		"error":   errorObject,
		"metrics": metrics,
	}, "ok", "error", "metrics")
	return map[string]any{"type": "object", "oneOf": []any{success, failure}}
}
func genericSchemaOutputSchema() map[string]any {
	action := closedOutput(map[string]any{
		"path":        outputString(),
		"description": outputString(),
	}, "path", "description")
	annotations := closedOutput(map[string]any{
		"read_only":   outputBoolean(),
		"destructive": outputBoolean(),
		"idempotent":  outputBoolean(),
		"open_world":  outputBoolean(),
	}, "read_only", "destructive", "idempotent", "open_world")
	contract := closedOutput(map[string]any{
		"description":   outputString(),
		"input_schema":  map[string]any{"type": "object", "additionalProperties": true},
		"output_schema": map[string]any{"type": "object", "additionalProperties": true},
		"annotations":   annotations,
	}, "description", "input_schema", "output_schema", "annotations")
	root := closedOutput(map[string]any{
		"revision": outputString(), "kind": outputString(), "path": outputString(),
		"domains": outputArray(closedOutput(map[string]any{"key": outputString()}, "key")),
	}, "revision", "kind", "path", "domains")
	domain := closedOutput(map[string]any{
		"revision": outputString(), "kind": outputString(), "path": outputString(),
		"actions": outputArray(action),
	}, "revision", "kind", "path", "actions")
	actionResult := closedOutput(map[string]any{
		"revision": outputString(), "kind": outputString(), "path": outputString(),
		"contract": contract,
	}, "revision", "kind", "path", "contract")
	return map[string]any{"type": "object", "oneOf": []any{root, domain, actionResult}}
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

func (s *Server) genericCallPublic(ctx context.Context, legacy map[string]Tool, raw json.RawMessage) (any, error) {
	started := time.Now()
	value, err := s.genericCall(ctx, legacy, raw)
	if err != nil {
		return publicCallFailure("CALL_FAILED", err.Error(), time.Since(started)), nil
	}
	internal, ok := value.(map[string]any)
	if !ok {
		return publicCallFailure("CALL_FAILED", "action returned a non-object result", time.Since(started)), nil
	}
	if internal["is_error"] == true {
		return publicCallFailureFromInternal(internal, time.Since(started)), nil
	}
	result, ok := internal["result"].(map[string]any)
	if !ok {
		return publicCallFailure("CALL_FAILED", "action returned no object result", time.Since(started)), nil
	}
	envelope := map[string]any{
		"ok":      true,
		"result":  result,
		"metrics": publicCallMetrics(result, time.Since(started)),
	}
	if pagination, ok := result["_pagination"].(map[string]any); ok {
		if cursor, ok := pagination["next_cursor"].(string); ok && cursor != "" {
			envelope["pagination"] = map[string]any{"next_cursor": cursor}
		}
	}
	return envelope, nil
}

func publicCallFailureFromInternal(internal map[string]any, elapsed time.Duration) map[string]any {
	container, _ := internal["result"].(map[string]any)
	message := "action failed"
	code := "ACTION_FAILED"
	if value, ok := container["error"]; ok {
		switch typed := value.(type) {
		case map[string]any:
			if candidate, ok := typed["code"].(string); ok && candidate != "" {
				code = candidate
			}
			if candidate, ok := typed["message"].(string); ok && candidate != "" {
				message = candidate
			}
		default:
			message = fmt.Sprint(typed)
		}
	}
	return publicCallFailure(code, message, elapsed)
}

func publicCallFailure(code, message string, elapsed time.Duration) map[string]any {
	if strings.TrimSpace(code) == "" {
		code = "CALL_FAILED"
	}
	if strings.TrimSpace(message) == "" {
		message = "call failed"
	}
	result := map[string]any{
		"ok":    false,
		"error": map[string]any{"code": code, "message": message},
	}
	result["metrics"] = publicCallMetrics(result, elapsed)
	return result
}

func publicCallMetrics(value any, elapsed time.Duration) map[string]any {
	data, err := json.Marshal(value)
	tokens := 0
	if err == nil {
		if counted, countErr := codeOutputCounter.CountText(data); countErr == nil {
			tokens = counted
		}
	}
	return map[string]any{"time": elapsed.Milliseconds(), "tokens": tokens}
}
