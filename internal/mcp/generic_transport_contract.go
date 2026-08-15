package mcp

import (
	"context"
	"encoding/json"
	"fmt"
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
	Path                   string
	Description            string
	InputSchema            map[string]any
	OutputSchema           map[string]any
	Annotations            ToolAnnotations
	Authority              func(context.Context) error
	AuthorityRole          string
	RequiresWorkflowPolicy bool
	LocalReceiptOnly       bool
	LocalReadOnly          bool
	AllowLegacyOverride    bool
	SessionBound           bool
	SessionRequired        bool
	ExecutionInputSchema   map[string]any
	Execute                func(context.Context, json.RawMessage) (any, error)
}

type genericActionEntry struct {
	GenericAction
	LegacyTool                string
	LegacyInputSchema         map[string]any
	LegacyOutputSchema        map[string]any
	LegacyExecute             func(context.Context, json.RawMessage) (any, error)
	RouteLegacyByProjectModel bool
}

type genericCallInput struct {
	SessionID string          `json:"session"`
	Action    string          `json:"action"`
	Input     json.RawMessage `json:"input"`
}

type genericBatchInput struct {
	SessionID string            `json:"session"`
	Calls     []json.RawMessage `json:"calls"`
}

func (s *Server) RegisterGenericAction(action GenericAction) error {
	if !validGenericActionPath(action.Path) {
		return fmt.Errorf("invalid generic action path %q", action.Path)
	}
	if strings.HasPrefix(action.Path, "plan/") {
		return fmt.Errorf("plan actions are retired from the canonical action registry")
	}
	if action.Description == "" || action.InputSchema == nil || action.OutputSchema == nil || action.Execute == nil {
		return fmt.Errorf("generic action %q is incomplete", action.Path)
	}
	if err := validateActionAuthorityRole(action.AuthorityRole); err != nil {
		return fmt.Errorf("generic action %q: %w", action.Path, err)
	}
	for toolName := range toolOutputSchemas {
		if legacyActionPath(toolName) == action.Path && !action.AllowLegacyOverride {
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
		if toolName == "system_ping" || toolName == "session" || toolName == "status" || toolName == "rules" || toolName == "project" || toolName == "project_status" || toolName == "agent_send" || isRetiredPlanAction(toolName) {
			continue
		}
		path := legacyActionPath(toolName)
		toolName, tool := toolName, tool
		contract := actionAuthorityContractFor(toolName)
		entry := genericActionEntry{
			GenericAction: GenericAction{
				Path:                   path,
				Description:            tool.Description,
				InputSchema:            tool.InputSchema,
				OutputSchema:           tool.OutputSchema,
				Annotations:            tool.Annotations,
				AuthorityRole:          contract.Role,
				RequiresWorkflowPolicy: contract.RequiresWorkflowPolicy,
				LocalReadOnly:          toolName == "agent_tail" || strings.HasPrefix(path, "git/"),
				Authority: func(ctx context.Context) error {
					return requireToolAuthority(ctx, toolName)
				},
				Execute:              tool.Execute,
				ExecutionInputSchema: tool.InputSchema,
			},
			LegacyTool: toolName,
		}
		// The project-level tail keeps skip for typed compatibility, while the
		// canonical registry exposes only the cursor contract. Both paths call
		// the same service AgentTailPage implementation.
		if toolName == "agent_tail" {
			entry.InputSchema = agentTailSessionInputSchema()
			entry.ExecutionInputSchema = agentTailExecutionInputSchema()
			entry.Execute = func(ctx context.Context, raw json.RawMessage) (any, error) {
				return s.agentTailAction(ctx, raw)
			}
		}
		entries[path] = entry
	}
	s.genericActionMu.RLock()
	defer s.genericActionMu.RUnlock()
	for path, action := range s.genericActions {
		if strings.HasPrefix(path, "plan/") {
			continue
		}
		entry := genericActionEntry{GenericAction: action}
		if entry.ExecutionInputSchema == nil {
			entry.ExecutionInputSchema = action.InputSchema
		}
		if path == "task/create" {
			if legacy, ok := legacy["task_create"]; ok {
				entry.LegacyTool = "task_create"
				entry.LegacyInputSchema = legacy.InputSchema
				entry.LegacyOutputSchema = legacy.OutputSchema
				entry.LegacyExecute = legacy.Execute
				entry.RouteLegacyByProjectModel = true
			}
		}
		entries[path] = entry
	}
	if _, exists := entries["query/run"]; !exists {
		entries["query/run"] = queryGenericAction(s)
	}
	for path, entry := range entries {
		if path == "session/bind" {
			entry.SessionBound = true
			entry.SessionRequired = true
		} else if !sessionlessActionPath(path) && !strings.HasPrefix(path, "runtime/") && (sessionBoundActionPath(path) || schemaHasProperty(entry.InputSchema, "project_id")) {
			entry.SessionBound = true
			entry.InputSchema = withoutProjectID(entry.InputSchema)
			if entry.LegacyInputSchema != nil {
				entry.LegacyInputSchema = entry.ExecutionInputSchema
			}
			entries[path] = entry
		}
		entry.SessionRequired = entry.SessionBound
		entries[path] = entry
	}
	s.addBootstrapActions(entries, legacy)
	return entries
}

func isRetiredPlanAction(toolName string) bool {
	return strings.HasPrefix(toolName, "plan_")
}

func sessionBoundActionPath(path string) bool {
	for _, prefix := range []string{"adr/", "agent/", "git/", "operator/", "plan/", "task/", "train/", "watcher/"} {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

func schemaHasProperty(schema map[string]any, name string) bool {
	if schema == nil {
		return false
	}
	if properties, ok := schema["properties"].(map[string]any); ok {
		if _, exists := properties[name]; exists {
			return true
		}
		for _, child := range properties {
			if nested, ok := child.(map[string]any); ok && schemaHasProperty(nested, name) {
				return true
			}
		}
	}
	if branches, ok := schema["oneOf"].([]any); ok {
		for _, branch := range branches {
			if nested, ok := branch.(map[string]any); ok && schemaHasProperty(nested, name) {
				return true
			}
		}
	}
	return false
}

func withoutProjectID(schema map[string]any) map[string]any {
	if schema == nil {
		return nil
	}
	result := make(map[string]any, len(schema))
	for key, value := range schema {
		if nested, ok := value.(map[string]any); ok {
			result[key] = withoutProjectID(nested)
		} else if branches, ok := value.([]any); ok {
			copied := make([]any, len(branches))
			for i, branch := range branches {
				if nested, ok := branch.(map[string]any); ok {
					copied[i] = withoutProjectID(nested)
				} else {
					copied[i] = branch
				}
			}
			result[key] = copied
		} else {
			result[key] = value
		}
	}
	if properties, ok := result["properties"].(map[string]any); ok {
		filtered := make(map[string]any, len(properties))
		for property, value := range properties {
			if property != "project_id" {
				filtered[property] = value
			}
		}
		result["properties"] = filtered
	}
	result["required"] = removeRequiredKey(result["required"], "project_id")
	return result
}

func removeRequiredKey(value any, remove string) []string {
	result := []string{}
	for _, key := range stringList(value) {
		if key != remove {
			result = append(result, key)
		}
	}
	return result
}

func genericCallInputSchema() map[string]any {
	schema := obj(map[string]any{
		"session": str("Optional durable project-bound session identifier."),
		"action":  str("Server-owned action path; inspect schema for available actions."),
		"input":   map[string]any{"type": "object", "additionalProperties": true, "description": "Generic action input validated by the server-owned action contract."},
	}, "action", "input")
	schema["x-sessionless"] = true
	schema["required"] = []string{"session", "action", "input"}
	return schema
}

func sessionStartInputSchema() map[string]any {
	return obj(map[string]any{
		"project_id": str("Registered project identifier."),
		"role":       str("Server-authorized session role."),
		"label":      str("Optional bounded session label."),
		"ref":        str("Optional caller reference."),
	}, "project_id", "role")
}

func genericActionParts(path string) (string, string, bool) {
	if parts := strings.Split(path, "/"); len(parts) == 2 && parts[0] != "" && parts[1] != "" {
		return parts[0], parts[1], true
	}
	return "", "", false
}

func genericBatchInputSchema() map[string]any {
	calls := array(genericBatchCallInputSchema())
	calls["maxItems"] = genericBatchMaxItems
	schema := obj(map[string]any{"session": str("Optional durable session shared by every batch item."), "calls": calls}, "calls")
	schema["x-sessionless"] = true
	schema["required"] = []string{"session", "calls"}
	return schema
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
	return obj(map[string]any{"path": str("Empty for the root index, a domain for its actions, or an exact action path.")})
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
	}, "path", "domain", "name", "description", "input_schema", "output_schema", "annotations", "session_required")
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
