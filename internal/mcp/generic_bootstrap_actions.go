package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/controller"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

type sessionUpdateActionInput struct {
	Label *string `json:"label"`
	Ref   *string `json:"ref"`
}

func (s *Server) addBootstrapActions(entries map[string]genericActionEntry, legacy map[string]Tool) {
	add := func(path, description string, schema map[string]any, required bool, execute func(context.Context, json.RawMessage) (any, error)) {
		if _, exists := entries[path]; exists {
			return
		}
		entry := genericActionEntry{GenericAction: GenericAction{
			Path:                 path,
			Description:          description,
			InputSchema:          schema,
			OutputSchema:         map[string]any{"type": "object", "additionalProperties": true},
			SessionBound:         required,
			SessionRequired:      required,
			ExecutionInputSchema: schema,
			Execute:              execute,
		}}
		if path == "project/status" {
			entry.OutputSchema = projectOperationalStatusOutputSchema()
		}
		if path == "operation/read" {
			entry.OutputSchema = operationReadOutputSchema()
		}
		if path == "session/info" {
			entry.LocalReadOnly = true
		}
		if path == "operation/read" {
			entry.LocalReadOnly = true
		}
		entries[path] = entry
	}
	add("rules/read", "Read and acknowledge the current rules for the bound project.", obj(map[string]any{}), true, s.rulesReadAction)
	add("workflow/rules", "Read and acknowledge the current rules for the bound project.", obj(map[string]any{}), true, s.rulesReadAction)
	add("project/status", "Read the compact operational status of the project bound to this Session.", obj(map[string]any{}), true, func(ctx context.Context, raw json.RawMessage) (any, error) {
		return s.Service.ProjectOperationalStatus(ctx)
	})
	add("session/list", "List active durable sessions.", obj(map[string]any{}), false, func(ctx context.Context, raw json.RawMessage) (any, error) {
		return s.Service.SessionList()
	})
	add("session/info", "Read the durable session bound to the public session.", obj(map[string]any{}), true, func(ctx context.Context, raw json.RawMessage) (any, error) {
		return s.sessionActionForContext(ctx, "info", nil)
	})
	add("session/end", "End the durable session bound to the public session.", obj(map[string]any{}), true, func(ctx context.Context, raw json.RawMessage) (any, error) {
		return s.sessionActionForContext(ctx, "end", nil)
	})
	add("operation/read", "Read one authorized durable asynchronous mutation receipt.", obj(map[string]any{
		"operation_id": str("Durable operation identifier."),
	}, "operation_id"), true, func(ctx context.Context, raw json.RawMessage) (any, error) {
		var input struct {
			OperationID string `json:"operation_id"`
		}
		if err := decode(raw, &input); err != nil {
			return nil, err
		}
		return s.Service.OperationRead(ctx, input.OperationID)
	})
	add("session/update", "Update the label or caller reference of the durable session bound to the public session.", obj(map[string]any{
		"label": str("Optional bounded session label."),
		"ref":   str("Optional caller reference."),
	}), true, func(ctx context.Context, raw json.RawMessage) (any, error) {
		var input sessionUpdateActionInput
		if err := decode(raw, &input); err != nil {
			return nil, err
		}
		return s.sessionActionForContext(ctx, "update", &input)
	})
	if tool, ok := legacy["system_ping"]; ok {
		add("gateway/status", "Read Gateway health and runtime status.", obj(map[string]any{}), false, func(ctx context.Context, raw json.RawMessage) (any, error) {
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
			sessionID := service.AgentSessionID(ctx)
			if sessionID == "" {
				return base, nil
			}
			session, err := s.activeSession(sessionID)
			if err != nil {
				return nil, fmt.Errorf("status session is invalid: %w", err)
			}
			if session.ProjectID == "" {
				return base, nil
			}
			projectStatus, err := s.Service.ProjectStatus(ctx, session.ProjectID)
			if err != nil {
				return nil, err
			}
			base["project_status"] = projectStatus
			return base, nil
		})
	}
}

func (s *Server) sessionActionForContext(ctx context.Context, action string, input any) (any, error) {
	id := service.AgentSessionID(ctx)
	if id == "" {
		return nil, fmt.Errorf("session is unavailable")
	}
	info, err := s.Service.SessionInfo(ctx, id)
	if err != nil {
		return nil, err
	}
	roleCtx, err := existingSessionRoleContext(ctx, info.Session.Role)
	if err != nil {
		return nil, err
	}
	switch action {
	case "info":
		return info, nil
	case "end":
		return s.Service.SessionEnd(roleCtx, id)
	case "update":
		v := input.(*sessionUpdateActionInput)
		return s.Service.SessionUpdate(roleCtx, service.SessionUpdateInput{SessionID: id, Label: v.Label, SessionRef: v.Ref})
	default:
		return nil, fmt.Errorf("unsupported session action %q", action)
	}
}
