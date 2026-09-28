package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

func (s *Server) ensureConfigurationActions() {
	if s.Service == nil {
		return
	}
	s.configurationActions.Do(func() {
		s.configurationActionErr = s.registerConfigurationActions()
	})
	if s.configurationActionErr != nil {
		panic(s.configurationActionErr)
	}
}

func (s *Server) registerConfigurationActions() error {
	register := func(path, description string, readOnly bool, execute func(context.Context, json.RawMessage) (any, error)) error {
		return s.RegisterGenericAction(GenericAction{
			Path:         path,
			Description:  description,
			InputSchema:  map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false},
			OutputSchema: map[string]any{"type": "object", "additionalProperties": true},
			Annotations: ToolAnnotations{
				ReadOnlyHint:   readOnly,
				IdempotentHint: readOnly,
			},
			AuthorityRole:   configActionRole(readOnly),
			SessionBound:    true,
			SessionRequired: true,
			LocalReadOnly:   readOnly,
			Execute:         execute,
		})
	}
	if err := register("config/procedure_list", "List configured project Procedures.", true, func(ctx context.Context, _ json.RawMessage) (any, error) {
		projectID, err := s.boundConfigurationProject(ctx)
		if err != nil {
			return nil, err
		}
		return s.Service.ConfigProcedureList(ctx, projectID)
	}); err != nil {
		return err
	}
	if err := register("config/procedure_read", "Read one configured project Procedure including its script and schemas.", true, func(ctx context.Context, raw json.RawMessage) (any, error) {
		var input struct {
			Name string `json:"name"`
		}
		if err := decode(raw, &input); err != nil {
			return nil, err
		}
		projectID, err := s.boundConfigurationProject(ctx)
		if err != nil {
			return nil, err
		}
		return s.Service.ConfigProcedureRead(ctx, projectID, input.Name)
	}); err != nil {
		return err
	}
	if err := register("config/procedure_create", "Create one project Procedure.", false, func(ctx context.Context, raw json.RawMessage) (any, error) {
		var input struct {
			Name       string                           `json:"name"`
			Definition model.ProjectProcedureDefinition `json:"definition"`
			Reason     string                           `json:"reason"`
		}
		if err := decode(raw, &input); err != nil {
			return nil, err
		}
		projectID, err := s.boundConfigurationProject(ctx)
		if err != nil {
			return nil, err
		}
		return s.Service.ConfigProcedureCreate(ctx, service.ConfigProcedureCreateInput{ProjectID: projectID, Name: input.Name, Definition: input.Definition, Reason: input.Reason})
	}); err != nil {
		return err
	}
	if err := register("config/procedure_update", "Replace one project Procedure definition.", false, func(ctx context.Context, raw json.RawMessage) (any, error) {
		var input struct {
			Name       string                           `json:"name"`
			Definition model.ProjectProcedureDefinition `json:"definition"`
			Reason     string                           `json:"reason"`
		}
		if err := decode(raw, &input); err != nil {
			return nil, err
		}
		projectID, err := s.boundConfigurationProject(ctx)
		if err != nil {
			return nil, err
		}
		return s.Service.ConfigProcedureUpdate(ctx, service.ConfigProcedureUpdateInput{ProjectID: projectID, Name: input.Name, Definition: input.Definition, Reason: input.Reason})
	}); err != nil {
		return err
	}
	if err := register("config/procedure_remove", "Remove one unbound project Procedure.", false, func(ctx context.Context, raw json.RawMessage) (any, error) {
		var input struct {
			Name   string `json:"name"`
			Reason string `json:"reason"`
		}
		if err := decode(raw, &input); err != nil {
			return nil, err
		}
		projectID, err := s.boundConfigurationProject(ctx)
		if err != nil {
			return nil, err
		}
		return s.Service.ConfigProcedureRemove(ctx, service.ConfigProcedureRemoveInput{ProjectID: projectID, Name: input.Name, Reason: input.Reason})
	}); err != nil {
		return err
	}
	if err := register("config/hook_list", "List the server-owned Hook catalogue and bindings.", true, func(ctx context.Context, _ json.RawMessage) (any, error) {
		projectID, err := s.boundConfigurationProject(ctx)
		if err != nil {
			return nil, err
		}
		return s.Service.ConfigHookList(ctx, projectID)
	}); err != nil {
		return err
	}
	if err := register("config/hook_read", "Read one server-owned Hook description and canonical payload.", true, func(ctx context.Context, raw json.RawMessage) (any, error) {
		var input struct {
			Hook string `json:"hook"`
		}
		if err := decode(raw, &input); err != nil {
			return nil, err
		}
		projectID, err := s.boundConfigurationProject(ctx)
		if err != nil {
			return nil, err
		}
		return s.Service.ConfigHookRead(ctx, projectID, input.Hook)
	}); err != nil {
		return err
	}
	if err := register("config/hook_bind", "Bind one Hook to one compatible Procedure.", false, func(ctx context.Context, raw json.RawMessage) (any, error) {
		var input struct {
			Hook      string `json:"hook"`
			Procedure string `json:"procedure"`
			Reason    string `json:"reason"`
		}
		if err := decode(raw, &input); err != nil {
			return nil, err
		}
		projectID, err := s.boundConfigurationProject(ctx)
		if err != nil {
			return nil, err
		}
		return s.Service.ConfigHookBind(ctx, service.ConfigHookBindInput{ProjectID: projectID, Hook: input.Hook, Procedure: input.Procedure, Reason: input.Reason})
	}); err != nil {
		return err
	}
	return register("config/hook_unbind", "Remove one Hook binding.", false, func(ctx context.Context, raw json.RawMessage) (any, error) {
		var input struct {
			Hook   string `json:"hook"`
			Reason string `json:"reason"`
		}
		if err := decode(raw, &input); err != nil {
			return nil, err
		}
		projectID, err := s.boundConfigurationProject(ctx)
		if err != nil {
			return nil, err
		}
		return s.Service.ConfigHookUnbind(ctx, service.ConfigHookUnbindInput{ProjectID: projectID, Hook: input.Hook, Reason: input.Reason})
	})
}

func configActionRole(readOnly bool) string {
	if readOnly {
		return actionRoleWorkflow
	}
	return durableSession.RolePlanner
}

func (s *Server) boundConfigurationProject(ctx context.Context) (string, error) {
	sessionID := service.AgentSessionID(ctx)
	if sessionID == "" {
		return "", fmt.Errorf("authenticated project Session is required")
	}
	record, err := s.activeSession(sessionID)
	if err != nil {
		return "", fmt.Errorf("authenticated project Session is invalid: %w", err)
	}
	if record.ProjectID == "" {
		return "", fmt.Errorf("PROJECT_BINDING_REQUIRED: bind the Session before project configuration")
	}
	return record.ProjectID, nil
}
