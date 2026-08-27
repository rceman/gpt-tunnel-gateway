package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
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
	if strings.HasSuffix(action.Path, "_status") {
		return fmt.Errorf("generic action %q uses retired *_status receipt path; use operation/read", action.Path)
	}
	if strings.HasPrefix(action.Path, "project/") && action.Path != "project/status" {
		return fmt.Errorf("generic action %q is not part of the active project action surface", action.Path)
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
		if toolName == "system_ping" || toolName == "session" || toolName == "status" || toolName == "rules" || toolName == "project" || toolName == "project_list" || toolName == "project_status" || toolName == "agent_send" || isRetiredPlanAction(toolName) {
			continue
		}
		path := legacyActionPath(toolName)
		if toolName == "git_worktree_status" {
			path = "git/worktree_status"
		}
		if strings.HasPrefix(path, "project/") && path != "project/status" {
			continue
		}
		if strings.HasSuffix(path, "_status") && toolName != "git_worktree_status" {
			panic(fmt.Sprintf("legacy action %q uses retired *_status receipt path; remove the registration", path))
		}
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
		if strings.HasPrefix(path, "project/") && path != "project/status" {
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
		if projectionDetailAction(path) {
			entry.InputSchema = withProjectionDetail(entry.InputSchema)
			entry.ExecutionInputSchema = withProjectionDetail(entry.ExecutionInputSchema)
		}
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
