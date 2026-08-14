package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/controller"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

var mcp7ProjectActions = map[string]string{
	"list":            "project/list",
	"read":            "project/read",
	"onboard":         "project/onboard",
	"onboard_status":  "project/onboard_status",
	"onboard_recover": "project/onboard_recover",
	"remove":          "project/remove",
}

func mcp7ActionEnvelopeSchema(actions ...string) map[string]any {
	action := str("Sessionless bootstrap action.")
	values := make([]any, 0, len(actions))
	for _, value := range actions {
		values = append(values, value)
	}
	action["enum"] = values
	schema := obj(map[string]any{
		"action": action,
		"input":  map[string]any{"type": "object", "additionalProperties": true, "description": "Action-specific input validated by the server-owned contract."},
	}, "action", "input")
	schema["x-sessionless"] = true
	return schema
}

func addMCP7BootstrapTools(add func(string, string, map[string]any, func(context.Context, json.RawMessage) (any, error)), s *Server, legacy map[string]Tool) {
	statusSchema := obj(map[string]any{"session_id": str("Optional active durable session; binds project status to that session's project.")})
	statusSchema["x-sessionless"] = true
	add("status", "Return gateway health, with optional status for an active session's bound project.", statusSchema, func(ctx context.Context, raw json.RawMessage) (any, error) {
		tool, ok := legacy["system_ping"]
		if !ok {
			return nil, fmt.Errorf("system ping handler is unavailable")
		}
		baseValue, err := tool.Execute(ctx, []byte(`{}`))
		if err != nil {
			return nil, err
		}
		base, ok := baseValue.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("system status handler returned an invalid object")
		}
		runtime := controller.Controller{Config: s.Service.Config, ConfigPath: s.Service.ConfigPath}.RuntimeIdentity(ctx)
		base["runtime_identity"] = runtime
		if runtime.RunningVersion != "" {
			base["version"] = runtime.RunningVersion
		} else if runtime.InstalledVersion != "" {
			base["version"] = runtime.InstalledVersion
		}
		var input struct {
			SessionID string `json:"session_id"`
		}
		if err := decode(raw, &input); err != nil {
			return nil, err
		}
		if input.SessionID == "" {
			return base, nil
		}
		session, err := s.activeSession(input.SessionID)
		if err != nil {
			return nil, fmt.Errorf("status session is invalid: %w", err)
		}
		projectStatus, err := s.Service.ProjectStatus(ctx, session.ProjectID)
		if err != nil {
			return nil, err
		}
		base["project_status"] = projectStatus
		return base, nil
	})
	rulesSchema := obj(map[string]any{
		"project_id": str("Registered project identifier."),
	}, "project_id")
	rulesSchema["x-sessionless"] = true
	add("rules", "Read the durable project workflow rules during sessionless bootstrap.", rulesSchema, func(ctx context.Context, raw json.RawMessage) (any, error) {
		tool, ok := legacy["project_workflow_policy_read"]
		if !ok {
			return nil, fmt.Errorf("workflow rules handler is unavailable")
		}
		return tool.Execute(ctx, raw)
	})
	add("project", "Run the bounded sessionless project bootstrap actions.", mcp7ActionEnvelopeSchema("list", "read", "onboard", "onboard_status", "onboard_recover", "remove"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		var input struct {
			Action string          `json:"action"`
			Input  json.RawMessage `json:"input"`
		}
		if err := decode(raw, &input); err != nil {
			return nil, err
		}
		path, ok := mcp7ProjectActions[input.Action]
		if !ok {
			return nil, fmt.Errorf("unknown project bootstrap action %q", input.Action)
		}
		entries := s.genericActionRegistry(legacy)
		return s.genericDispatch(ctx, entries, durableSession.Record{}, path, input.Input)
	})
}
