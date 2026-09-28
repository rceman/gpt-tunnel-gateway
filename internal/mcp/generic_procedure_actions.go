package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/actioncontract"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

const procedureDomainGuide = "Project Procedures are listed by config/procedure_list. Discover a callable contract with schema path procedure/<name>, then call that exact action."

func (s *Server) addProcedureActions(ctx context.Context, entries map[string]genericActionEntry, projectID string) error {
	if s.Service == nil || projectID == "" {
		return fmt.Errorf("project-bound Session is required for Procedure discovery")
	}
	configuration, err := s.Service.ProjectConfigurationRead(ctx, projectID)
	if err != nil {
		return err
	}
	contracts := s.actionContractSet()
	for name, definition := range configuration.Procedures {
		if err := model.ValidateProcedureName(name); err != nil {
			return fmt.Errorf("invalid configured Procedure name: %w", err)
		}
		path := "procedure/" + name
		if _, exists := entries[path]; exists {
			return fmt.Errorf("dynamic Procedure action collides with static action %q", path)
		}
		contract, err := contracts.CompileProcedureAction(path, definition.Summary, definition.Guide, definition.Input, definition.Output)
		if err != nil {
			return fmt.Errorf("compile configured Procedure %q: %w", name, err)
		}
		name, definition, configurationRevision := name, definition, configuration.Revision
		entries[path] = genericActionEntry{
			GenericAction: GenericAction{
				Path:            path,
				Description:     definition.Summary,
				AuthorityRole:   actionRoleWorkflow,
				SessionBound:    true,
				SessionRequired: true,
				Annotations: ToolAnnotations{
					DestructiveHint: true,
					OpenWorldHint:   true,
				},
				Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
					projectID, err := s.boundConfigurationProject(ctx)
					if err != nil {
						return nil, err
					}
					return s.Service.ProcedureExecutionStart(ctx, service.ProcedureExecutionStartInput{
						ProjectID: projectID, Name: name, Definition: definition,
						ConfigurationRevision: configurationRevision, Input: raw,
					})
				},
			},
			Contract: contract,
		}
	}
	return nil
}

func (s *Server) prepareProcedureActionEntries(ctx context.Context, entries map[string]genericActionEntry, record durableSession.Record, action string) error {
	if !isProcedureAction(action) {
		return nil
	}
	if record.ProjectID == "" {
		return fmt.Errorf("PROJECT_BINDING_REQUIRED: bind the Session before Procedure discovery")
	}
	return s.addProcedureActions(ctx, entries, record.ProjectID)
}

func isProcedureAction(path string) bool {
	return strings.HasPrefix(path, "procedure/") && strings.Count(path, "/") == 1
}

func procedureActionName(path string) (string, bool) {
	if !isProcedureAction(path) {
		return "", false
	}
	name := strings.TrimPrefix(path, "procedure/")
	if model.ValidateProcedureName(name) != nil {
		return "", false
	}
	return name, true
}

func procedureActionContractFromDefinition(contracts *actioncontract.CompiledSet, name string, definition model.ProjectProcedureDefinition) (actioncontract.CompiledAction, error) {
	if contracts == nil {
		return actioncontract.CompiledAction{}, fmt.Errorf("canonical action contracts are unavailable")
	}
	return contracts.CompileProcedureAction("procedure/"+name, definition.Summary, definition.Guide, definition.Input, definition.Output)
}
