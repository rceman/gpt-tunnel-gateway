package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

type ProjectConfigurationMutationOutput struct {
	Revision int `json:"revision"`
}

type ProjectProcedureCatalogItem struct {
	Name    string `json:"name"`
	Summary string `json:"summary"`
}

type ProjectProcedureListOutput struct {
	ConfigurationRevision int                           `json:"configuration_revision"`
	Procedures            []ProjectProcedureCatalogItem `json:"procedures"`
}

type ProjectProcedureReadOutput struct {
	ConfigurationRevision int                              `json:"configuration_revision"`
	Name                  string                           `json:"name"`
	Definition            model.ProjectProcedureDefinition `json:"definition"`
}

type ProjectHookSummary struct {
	Hook        string `json:"hook"`
	Description string `json:"description"`
	Procedure   string `json:"procedure,omitempty"`
}

type ProjectHookListOutput struct {
	ConfigurationRevision int                  `json:"configuration_revision"`
	Hooks                 []ProjectHookSummary `json:"hooks"`
}

type ProjectHookReadOutput struct {
	ConfigurationRevision int            `json:"configuration_revision"`
	Hook                  string         `json:"hook"`
	Description           string         `json:"description"`
	PayloadSchema         map[string]any `json:"payload_schema"`
	Procedure             string         `json:"procedure,omitempty"`
}

type ProjectConfigurationSummary struct {
	ConfigurationRevision int `json:"configuration_revision"`
	ProcedureCount        int `json:"procedure_count"`
	BoundHookCount        int `json:"bound_hook_count"`
}

type ConfigProcedureCreateInput struct {
	ProjectID  string
	Name       string
	Definition model.ProjectProcedureDefinition
	Reason     string
}

type ConfigProcedureUpdateInput struct {
	ProjectID  string
	Name       string
	Definition model.ProjectProcedureDefinition
	Reason     string
}

type ConfigProcedureRemoveInput struct {
	ProjectID string
	Name      string
	Reason    string
}

type ConfigHookBindInput struct {
	ProjectID string
	Hook      string
	Procedure string
	Reason    string
}

type ConfigHookUnbindInput struct {
	ProjectID string
	Hook      string
	Reason    string
}

type ConfigGuideBindInput struct {
	ProjectID string
	Subject   string
	RuleID    string
	Reason    string
}

func (s *Service) ConfigProcedureList(ctx context.Context, projectID string) (ProjectProcedureListOutput, error) {
	configuration, err := s.ProjectConfigurationRead(ctx, projectID)
	if err != nil {
		return ProjectProcedureListOutput{}, err
	}
	names := make([]string, 0, len(configuration.Procedures))
	for name := range configuration.Procedures {
		names = append(names, name)
	}
	sort.Strings(names)
	result := ProjectProcedureListOutput{
		ConfigurationRevision: configuration.Revision,
		Procedures:            make([]ProjectProcedureCatalogItem, 0, len(names)),
	}
	for _, name := range names {
		result.Procedures = append(result.Procedures, ProjectProcedureCatalogItem{
			Name:    name,
			Summary: configuration.Procedures[name].Summary,
		})
	}
	return result, nil
}

func (s *Service) ConfigProcedureRead(ctx context.Context, projectID, name string) (ProjectProcedureReadOutput, error) {
	if err := model.ValidateProcedureName(name); err != nil {
		return ProjectProcedureReadOutput{}, err
	}
	configuration, err := s.ProjectConfigurationRead(ctx, projectID)
	if err != nil {
		return ProjectProcedureReadOutput{}, err
	}
	definition, found := configuration.Procedures[name]
	if !found {
		return ProjectProcedureReadOutput{}, fmt.Errorf("Procedure %q not found", name)
	}
	return ProjectProcedureReadOutput{
		ConfigurationRevision: configuration.Revision,
		Name:                  name,
		Definition:            definition,
	}, nil
}

func (s *Service) ConfigHookList(ctx context.Context, projectID string) (ProjectHookListOutput, error) {
	configuration, err := s.ProjectConfigurationRead(ctx, projectID)
	if err != nil {
		return ProjectHookListOutput{}, err
	}
	result := ProjectHookListOutput{
		ConfigurationRevision: configuration.Revision,
		Hooks:                 make([]ProjectHookSummary, 0, len(model.ProjectHookNames())),
	}
	for _, hook := range model.ProjectHookNames() {
		description, _ := model.ProjectHookDescription(hook)
		item := ProjectHookSummary{
			Hook:        hook,
			Description: description,
		}
		if procedure, bound := configuration.Hooks[hook]; bound {
			item.Procedure = procedure
		}
		result.Hooks = append(result.Hooks, item)
	}
	return result, nil
}

