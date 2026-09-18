package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

func (s *Server) ensureRuleActions() {
	s.ruleActions.Do(func() { s.ruleActionErr = s.registerRuleActions() })
	if s.ruleActionErr != nil {
		panic(s.ruleActionErr)
	}
}

func (s *Server) registerRuleActions() error {
	register := func(a GenericAction) error {
		a.AuthorityRole = actionRoleWorkflow
		a.SessionBound = true
		a.LocalReceiptOnly = true
		return s.RegisterGenericAction(a)
	}
	if err := register(GenericAction{
		Path:                 "rule/create",
		Description:          "Create a proposed rule (machine or narrative form) in the shared durable store.",
		InputSchema:          ruleCreateSchema(),
		ExecutionInputSchema: adrExecutionSchema(ruleCreateSchema()),
		OutputSchema:         ruleMutationOutputSchema(),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
			IdempotentHint:  true,
		},
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				ProjectID      string          `json:"project_id"`
				Title          string          `json:"title"`
				Summary        string          `json:"summary"`
				Name           string          `json:"name,omitempty"`
				Value          json.RawMessage `json:"value,omitempty"`
				Description    string          `json:"description,omitempty"`
				Status         string          `json:"status,omitempty"`
				RelationType   string          `json:"relation_type,omitempty"`
				RelationTarget string          `json:"relation_target,omitempty"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			actor := service.AgentSessionID(ctx)
			if actor == "" {
				return nil, fmt.Errorf("authorized session actor is unavailable")
			}
			v, err := s.Service.RuleCreate(ctx, service.RuleCreateInput{Rule: model.Rule{ProjectID: in.ProjectID, Title: in.Title, Summary: in.Summary, Status: in.Status, Name: in.Name, Value: in.Value, Description: in.Description, CreatedBy: actor, UpdatedBy: actor}, RelationType: in.RelationType, RelationTarget: in.RelationTarget})
			if err != nil {
				return nil, err
			}
			return ruleMutationPublicValue(v), nil
		},
	}); err != nil {
		return err
	}
	if err := register(GenericAction{
		Path:                 "rule/read",
		Description:          "Read a rule revision.",
		InputSchema:          ruleReadSchema(),
		ExecutionInputSchema: adrExecutionSchema(ruleReadSchema()),
		OutputSchema:         ruleOutputSchema(),
		Annotations: ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				ProjectID string `json:"project_id"`
				Key       string `json:"key"`
				Revision  int    `json:"revision"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			v, err := s.Service.RuleReadRevision(ctx, in.ProjectID, in.Key, in.Revision)
			if err != nil {
				return nil, err
			}
			relations, err := s.Service.RelationProjection(ctx, in.ProjectID, v.ID)
			if err != nil {
				return nil, err
			}
			return rulePublicProjection(v, relations), nil
		},
	}); err != nil {
		return err
	}
	if err := register(GenericAction{
		Path:                 "rule/update",
		Description:          "Update rule content with exact server-owned CAS; the rule name is immutable and is not part of update input.",
		InputSchema:          ruleUpdateSchema(),
		ExecutionInputSchema: adrExecutionSchema(ruleUpdateSchema()),
		OutputSchema:         ruleMutationOutputSchema(),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
			IdempotentHint:  true,
		},
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				ProjectID   string           `json:"project_id"`
				Key         string           `json:"key"`
				Title       *string          `json:"title,omitempty"`
				Summary     *string          `json:"summary,omitempty"`
				Value       *json.RawMessage `json:"value,omitempty"`
				Description *string          `json:"description,omitempty"`
				Status      *string          `json:"status,omitempty"`
				Reason      string           `json:"reason"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			actor := service.AgentSessionID(ctx)
			if actor == "" {
				return nil, fmt.Errorf("authorized session actor is unavailable")
			}
			v, err := s.Service.RuleUpdateCurrent(ctx, service.RuleUpdateInput{ProjectID: in.ProjectID, RuleID: in.Key, Title: in.Title, Summary: in.Summary, Value: in.Value, Description: in.Description, Status: in.Status, Reason: in.Reason, UpdatedBy: actor})
			if err != nil {
				return nil, err
			}
			return ruleMutationPublicValue(v), nil
		},
	}); err != nil {
		return err
	}
	if err := register(GenericAction{
		Path:                 "rule/list",
		Description:          "List bounded rule summaries.",
		InputSchema:          ruleListSchema(),
		ExecutionInputSchema: adrExecutionSchema(ruleListSchema()),
		OutputSchema:         ruleListOutputSchema(),
		Annotations: ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				ProjectID       string `json:"project_id"`
				Cursor          string `json:"cursor,omitempty"`
				IncludeArchived bool   `json:"include_archived,omitempty"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			p, err := s.Service.RuleListPageWithOptions(ctx, in.ProjectID, service.RuleListInput{CollectionPageInput: service.CollectionPageInput{Limit: service.DefaultPublicCollectionLimit, Cursor: in.Cursor}, IncludeArchived: in.IncludeArchived})
			if err != nil {
				return nil, err
			}
			return rulePublicPageValue(p)
		},
	}); err != nil {
		return err
	}
	if err := register(GenericAction{
		Path:                 "rule/query",
		Description:          "Query rule summaries.",
		InputSchema:          ruleQuerySchema(),
		ExecutionInputSchema: adrExecutionSchema(ruleQuerySchema()),
		OutputSchema:         ruleListOutputSchema(),
		Annotations: ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				ProjectID string `json:"project_id"`
				Text      string `json:"text,omitempty"`
				Status    string `json:"status,omitempty"`
				Cursor    string `json:"cursor,omitempty"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			p, err := s.Service.RuleQuery(ctx, in.ProjectID, service.RuleQueryInput{CollectionPageInput: service.CollectionPageInput{Limit: service.DefaultPublicCollectionLimit, Cursor: in.Cursor}, Text: in.Text, Status: in.Status, IncludeArchived: in.Status == model.RuleStatusArchived})
			if err != nil {
				return nil, err
			}
			return rulePublicPageValue(p)
		},
	}); err != nil {
		return err
	}
	if err := register(GenericAction{
		Path:                 "rule/archive",
		Description:          "Archive a rule with exact server-owned CAS.",
		InputSchema:          ruleArchiveSchema(),
		ExecutionInputSchema: adrExecutionSchema(ruleArchiveSchema()),
		OutputSchema:         ruleMutationOutputSchema(),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
			IdempotentHint:  true,
		},
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				ProjectID string `json:"project_id"`
				Key       string `json:"key"`
				Reason    string `json:"reason"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			actor := service.AgentSessionID(ctx)
			if actor == "" {
				return nil, fmt.Errorf("authorized session actor is unavailable")
			}
			v, err := s.Service.RuleArchiveCurrent(ctx, service.RuleArchiveInput{ProjectID: in.ProjectID, RuleID: in.Key, Reason: in.Reason, ArchivedBy: actor})
			if err != nil {
				return nil, err
			}
			return ruleMutationPublicValue(v), nil
		},
	}); err != nil {
		return err
	}
	if err := register(GenericAction{
		Path:                 "rule/history",
		Description:          "Read bounded rule revision metadata.",
		InputSchema:          ruleHistorySchema(),
		ExecutionInputSchema: adrExecutionSchema(ruleHistorySchema()),
		OutputSchema:         ruleHistoryOutputSchema(),
		Annotations: ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				ProjectID string `json:"project_id"`
				Key       string `json:"key"`
				Cursor    string `json:"cursor,omitempty"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			p, err := s.Service.RuleHistoryPage(ctx, in.ProjectID, in.Key, service.CollectionPageInput{Limit: service.DefaultPublicCollectionLimit, Cursor: in.Cursor}, false)
			if err != nil {
				return nil, err
			}
			return ruleHistoryPageValue(p)
		},
	}); err != nil {
		return err
	}
	if err := register(GenericAction{
		Path:                 "rule/effective",
		Description:          "Project the bound project's accepted named rules as {name: typed_value} with the effective-set digest, and acknowledge that exact effective set for the caller Session (non-read-only, idempotent).",
		InputSchema:          ruleEffectiveSchema(),
		ExecutionInputSchema: ruleEffectiveExecutionSchema(),
		OutputSchema:         ruleEffectiveOutputSchema(),
		SessionRequired:      true,
		Annotations: ToolAnnotations{
			IdempotentHint: true,
		},
		Execute: s.ruleEffectiveAction,
	}); err != nil {
		return err
	}
	return nil
}

// ruleEffectiveAction projects the accepted named rules of the bound project
// as {name: typed_value} and acknowledges that exact effective set for the
// caller Session. The acknowledgement side effect is intentionally
// non-read-only and idempotent; the action carries neither project nor
// session identifiers in its result.
func (s *Server) ruleEffectiveAction(ctx context.Context, raw json.RawMessage) (any, error) {
	var input struct {
		ProjectID string `json:"project_id"`
	}
	if err := decode(raw, &input); err != nil {
		return nil, err
	}
	id := service.AgentSessionID(ctx)
	if id == "" {
		return nil, fmt.Errorf("SESSION_REQUIRED: provide the public session field")
	}
	effective, digest, err := s.Service.RuleEffectiveSet(ctx, input.ProjectID)
	if err != nil {
		return nil, err
	}
	updated, err := durableSession.NewStoreWithDurability(s.Service.Durability).AcknowledgeRules(id, globalWorkflowRevision, globalWorkflowDigest(), digest)
	if err != nil {
		return nil, err
	}
	rules := make(map[string]json.RawMessage, len(effective))
	for _, rule := range effective {
		rules[rule.Name] = rule.Value
	}
	publicDigest := digest
	if len(publicDigest) > 8 {
		publicDigest = publicDigest[:8]
	}
	return map[string]any{"digest": publicDigest, "rules": rules, "acknowledged": updated.ProjectRulesDigest == digest}, nil
}
