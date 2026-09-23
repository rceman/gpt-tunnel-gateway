package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

func (s *Server) ensureRelationActions() {
	s.relationActions.Do(func() { s.relationActionErr = s.registerRelationActions() })
	if s.relationActionErr != nil {
		panic(s.relationActionErr)
	}
}

func relationKindSchema() map[string]any {
	return outputEnum(model.RelationKinds()...)
}

func relationDirectionSchema() map[string]any {
	return outputEnum(sqlitestore.RelationDirectionOutgoing, sqlitestore.RelationDirectionIncoming, sqlitestore.RelationDirectionBoth)
}

func relationCreateSchema() map[string]any {
	return obj(map[string]any{"source": str("Canonical source entity key."), "kind": relationKindSchema(), "target": str("Canonical target entity key.")}, "source", "kind", "target")
}

func relationListSchema() map[string]any {
	return obj(map[string]any{"source": str("Canonical source entity key."), "kind": relationKindSchema(), "direction": relationDirectionSchema(), "cursor": publicServerCursorSchema()}, "source")
}

func relationCreateOutputSchema() map[string]any {
	return closedOutput(map[string]any{"source": outputString(), "kind": relationKindSchema(), "target": outputString(), "created": outputBoolean()}, "source", "kind", "target", "created")
}

func relationListOutputSchema() map[string]any {
	return closedOutput(map[string]any{"source": outputString(), "relations": relationGroupedOutputSchema()}, "source", "relations")
}

func (s *Server) registerRelationActions() error {
	register := func(action GenericAction) error {
		action.AuthorityRole = actionRoleWorkflow
		action.SessionBound = true
		action.LocalReceiptOnly = true
		return s.RegisterGenericAction(action)
	}
	if err := register(GenericAction{
		Path:                 "relation/create",
		Description:          "Create one idempotent canonical directed relation between two canonical entities.",
		InputSchema:          relationCreateSchema(),
		ExecutionInputSchema: adrExecutionSchema(relationCreateSchema()),
		OutputSchema:         relationCreateOutputSchema(),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
			IdempotentHint:  true,
		},
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				ProjectID string `json:"project_id"`
				Source    string `json:"source"`
				Kind      string `json:"kind"`
				Target    string `json:"target"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			actor := service.AgentSessionID(ctx)
			if actor == "" {
				return nil, fmt.Errorf("authorized session actor is unavailable")
			}
			result, err := s.Service.RelationCreate(ctx, service.RelationCreateInput{ProjectID: in.ProjectID, Source: in.Source, Kind: in.Kind, Target: in.Target, CreatedBy: actor})
			if err != nil {
				return nil, err
			}
			return map[string]any{"source": result.Source, "kind": result.Kind, "target": result.Target, "created": result.Created}, nil
		},
	}); err != nil {
		return err
	}
	return register(GenericAction{
		Path:                 "relation/list",
		Description:          "List bounded canonical relations for one source with live target titles.",
		InputSchema:          relationListSchema(),
		ExecutionInputSchema: adrExecutionSchema(relationListSchema()),
		OutputSchema:         relationListOutputSchema(),
		Annotations: ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				ProjectID string `json:"project_id"`
				Source    string `json:"source"`
				Kind      string `json:"kind,omitempty"`
				Direction string `json:"direction,omitempty"`
				Cursor    string `json:"cursor,omitempty"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			result, err := s.Service.RelationList(ctx, service.RelationListInput{ProjectID: in.ProjectID, Source: in.Source, Kind: in.Kind, Direction: in.Direction, Cursor: in.Cursor})
			if err != nil {
				return nil, err
			}
			value := map[string]any{"source": result.Source, "relations": relationGroupedValue(result.Relations)}
			return genericActionPageResult(value, result.HasMore, result.NextCursor)
		},
	})
}

func relationGroupedValue(relations map[string]map[string]string) map[string]any {
	grouped := make(map[string]any, len(relations))
	for kind, bucket := range relations {
		entries := make(map[string]any, len(bucket))
		for key, title := range bucket {
			entries[key] = title
		}
		grouped[kind] = entries
	}
	return grouped
}