func (s *Service) ConfigHookRead(ctx context.Context, projectID, hook string) (ProjectHookReadOutput, error) {
	if !model.IsProjectHookName(hook) {
		return ProjectHookReadOutput{}, fmt.Errorf("unknown server-owned Hook %q", hook)
	}
	description, _ := model.ProjectHookDescription(hook)
	payload, _ := model.ProjectHookPayloadSchema(hook)
	configuration, err := s.ProjectConfigurationRead(ctx, projectID)
	if err != nil {
		return ProjectHookReadOutput{}, err
	}
	result := ProjectHookReadOutput{
		ConfigurationRevision: configuration.Revision,
		Hook:                  hook,
		Description:           description,
		PayloadSchema:         payload,
	}
	if procedure, bound := configuration.Hooks[hook]; bound {
		result.Procedure = procedure
	}
	return result, nil
}

func (s *Service) ProjectConfigurationSummary(ctx context.Context, projectID string) (ProjectConfigurationSummary, error) {
	configuration, err := s.ProjectConfigurationRead(ctx, projectID)
	if err != nil {
		return ProjectConfigurationSummary{}, err
	}
	return ProjectConfigurationSummary{
		ConfigurationRevision: configuration.Revision,
		ProcedureCount:        len(configuration.Procedures),
		BoundHookCount:        len(configuration.Hooks),
	}, nil
}

func (s *Service) ConfigProcedureCreate(ctx context.Context, in ConfigProcedureCreateInput) (ProjectConfigurationMutationOutput, error) {
	if err := model.ValidateProcedureName(in.Name); err != nil {
		return ProjectConfigurationMutationOutput{}, err
	}
	if err := model.ValidateProjectProcedureDefinition(in.Definition); err != nil {
		return ProjectConfigurationMutationOutput{}, err
	}
	return s.mutateProjectConfiguration(ctx, in.ProjectID, in.Reason, "config/procedure_create", []string{"procedures." + in.Name}, func(candidate *model.ProjectConfiguration) error {
		if _, exists := candidate.Procedures[in.Name]; exists {
			return fmt.Errorf("Procedure %q already exists", in.Name)
		}
		if len(candidate.Procedures) >= model.MaxProjectProcedures {
			return fmt.Errorf("Procedure catalogue is full")
		}
		candidate.Procedures[in.Name] = in.Definition
		return nil
	})
}

func (s *Service) ConfigProcedureUpdate(ctx context.Context, in ConfigProcedureUpdateInput) (ProjectConfigurationMutationOutput, error) {
	if err := model.ValidateProcedureName(in.Name); err != nil {
		return ProjectConfigurationMutationOutput{}, err
	}
	if err := model.ValidateProjectProcedureDefinition(in.Definition); err != nil {
		return ProjectConfigurationMutationOutput{}, err
	}
	return s.mutateProjectConfiguration(ctx, in.ProjectID, in.Reason, "config/procedure_update", []string{"procedures." + in.Name}, func(candidate *model.ProjectConfiguration) error {
		if _, exists := candidate.Procedures[in.Name]; !exists {
			return fmt.Errorf("Procedure %q not found", in.Name)
		}
		candidate.Procedures[in.Name] = in.Definition
		return nil
	})
}

func (s *Service) ConfigProcedureRemove(ctx context.Context, in ConfigProcedureRemoveInput) (ProjectConfigurationMutationOutput, error) {
	if err := model.ValidateProcedureName(in.Name); err != nil {
		return ProjectConfigurationMutationOutput{}, err
	}
	return s.mutateProjectConfiguration(ctx, in.ProjectID, in.Reason, "config/procedure_remove", []string{"procedures." + in.Name}, func(candidate *model.ProjectConfiguration) error {
		if _, exists := candidate.Procedures[in.Name]; !exists {
			return fmt.Errorf("Procedure %q not found", in.Name)
		}
		for hook, procedure := range candidate.Hooks {
			if procedure == in.Name {
				return fmt.Errorf("Procedure %q is bound to Hook %q", in.Name, hook)
			}
		}
		delete(candidate.Procedures, in.Name)
		return nil
	})
}

