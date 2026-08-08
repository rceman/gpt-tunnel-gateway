package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

const genericSchemaRevision = "generic-mcp-v1"
const genericBatchMaxItems = 100

// GenericAction is a server-owned action registration. It is intentionally
// not exposed through MCP; the stable MCP transport discovers registrations
// through schema and invokes them through the same handler path as legacy
// tools.
type GenericAction struct {
	Path         string
	Description  string
	InputSchema  map[string]any
	OutputSchema map[string]any
	Annotations  ToolAnnotations
	Authority    func(context.Context) error
	Execute      func(context.Context, json.RawMessage) (any, error)
}

type genericActionEntry struct {
	GenericAction
	LegacyTool string
}

type genericCallInput struct {
	SessionID string          `json:"session_id"`
	Action    string          `json:"action"`
	Input     json.RawMessage `json:"input"`
}

type genericBatchInput struct {
	SessionID string            `json:"session_id"`
	Calls     []json.RawMessage `json:"calls"`
}

func (s *Server) RegisterGenericAction(action GenericAction) error {
	if !validGenericActionPath(action.Path) {
		return fmt.Errorf("invalid generic action path %q", action.Path)
	}
	if action.Description == "" || action.InputSchema == nil || action.OutputSchema == nil || action.Execute == nil {
		return fmt.Errorf("generic action %q is incomplete", action.Path)
	}
	for toolName := range toolOutputSchemas {
		if legacyActionPath(toolName) == action.Path {
			return fmt.Errorf("generic action %q conflicts with legacy tool %q", action.Path, toolName)
		}
	}
	s.genericActionMu.Lock()
	defer s.genericActionMu.Unlock()
	if s.genericActions == nil {
		s.genericActions = map[string]GenericAction{}
	}
	if _, exists := s.genericActions[action.Path]; exists {
		return fmt.Errorf("generic action %q already registered", action.Path)
	}
	s.genericActions[action.Path] = action
	return nil
}

func validGenericActionPath(path string) bool {
	parts := strings.Split(path, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return false
	}
	for _, part := range parts {
		for i, r := range part {
			if i == 0 {
				if r < 'a' || r > 'z' {
					return false
				}
				continue
			}
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
				continue
			}
			return false
		}
	}
	return true
}

func legacyActionPath(toolName string) string {
	if index := strings.IndexByte(toolName, '_'); index > 0 && index+1 < len(toolName) {
		return toolName[:index] + "/" + toolName[index+1:]
	}
	return "system/" + toolName
}

func (s *Server) genericActionRegistry(legacy map[string]Tool) map[string]genericActionEntry {
	entries := make(map[string]genericActionEntry, len(legacy))
	for toolName, tool := range legacy {
		if toolName == "system_ping" || toolName == "session" {
			continue
		}
		path := legacyActionPath(toolName)
		toolName, tool := toolName, tool
		entries[path] = genericActionEntry{
			GenericAction: GenericAction{
				Path:         path,
				Description:  tool.Description,
				InputSchema:  tool.InputSchema,
				OutputSchema: tool.OutputSchema,
				Annotations:  tool.Annotations,
				Authority: func(ctx context.Context) error {
					return requireToolAuthority(ctx, toolName)
				},
				Execute: tool.Execute,
			},
			LegacyTool: toolName,
		}
	}
	s.genericActionMu.RLock()
	defer s.genericActionMu.RUnlock()
	for path, action := range s.genericActions {
		entries[path] = genericActionEntry{GenericAction: action}
	}
	return entries
}

func genericCallInputSchema() map[string]any {
	return obj(map[string]any{
		"session_id": str("Explicit durable project-bound session identifier."),
		"action":     str("Server-owned action path; inspect schema for available actions."),
		"input":      map[string]any{"type": "object", "additionalProperties": true, "description": "Generic action input validated by the server-owned action contract."},
	}, "session_id", "action", "input")
}

