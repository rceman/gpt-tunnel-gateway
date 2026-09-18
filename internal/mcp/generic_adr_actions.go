package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func (s *Server) ensureADRActions() {
	s.adrActions.Do(func() { s.adrActionErr = s.registerADRActions() })
	if s.adrActionErr != nil {
		panic(s.adrActionErr)
	}
}

func (s *Server) registerADRActions() error {
	register := func(a GenericAction) error {
		a.AuthorityRole = actionRoleWorkflow
		a.SessionBound = true
		a.LocalReceiptOnly = true
		a.AllowLegacyOverride = true
		return s.RegisterGenericAction(a)
	}
	if err := register(GenericAction{
		Path:                 "adr/create",
		Description:          "Create one stable revisioned ADR.",
		InputSchema:          adrCreateSchema(),
		ExecutionInputSchema: adrExecutionSchema(adrCreateSchema()),
		OutputSchema:         adrMutationOutputSchema(),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
			IdempotentHint:  true,
		},
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				ProjectID      string `json:"project_id"`
				Title          string `json:"title"`
				Summary        string `json:"summary"`
				Context        string `json:"context"`
				Decision       string `json:"decision"`
				Consequences   string `json:"consequences"`
				Status         string `json:"status,omitempty"`
				RelationType   string `json:"relation_type,omitempty"`
				RelationTarget string `json:"relation_target,omitempty"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			actor := service.AgentSessionID(ctx)
			if actor == "" {
				return nil, fmt.Errorf("authorized session actor is unavailable")
			}
			v, err := s.Service.ADRCreate(ctx, service.ADRCreateInput{ADR: model.ADR{ProjectID: in.ProjectID, Title: in.Title, Summary: in.Summary, Context: in.Context, Decision: in.Decision, Consequences: in.Consequences, Status: in.Status, CreatedBy: actor, UpdatedBy: actor}, RelationType: in.RelationType, RelationTarget: in.RelationTarget})
			if err != nil {
				return nil, err
			}
			return adrMutationPublicValue(v), nil
		},
	}); err != nil {
		return err
	}
	if err := register(GenericAction{
		Path:                 "adr/read",
		Description:          "Read an ADR revision.",
		InputSchema:          adrReadSchema(),
		ExecutionInputSchema: adrExecutionSchema(adrReadSchema()),
		OutputSchema:         adrOutputSchema(),
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
			v, err := s.Service.ADRReadRevision(ctx, in.ProjectID, in.Key, in.Revision)
			if err != nil {
				return nil, err
			}
			relations, err := s.Service.RelationProjection(ctx, in.ProjectID, v.ID)
			if err != nil {
				return nil, err
			}
			return adrPublicProjection(v, relations), nil
		},
	}); err != nil {
		return err
	}
	if err := register(GenericAction{
		Path:                 "adr/update",
		Description:          "Update ADR content with exact server-owned CAS.",
		InputSchema:          adrUpdateSchema(),
		ExecutionInputSchema: adrExecutionSchema(adrUpdateSchema()),
		OutputSchema:         adrMutationOutputSchema(),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
			IdempotentHint:  true,
		},
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				ProjectID    string  `json:"project_id"`
				Key          string  `json:"key"`
				Title        *string `json:"title,omitempty"`
				Summary      *string `json:"summary,omitempty"`
				Context      *string `json:"context,omitempty"`
				Decision     *string `json:"decision,omitempty"`
				Consequences *string `json:"consequences,omitempty"`
				Status       *string `json:"status,omitempty"`
				Reason       string  `json:"reason"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			actor := service.AgentSessionID(ctx)
			if actor == "" {
				return nil, fmt.Errorf("authorized session actor is unavailable")
			}
			v, err := s.Service.ADRUpdateCurrent(ctx, service.ADRUpdateInput{ProjectID: in.ProjectID, ADRID: in.Key, Title: in.Title, Summary: in.Summary, Context: in.Context, Decision: in.Decision, Consequences: in.Consequences, Status: in.Status, Reason: in.Reason, UpdatedBy: actor})
			if err != nil {
				return nil, err
			}
			return adrMutationPublicValue(v), nil
		},
	}); err != nil {
		return err
	}
	if err := register(GenericAction{
		Path:                 "adr/list",
		Description:          "List bounded ADR summaries.",
		InputSchema:          adrListSchema(),
		ExecutionInputSchema: adrExecutionSchema(adrListSchema()),
		OutputSchema:         adrListOutputSchema(),
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
			p, err := s.Service.ADRListPageWithOptions(ctx, in.ProjectID, service.ADRListInput{CollectionPageInput: service.CollectionPageInput{Limit: service.DefaultPublicCollectionLimit, Cursor: in.Cursor}, IncludeArchived: in.IncludeArchived})
			if err != nil {
				return nil, err
			}
			return adrPublicPageValue(p)
		},
	}); err != nil {
		return err
	}
	if err := register(GenericAction{
		Path:                 "adr/query",
		Description:          "Query ADR summaries.",
		InputSchema:          adrQuerySchema(),
		ExecutionInputSchema: adrExecutionSchema(adrQuerySchema()),
		OutputSchema:         adrListOutputSchema(),
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
			p, err := s.Service.ADRQuery(ctx, in.ProjectID, service.ADRQueryInput{CollectionPageInput: service.CollectionPageInput{Limit: service.DefaultPublicCollectionLimit, Cursor: in.Cursor}, Text: in.Text, Status: in.Status, IncludeArchived: in.Status == model.ADRStatusArchived || in.Status == model.ADRStatusSuperseded})
			if err != nil {
				return nil, err
			}
			return adrPublicPageValue(p)
		},
	}); err != nil {
		return err
	}
	if err := register(GenericAction{
		Path:                 "adr/archive",
		Description:          "Archive an ADR with exact server-owned CAS.",
		InputSchema:          adrArchiveSchema(),
		ExecutionInputSchema: adrExecutionSchema(adrArchiveSchema()),
		OutputSchema:         adrMutationOutputSchema(),
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
			v, err := s.Service.ADRArchiveCurrent(ctx, service.ADRArchiveInput{ProjectID: in.ProjectID, ADRID: in.Key, Reason: in.Reason, ArchivedBy: actor})
			if err != nil {
				return nil, err
			}
			return adrMutationPublicValue(v), nil
		},
	}); err != nil {
		return err
	}
	if err := register(GenericAction{
		Path:                 "adr/history",
		Description:          "Read bounded ADR revision metadata.",
		InputSchema:          adrHistorySchema(),
		ExecutionInputSchema: adrExecutionSchema(adrHistorySchema()),
		OutputSchema:         adrHistoryOutputSchema(),
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
			p, err := s.Service.ADRHistoryPage(ctx, in.ProjectID, in.Key, service.CollectionPageInput{Limit: service.DefaultPublicCollectionLimit, Cursor: in.Cursor}, false)
			if err != nil {
				return nil, err
			}
			return adrHistoryPageValue(p)
		},
	}); err != nil {
		return err
	}
	return nil
}
