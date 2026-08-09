package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

func addGenericTransportTools(add func(string, string, map[string]any, func(context.Context, json.RawMessage) (any, error)), s *Server, legacy map[string]Tool) {
	add("call", "Dispatch one server-owned action through its authoritative schema and handler.", genericCallInputSchema(), func(ctx context.Context, raw json.RawMessage) (any, error) {
		return s.genericCall(ctx, legacy, raw)
	})
	add("schema", "Discover generic action domains, action contracts, and the server-side schema revision.", genericSchemaInputSchema(), func(ctx context.Context, raw json.RawMessage) (any, error) {
		return s.genericSchema(legacy, raw)
	})
	add("batch", "Dispatch ordered generic actions and return one result for every item, including failures.", genericBatchInputSchema(), func(ctx context.Context, raw json.RawMessage) (any, error) {
		return s.genericBatch(ctx, legacy, raw)
	})
}

func (s *Server) genericCallWithEntries(ctx context.Context, entries map[string]genericActionEntry, raw json.RawMessage) (map[string]any, error) {
	var input genericCallInput
	if err := decode(raw, &input); err != nil {
		return nil, err
	}
	return s.genericDispatch(ctx, entries, durableSession.Record{}, input.Action, input.Input)
}

func (s *Server) genericDispatch(ctx context.Context, entries map[string]genericActionEntry, record durableSession.Record, action string, raw json.RawMessage) (map[string]any, error) {
	if action == "" {
		return nil, fmt.Errorf("action is required; inspect schema with path=\"\"")
	}
	entry, ok := entries[action]
	if !ok {
		return genericActionError(action, fmt.Sprintf("unknown action %q; inspect schema with path=\"\"", action)), nil
	}
	if record.ID != "" {
		if err := requireSessionRole(ctx, record.Role); err != nil {
			return genericActionError(action, err.Error()), nil
		}
		var err error
		raw, err = inheritSessionProject(entry.InputSchema, record.ProjectID, raw)
		if err != nil {
			return genericActionError(action, err.Error()), nil
		}
	}
	if entry.Authority != nil {
		if err := entry.Authority(ctx); err != nil {
			return genericActionError(action, err.Error()), nil
		}
	}
	if err := validateGenericActionInput(entry.InputSchema, raw); err != nil {
		return genericActionError(action, err.Error()+"; inspect schema with path=\""+action+"\""), nil
	}
	value, err := entry.Execute(ctx, raw)
	if err != nil {
		return genericActionError(action, err.Error()), nil
	}
	result := normalizeObject(value)
	if err := validateOutputValue(entry.OutputSchema, result); err != nil {
		return genericActionError(action, "action output contract violation: "+err.Error()), nil
	}
	return map[string]any{"action": action, "result": result, "is_error": false}, nil
}

func genericActionError(action, message string) map[string]any {
	return map[string]any{"action": action, "result": map[string]any{"error": message}, "is_error": true}
}

func validateGenericActionInput(schema map[string]any, raw json.RawMessage) error {
	if len(raw) == 0 || string(raw) == "null" {
		return fmt.Errorf("input must be an object")
	}
	var args map[string]json.RawMessage
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	if err := decoder.Decode(&args); err != nil || args == nil {
		return fmt.Errorf("input must be an object")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("input has trailing JSON content")
	}
	properties, _ := schema["properties"].(map[string]any)
	closed, _ := schema["additionalProperties"].(bool)
	if closed {
		for key := range args {
			if _, ok := properties[key]; !ok {
				return fmt.Errorf("unknown argument %q", key)
			}
		}
	}
	for _, required := range stringList(schema["required"]) {
		if _, ok := args[required]; !ok {
			return fmt.Errorf("missing required argument %q", required)
		}
	}
	return nil
}

func (s *Server) genericBatch(ctx context.Context, legacy map[string]Tool, raw json.RawMessage) (any, error) {
	var input genericBatchInput
	if err := decode(raw, &input); err != nil {
		return nil, err
	}
	if len(input.Calls) > genericBatchMaxItems {
		return nil, fmt.Errorf("calls exceeds maximum of %d", genericBatchMaxItems)
	}
	if input.SessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}
	record, err := s.activeSession(input.SessionID)
	if err != nil {
		return nil, err
	}
	ctx = withSession(ctx, record)
	entries := s.genericActionRegistry(legacy)
	results := make([]map[string]any, 0, len(input.Calls))
	for _, call := range input.Calls {
		var item genericCallInput
		result, err := decodeBatchCall(call, &item)
		if err == nil && item.SessionID != "" && item.SessionID != input.SessionID {
			result = nil
			err = fmt.Errorf("batch item session_id does not match batch session_id")
		}
		if err == nil {
			result, err = s.genericDispatch(ctx, entries, record, item.Action, item.Input)
		}
		if err != nil {
			var probe struct {
				Action string `json:"action"`
			}
			_ = json.Unmarshal(call, &probe)
			result = genericActionError(probe.Action, err.Error())
		}
		results = append(results, result)
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
	properties, _ := schema["properties"].(map[string]any)
	if _, ok := properties["project_id"]; !ok {
		return raw, nil
	}
	var args map[string]json.RawMessage
	if err := json.Unmarshal(raw, &args); err != nil || args == nil {
		return nil, fmt.Errorf("input must be an object")
	}
	if value, ok := args["project_id"]; ok {
		var supplied string
		if err := json.Unmarshal(value, &supplied); err != nil || supplied != projectID {
			return nil, fmt.Errorf("project_id does not match session project")
		}
		return raw, nil
	}
	required := false
	for _, key := range stringList(schema["required"]) {
		if key == "project_id" {
			required = true
			break
		}
	}
	if !required {
		return raw, nil
	}
	args["project_id"], _ = json.Marshal(projectID)
	return json.Marshal(args)
}
