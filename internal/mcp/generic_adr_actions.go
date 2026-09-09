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
		a.AuthorityRole = actionRolePlannerOrAgent
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
				ProjectID    string `json:"project_id"`
				Title        string `json:"title"`
				Context      string `json:"context"`
				Decision     string `json:"decision"`
				Consequences string `json:"consequences"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			actor := service.AgentSessionID(ctx)
			if actor == "" {
				return nil, fmt.Errorf("authorized session actor is unavailable")
			}
			v, err := s.Service.ADRCreate(ctx, service.ADRCreateInput{ADR: model.ADR{ProjectID: in.ProjectID, Title: in.Title, Context: in.Context, Decision: in.Decision, Consequences: in.Consequences, CreatedBy: actor, UpdatedBy: actor}})
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
				ADR       string `json:"adr"`
				Revision  int    `json:"revision"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			v, err := s.Service.ADRReadRevision(ctx, in.ProjectID, in.ADR, in.Revision)
			if err != nil {
				return nil, err
			}
			return adrPublicProjection(v), nil
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
				ADR          string  `json:"adr"`
				Title        *string `json:"title,omitempty"`
				Context      *string `json:"context,omitempty"`
				Decision     *string `json:"decision,omitempty"`
				Consequences *string `json:"consequences,omitempty"`
				Reason       string  `json:"reason"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			actor := service.AgentSessionID(ctx)
			if actor == "" {
				return nil, fmt.Errorf("authorized session actor is unavailable")
			}
			v, err := s.Service.ADRUpdateCurrent(ctx, service.ADRUpdateInput{ProjectID: in.ProjectID, ADRID: in.ADR, Title: in.Title, Context: in.Context, Decision: in.Decision, Consequences: in.Consequences, Reason: in.Reason, UpdatedBy: actor})
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
				ADR       string `json:"adr"`
				Reason    string `json:"reason"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			actor := service.AgentSessionID(ctx)
			if actor == "" {
				return nil, fmt.Errorf("authorized session actor is unavailable")
			}
			v, err := s.Service.ADRArchiveCurrent(ctx, service.ADRArchiveInput{ProjectID: in.ProjectID, ADRID: in.ADR, Reason: in.Reason, ArchivedBy: actor})
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
				ADR       string `json:"adr"`
				Cursor    string `json:"cursor,omitempty"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			p, err := s.Service.ADRHistoryPage(ctx, in.ProjectID, in.ADR, service.CollectionPageInput{Limit: service.DefaultPublicCollectionLimit, Cursor: in.Cursor}, false)
			if err != nil {
				return nil, err
			}
			return adrHistoryPageValue(p)
		},
	}); err != nil {
		return err
	}
	if err := register(GenericAction{
		Path:                 "debug/adr_legacy_relations",
		Description:          "Read-only legacy ADR relation metadata.",
		InputSchema:          adrLegacyRelationsSchema(),
		ExecutionInputSchema: adrExecutionSchema(adrLegacyRelationsSchema()),
		OutputSchema:         adrLegacyRelationsOutputSchema(),
		Annotations: ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				ProjectID string `json:"project_id"`
				ADR       string `json:"adr,omitempty"`
				Cursor    string `json:"cursor,omitempty"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			page, err := s.Service.ADRLegacyRelations(ctx, in.ProjectID, in.ADR, in.Cursor)
			if err != nil {
				return nil, err
			}
			relations := make([]any, 0, len(page.Relations))
			for _, relation := range page.Relations {
				relations = append(relations, map[string]any{"adr": relation.ADR, "revision": relation.Revision, "supersedes": relation.Supersedes})
			}
			result := map[string]any{"relations": relations}
			if page.HasMore {
				result["_pagination"] = map[string]any{"next_cursor": page.NextCursor}
			}
			return result, nil
		},
	}); err != nil {
		return err
	}
	return s.registerTaskLegacyRevisionActions()
}