func genericBatchInputSchema() map[string]any {
	calls := array(genericBatchCallInputSchema())
	calls["maxItems"] = genericBatchMaxItems
	return obj(map[string]any{"session_id": str("One explicit durable session shared by every batch item."), "calls": calls}, "session_id", "calls")
}

func genericBatchCallInputSchema() map[string]any {
	return obj(map[string]any{
		"action": str("Server-owned action path; inspect schema for available actions."),
		"input":  map[string]any{"type": "object", "additionalProperties": true, "description": "Generic action input validated by the server-owned action contract."},
	}, "action", "input")
}

func genericSchemaInputSchema() map[string]any {
	return obj(map[string]any{"path": str("Empty for the root index, a domain for its actions, or an exact action path.")})
}

func genericCallOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"action":   outputString(),
		"result":   map[string]any{"type": "object", "additionalProperties": true},
		"is_error": outputBoolean(),
	}, "action", "result", "is_error")
}

func genericBatchOutputSchema() map[string]any {
	return closedOutput(map[string]any{"results": outputArray(genericCallOutputSchema())}, "results")
}

func genericSchemaOutputSchema() map[string]any {
	action := closedOutput(map[string]any{
		"path": outputString(), "domain": outputString(), "name": outputString(),
		"description": outputString(), "input_schema": map[string]any{"type": "object", "additionalProperties": true},
		"output_schema": map[string]any{"type": "object", "additionalProperties": true}, "annotations": map[string]any{"type": "object", "additionalProperties": true},
	}, "path", "domain", "name", "description", "input_schema", "output_schema", "annotations")
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
		return nil, fmt.Errorf("session_id is required")
	}
	record, err := s.activeSession(input.SessionID)
	if err != nil {
		return nil, err
	}
	ctx = withSession(ctx, record)
	return s.genericDispatch(ctx, s.genericActionRegistry(legacy), record, input.Action, input.Input)
}

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

func (s *Server) genericSchema(legacy map[string]Tool, raw json.RawMessage) (any, error) {
	var input struct {
		Path string `json:"path"`
	}
	if err := decode(raw, &input); err != nil {
		return nil, err
	}
	entries := s.genericActionRegistry(legacy)
	result := map[string]any{
		"revision": genericSchemaRevision, "path": input.Path, "kind": "root",
		"domains": []string{}, "actions": []map[string]any{}, "contract": map[string]any{},
	}
	if input.Path == "" {
		domains := map[string]bool{}
		for path := range entries {
			domains[strings.SplitN(path, "/", 2)[0]] = true
		}
		result["domains"] = sortedKeys(domains)
		return result, nil
	}
	if entry, ok := entries[input.Path]; ok {
		result["kind"] = "action"
		result["contract"] = genericActionContract(entry)
		return result, nil
	}
	actions := make([]map[string]any, 0)
	for path, entry := range entries {
		parts := strings.SplitN(path, "/", 2)
		if len(parts) == 2 && parts[0] == input.Path {
			actions = append(actions, genericActionSummary(path, entry))
		}
	}
	if len(actions) == 0 {
		return nil, fmt.Errorf("schema path %q not found; inspect schema with path=\"\"", input.Path)
	}
	sort.Slice(actions, func(i, j int) bool { return actions[i]["path"].(string) < actions[j]["path"].(string) })
	result["kind"] = "domain"
	result["actions"] = actions
	return result, nil
}

func genericActionContract(entry genericActionEntry) map[string]any {
	return genericActionSummary(entry.Path, entry)
}

func genericActionSummary(path string, entry genericActionEntry) map[string]any {
	parts := strings.SplitN(path, "/", 2)
	return map[string]any{
		"path": path, "domain": parts[0], "name": parts[1], "description": entry.Description,
		"input_schema": entry.InputSchema, "output_schema": entry.OutputSchema, "annotations": entry.Annotations,
	}
}

func sortedKeys(values map[string]bool) []string {
	result := make([]string, 0, len(values))
	for key := range values {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}
