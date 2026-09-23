package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/controller"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func (s *Server) addBootstrapActions(entries map[string]genericActionEntry, legacy map[string]Tool) {
	add := func(path string, required bool, execute func(context.Context, json.RawMessage) (any, error)) {
		if _, exists := entries[path]; exists {
			return
		}
		action := GenericAction{
			Path:            path,
			SessionBound:    required,
			SessionRequired: required,
			Execute:         execute,
		}
		if path == "session/info" || path == "operation/read" || path == "operation/await" {
			action.LocalReadOnly = true
		}
		entries[path] = genericActionEntry{GenericAction: action}
	}
	add("project/status", true, func(ctx context.Context, raw json.RawMessage) (any, error) {
		return s.Service.ProjectOperationalStatus(ctx)
	})
	add("session/list", false, func(ctx context.Context, raw json.RawMessage) (any, error) {
		result, err := s.Service.SessionList(ctx)
		if err != nil {
			return nil, err
		}
		return publicSessionResult(result), nil
	})
	add("session/info", true, func(ctx context.Context, raw json.RawMessage) (any, error) {
		return s.sessionActionForContext(ctx, "info")
	})
	add("session/end", true, func(ctx context.Context, raw json.RawMessage) (any, error) {
		return s.sessionActionForContext(ctx, "end")
	})
	add("operation/read", true, func(ctx context.Context, raw json.RawMessage) (any, error) {
		var input struct {
			OperationID string `json:"operation_id"`
		}
		if err := decode(raw, &input); err != nil {
			return nil, err
		}
		return s.Service.OperationRead(ctx, input.OperationID)
	})
	add("operation/await", true, func(ctx context.Context, raw json.RawMessage) (any, error) {
		var input struct {
			OperationID string `json:"operation_id"`
			Seconds     int    `json:"seconds,omitempty"`
		}
		if err := decode(raw, &input); err != nil {
			return nil, err
		}
		return s.Service.OperationAwait(ctx, input.OperationID, time.Duration(input.Seconds)*time.Second)
	})
	if tool, ok := legacy["system_ping"]; ok {
		add("gateway/status", false, func(ctx context.Context, raw json.RawMessage) (any, error) {
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
			projectStatus, err := s.Service.ProjectOperationalStatus(ctx)
			if err != nil {
				return nil, err
			}
			base["project_status"] = projectStatus
			return base, nil
		})
	}
}

func (s *Server) sessionActionForContext(ctx context.Context, action string) (any, error) {
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
		return publicSessionResult(info), nil
	case "end":
		result, err := s.Service.SessionEnd(roleCtx, id)
		if err != nil {
			return nil, err
		}
		return publicSessionResult(result), nil
	default:
		return nil, fmt.Errorf("unsupported session action %q", action)
	}
}
