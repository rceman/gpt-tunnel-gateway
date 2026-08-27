package mcp

import (
	"context"
	"encoding/json"

	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func (s *Server) addADRTools(add toolAdder) {
	adrLimit := integer("Maximum ADRs", 1, service.MaxPublicCollectionLimit)
	adrLimit["default"] = service.DefaultPublicCollectionLimit
	add("adr_list", "List bounded accepted ADRs with deterministic continuation. New next_cursor values are compact server-owned tokens of at most 8 safe characters; legacy cursors remain input-compatible.", obj(map[string]any{"project_id": str("Project identifier"), "limit": adrLimit, "cursor": str("Server-owned continuation cursor; new values are <=8 safe characters and legacy values are accepted")}, "project_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		var args struct {
			ProjectID string `json:"project_id"`
			Limit     int    `json:"limit,omitempty"`
			Cursor    string `json:"cursor,omitempty"`
		}
		if e := decode(raw, &args); e != nil {
			return nil, e
		}
		v, e := s.Service.ADRListPage(ctx, args.ProjectID, service.CollectionPageInput{Limit: args.Limit, Cursor: args.Cursor})
		return v, e
	})
	add("adr_read", "Read an ADR.", obj(map[string]any{"project_id": str("Project identifier"), "adr_id": str("ADR identifier")}, "project_id", "adr_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		p, e := getString(raw, "project_id")
		if e != nil {
			return nil, e
		}
		id, e := getString(raw, "adr_id")
		if e != nil {
			return nil, e
		}
		return s.Service.ADRRead(ctx, p, id)
	})
	add("adr_create", "Create immutable ADR.", obj(map[string]any{"adr": map[string]any{"type": "object"}, "expected_hub_revision": str("Optimistic hub revision")}, "adr"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in service.ADRCreateInput
		if e := decode(raw, &in); e != nil {
			return nil, e
		}
		return s.Service.ADRCreateAsync(ctx, in)
	})
}