func (s *Service) ConfigHookBind(ctx context.Context, in ConfigHookBindInput) (ProjectConfigurationMutationOutput, error) {
	if !model.IsProjectHookName(in.Hook) {
		return ProjectConfigurationMutationOutput{}, fmt.Errorf("unknown server-owned Hook %q", in.Hook)
	}
	if err := model.ValidateProcedureName(in.Procedure); err != nil {
		return ProjectConfigurationMutationOutput{}, err
	}
	return s.mutateProjectConfiguration(ctx, in.ProjectID, in.Reason, "config/hook_bind", []string{"hooks." + in.Hook}, func(candidate *model.ProjectConfiguration) error {
		if _, exists := candidate.Procedures[in.Procedure]; !exists {
			return fmt.Errorf("Procedure %q not found", in.Procedure)
		}
		candidate.Hooks[in.Hook] = in.Procedure
		return nil
	})
}

func (s *Service) ConfigHookUnbind(ctx context.Context, in ConfigHookUnbindInput) (ProjectConfigurationMutationOutput, error) {
	if !model.IsProjectHookName(in.Hook) {
		return ProjectConfigurationMutationOutput{}, fmt.Errorf("unknown server-owned Hook %q", in.Hook)
	}
	return s.mutateProjectConfiguration(ctx, in.ProjectID, in.Reason, "config/hook_unbind", []string{"hooks." + in.Hook}, func(candidate *model.ProjectConfiguration) error {
		if _, exists := candidate.Hooks[in.Hook]; !exists {
			return fmt.Errorf("Hook %q is not bound", in.Hook)
		}
		delete(candidate.Hooks, in.Hook)
		return nil
	})
}

func (s *Service) ConfigGuideBind(ctx context.Context, in ConfigGuideBindInput) (ProjectConfigurationMutationOutput, error) {
	if !model.GuideRequired(in.Subject) {
		return ProjectConfigurationMutationOutput{}, fmt.Errorf("guide subject %q is not applicable", in.Subject)
	}
	if err := model.ValidateRuleID(in.RuleID); err != nil {
		return ProjectConfigurationMutationOutput{}, err
	}
	rule, err := s.RuleRead(ctx, in.ProjectID, in.RuleID)
	if err != nil {
		return ProjectConfigurationMutationOutput{}, err
	}
	if rule.ID != in.RuleID || rule.ProjectID != in.ProjectID || rule.Status != model.RuleStatusAccepted {
		return ProjectConfigurationMutationOutput{}, fmt.Errorf("guide Rule must be accepted and belong to the bound project")
	}
	if _, err := model.ValidateGuideValue(in.Subject, rule.Value); err != nil {
		return ProjectConfigurationMutationOutput{}, fmt.Errorf("guide Rule value does not match the %s projection: %w", in.Subject, err)
	}
	return s.mutateProjectConfiguration(ctx, in.ProjectID, in.Reason, "config/guide_bind", []string{"guide_bindings." + in.Subject}, func(candidate *model.ProjectConfiguration) error {
		candidate.GuideBindings[in.Subject] = in.RuleID
		return nil
	})
}

