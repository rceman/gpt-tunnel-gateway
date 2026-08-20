package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

func (s *Server) genericBatch(ctx context.Context, legacy map[string]Tool, raw json.RawMessage) (any, error) {
	var input genericBatchInput
	if err := decode(raw, &input); err != nil {
		return nil, err
	}
	if input.SessionID == "" {
		return nil, fmt.Errorf("session is required")
	}
	if len(input.Calls) > genericBatchMaxItems {
		return nil, fmt.Errorf("calls exceeds maximum of %d", genericBatchMaxItems)
	}
	record := durableSession.Record{}
	if input.SessionID != "" {
		var err error
		record, err = s.activeSession(input.SessionID)
		if err != nil {
			return nil, err
		}
		ctx = withSession(ctx, record)
	}
	entries := s.genericActionRegistry(legacy)
	results := make([]map[string]any, 0, len(input.Calls))
	for _, call := range input.Calls {
		var item genericCallInput
		action := ""
		result, err := decodeBatchCall(call, &item)
		if err == nil {
			action = item.Action
		}
		if err == nil && item.SessionID != "" {
			result = nil
			err = fmt.Errorf("batch item session is not accepted; use the batch root session")
		}
		if err == nil {
			result, err = s.genericDispatch(ctx, entries, record, item.Action, item.Input)
		}
		if err != nil {
			var probe struct {
				Action string `json:"action"`
			}
			_ = json.Unmarshal(call, &probe)
			action = probe.Action
			result = genericActionError(probe.Action, err.Error())
		}
		results = append(results, genericBatchResult(action, result))
	}
	return map[string]any{"results": results}, nil
}
func decodeBatchCall(raw json.RawMessage, input *genericCallInput) (map[string]any, error) {
	if err := decode(raw, input); err != nil {
		return nil, err
	}
	return nil, nil
}
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
