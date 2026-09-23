package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/actioncontract"
)

const genericSchemaRevision = "generic-mcp-v2"

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
	SessionBound           bool
	SessionRequired        bool
	InjectSessionProjectID bool
	ExecutionInputSchema   map[string]any
	Execute                func(context.Context, json.RawMessage) (any, error)
}
type genericActionEntry struct {
	GenericAction
	Contract actioncontract.CompiledAction
}
type genericCallInput struct {
	SessionID string          `json:"session"`
	Action    string          `json:"action"`
	Input     json.RawMessage `json:"input"`
}

func (s *Server) RegisterGenericAction(action GenericAction) error {
	if !validGenericActionPath(action.Path) {
		return fmt.Errorf("invalid generic action path %q", action.Path)
	}
	if strings.HasPrefix(action.Path, "git/") {
		return fmt.Errorf("generic action %q is not part of the active MCP action surface", action.Path)
	}
	if strings.HasSuffix(action.Path, "_status") {
		return fmt.Errorf("generic action %q uses retired *_status receipt path; use operation/read", action.Path)
	}
	if strings.HasPrefix(action.Path, "project/") && action.Path != "project/status" && action.Path != "project/guide_bind" {
		return fmt.Errorf("generic action %q is not part of the active project action surface", action.Path)
	}
	if strings.HasPrefix(action.Path, "plan/") {
		return fmt.Errorf("plan actions are retired from the canonical action registry")
	}
	if _, exists := s.actionContractSet().Action(action.Path); !exists {
		return fmt.Errorf("generic action %q has no canonical action contract", action.Path)
	}
	if action.Execute == nil {
		return fmt.Errorf("generic action %q has no handler", action.Path)
	}
	if err := validateActionAuthorityRole(action.AuthorityRole); err != nil {
		return fmt.Errorf("generic action %q: %w", action.Path, err)
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
func sessionProjectInjectionActionPath(path string) bool {
	for _, prefix := range []string{"adr/", "milestone/", "relation/", "rule/", "task/", "track/"} {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return path == "project/guide_bind" || path == "agent/guide"
}

func validGenericActionPath(path string) bool {
	parts := strings.Split(path, "/")
	if len(parts) < 2 || len(parts) > 3 || parts[0] == "" || parts[1] == "" || (len(parts) == 3 && parts[0] != "admin") {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
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
		if toolName == "call" || toolName == "schema" || toolName == "system_ping" || toolName == "session" || toolName == "status" || toolName == "guide" || toolName == "projects" || toolName == "project" || toolName == "project_list" || toolName == "project_status" || isRetiredPlanAction(toolName) {
			continue
		}
		path := legacyActionPath(toolName)
		if toolName == "git_worktree_status" {
			path = "git/worktree_status"
		}
		if strings.HasPrefix(path, "git/") {
			continue
		}
		if strings.HasPrefix(path, "project/") && path != "project/status" && path != "project/guide_bind" {
			continue
		}
		if strings.HasSuffix(path, "_status") && toolName != "git_worktree_status" {
			panic(fmt.Sprintf("legacy action %q uses retired *_status receipt path; remove the registration", path))
		}
		toolName, tool := toolName, tool
		contract := actionAuthorityContractFor(toolName)
		if _, exists := entries[path]; exists {
			panic(fmt.Sprintf("multiple legacy handlers register action %q", path))
		}
		entry := genericActionEntry{
			GenericAction: GenericAction{
				Path:                   path,
				Description:            tool.Description,
				InputSchema:            tool.InputSchema,
				OutputSchema:           tool.OutputSchema,
				Annotations:            tool.Annotations,
				AuthorityRole:          contract.Role,
				RequiresWorkflowPolicy: contract.RequiresWorkflowPolicy,
				LocalReadOnly:          strings.HasPrefix(path, "git/"),
				Authority: func(ctx context.Context) error {
					return requireToolAuthority(ctx, toolName)
				},
				Execute: tool.Execute,
			},
		}
		entries[path] = entry
	}
	s.genericActionMu.RLock()
	defer s.genericActionMu.RUnlock()
	for path, action := range s.genericActions {
		if _, exists := entries[path]; exists {
			panic(fmt.Sprintf("generic action %q duplicates a registered legacy handler", path))
		}
		if strings.HasPrefix(path, "git/") {
			continue
		}
		if strings.HasPrefix(path, "plan/") {
			continue
		}
		if strings.HasPrefix(path, "project/") && path != "project/status" && path != "project/guide_bind" {
			continue
		}
		entry := genericActionEntry{GenericAction: action}
		entries[path] = entry
	}
	for path, entry := range entries {
		if !sessionlessActionPath(path) && !strings.HasPrefix(path, "runtime/") && sessionBoundActionPath(path) {
			entry.SessionBound = true
		}
		entry.InjectSessionProjectID = sessionProjectInjectionActionPath(path)
		entry.SessionRequired = entry.SessionRequired || entry.SessionBound
		entries[path] = entry
	}
	s.addBootstrapActions(entries, legacy)
	s.applyActionContracts(entries)
	return entries
}