func (s *Service) mutateProjectConfiguration(ctx context.Context, projectID, reason, mutation string, changedFields []string, apply func(*model.ProjectConfiguration) error) (ProjectConfigurationMutationOutput, error) {
	if err := authority.RequirePlanner(ctx); err != nil {
		return ProjectConfigurationMutationOutput{}, err
	}
	if err := validateConfigurationMutationReason(reason); err != nil {
		return ProjectConfigurationMutationOutput{}, err
	}
	actor := AgentSessionID(ctx)
	if actor == "" || containsControl(actor) {
		return ProjectConfigurationMutationOutput{}, fmt.Errorf("bound Session actor is required")
	}
	if err := requireSharedProjectConfiguration(ctx, s, projectID); err != nil {
		return ProjectConfigurationMutationOutput{}, err
	}
	current, err := s.ProjectConfigurationRead(ctx, projectID)
	if err != nil {
		return ProjectConfigurationMutationOutput{}, err
	}
	candidate, err := projectConfigurationCandidate(current, actor, apply)
	if err != nil {
		return ProjectConfigurationMutationOutput{}, err
	}
	if s.Durability != nil {
		payload, err := json.Marshal(candidate)
		if err != nil {
			return ProjectConfigurationMutationOutput{}, err
		}
		mutationID, err := projectConfigurationMutationID()
		if err != nil {
			return ProjectConfigurationMutationOutput{}, err
		}
		_, err = s.Durability.CommitSharedLifecycleRevision(ctx, sqlitestore.SharedLifecycleRevision{
			OperationID: mutationID, EntityType: "project_configuration", ProjectID: projectID, EntityID: projectID,
			ExpectedRevision: int64(current.Revision), ExpectedStoreRevision: int64(current.Revision), Revision: int64(candidate.Revision),
			Kind: "project-configuration-update", HistoryMutationKind: "update", Payload: payload,
			Actor: actor, Reason: reason, ChangedFields: changedFields, CreatedAt: candidate.UpdatedAt,
		})
		if err != nil {
			if sqlitestore.IsSharedRevisionConflict(err) {
				return ProjectConfigurationMutationOutput{}, &LifecycleConflictError{
					Code:  "conflict",
					Phase: "project_configuration.cas",
				}
			}
			return ProjectConfigurationMutationOutput{}, err
		}
		return ProjectConfigurationMutationOutput{Revision: candidate.Revision}, nil
	}
	path := s.projectConfigurationPath(projectID)
	_, err = s.Hub.Transact(ctx, "", "gateway: "+mutation+" "+projectID+": "+reason, func(worktree string) ([]string, error) {
		var latestRaw json.RawMessage
		if err := readWorktreeJSON(worktree, path, &latestRaw); err != nil {
			return nil, err
		}
		latest, err := sqlitestore.DecodeCanonicalProjectConfigurationPayload(latestRaw)
		if err != nil {
			return nil, err
		}
		if latest.Revision != current.Revision {
			return nil, &LifecycleConflictError{
				Code:  "conflict",
				Phase: "project_configuration.cas",
			}
		}
		latestCandidate, err := projectConfigurationCandidate(latest, actor, apply)
		if err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, path, latestCandidate); err != nil {
			return nil, err
		}
		candidate = latestCandidate
		return []string{path}, nil
	})
	if err != nil {
		return ProjectConfigurationMutationOutput{}, err
	}
	return ProjectConfigurationMutationOutput{Revision: candidate.Revision}, nil
}

func projectConfigurationCandidate(current model.ProjectConfiguration, actor string, apply func(*model.ProjectConfiguration) error) (model.ProjectConfiguration, error) {
	if current.Revision >= int(^uint(0)>>1) {
		return model.ProjectConfiguration{}, fmt.Errorf("project configuration revision is exhausted")
	}
	candidate := current
	candidate.GuideBindings = cloneProjectStringMap(current.GuideBindings)
	candidate.Procedures = cloneProcedureMap(current.Procedures)
	candidate.Hooks = cloneProjectStringMap(current.Hooks)
	if err := apply(&candidate); err != nil {
		return model.ProjectConfiguration{}, err
	}
	candidate.Revision++
	candidate.UpdatedBy = actor
	candidate.UpdatedAt = time.Now().UTC()
	if err := model.ValidateProjectConfiguration(candidate); err != nil {
		return model.ProjectConfiguration{}, err
	}
	return candidate, nil
}

func projectConfigurationMutationID() (string, error) {
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", fmt.Errorf("generate configuration mutation identity: %w", err)
	}
	return "cfg-" + hex.EncodeToString(token[:]), nil
}

func validateConfigurationMutationReason(reason string) error {
	if !utf8.ValidString(reason) || strings.TrimSpace(reason) == "" || utf8.RuneCountInString(reason) > 1024 || len(reason) > 1024 || containsControl(reason) {
		return fmt.Errorf("a bounded mutation reason is required")
	}
	return nil
}

func cloneProjectStringMap(source map[string]string) map[string]string {
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func cloneProcedureMap(source map[string]model.ProjectProcedureDefinition) map[string]model.ProjectProcedureDefinition {
	result := make(map[string]model.ProjectProcedureDefinition, len(source))
	for name, definition := range source {
		result[name] = definition
	}
	return result
}
