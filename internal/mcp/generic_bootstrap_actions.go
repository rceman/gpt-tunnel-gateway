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
		if path == "session/info" {
			entry.LocalReadOnly = true
		}
		entries[path] = entry
	}
	add("rules/read", "Read and acknowledge the current rules for the bound project.", obj(map[string]any{}), true, s.rulesReadAction)
	operationInput := obj(map[string]any{"operation_id": str("Project-scoped local operation identifier.")}, "operation_id")
	operationExecution := obj(map[string]any{"operation_id": str("Project-scoped local operation identifier."), "project_id": str("Session-bound project.")}, "operation_id", "project_id")
	entries["operation/read"] = genericActionEntry{GenericAction: GenericAction{
		Path: "operation/read", Description: "Read one bounded local operation projection.", InputSchema: operationInput,
		OutputSchema: closedOutput(map[string]any{
			"schema_version": outputInteger(), "id": outputString(), "project_id": outputString(), "kind": outputString(), "status": outputString(),
			"correlation_id": outputString(), "entity_id": outputString(), "request_sha256": outputString(), "result": outputString(), "error": outputString(),
			"created_at": outputDateTime(), "updated_at": outputDateTime(),
		}, "schema_version", "id", "project_id", "kind", "status", "created_at", "updated_at"),
		Annotations: readOnlyAnnotations(), LocalReceiptOnly: true, SessionBound: true, SessionRequired: true, ExecutionInputSchema: operationExecution,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in service.LocalOperationReadInput
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			return s.Service.LocalOperationRead(ctx, in)
		},
	}}
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
	if tool, ok := legacy["project_workflow_policy_read"]; ok {
		add("workflow/rules", "Read workflow rules for a registered project.", tool.InputSchema, false, tool.Execute)
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
