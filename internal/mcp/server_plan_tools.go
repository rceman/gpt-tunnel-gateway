package mcp

import (
	"context"
	"encoding/json"

	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func (s *Server) addPlanTools(add toolAdder) {
	add("project_register", "Register a durable project from a JSON object.", obj(map[string]any{"project": map[string]any{"type": "object"}, "expected_hub_revision": str("Optimistic hub revision")}, "project"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in service.ProjectRegisterInput
		if e := decode(raw, &in); e != nil {
			return nil, e
		}
		return s.Service.ProjectRegister(ctx, in)
	})
	add("plan_read", "Read current global plan.", obj(map[string]any{"project_id": str("Project identifier")}, "project_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		id, e := getString(raw, "project_id")
		if e != nil {
			return nil, e
		}
		return s.Service.PlanRead(ctx, id)
	})
	add("plan_cutover", "Owner-invoked one-time conversion of the known schema-v1 plan to schema-v2.", obj(map[string]any{"project_id": str("Project identifier"), "updated_by": str("Owner identity"), "expected_hub_revision": str("Optimistic hub revision")}, "project_id", "updated_by"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in service.PlanCutoverInput
		if e := decode(raw, &in); e != nil {
			return nil, e
		}
		return s.Service.PlanCutover(ctx, in)
	})
	add("plan_update", "Partially update the compact plan manifest.", obj(map[string]any{"project_id": str("Project identifier"), "title": str("Plan title"), "summary": str("Plan summary"), "current_objective": str("Current objective"), "queue": map[string]any{"type": "array", "items": str("Ordered section identifiers")}, "active_task_id": str("Active task"), "updated_by": str("Author identity"), "expected_hub_revision": str("Optimistic hub revision")}, "project_id", "updated_by"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in service.PlanUpdateInput
		if e := decode(raw, &in); e != nil {
			return nil, e
		}
		return s.Service.PlanUpdate(ctx, in)
	})
	add("plan_section_read", "Read one complete plan section by exact identifier.", obj(map[string]any{"project_id": str("Project identifier"), "section_id": str("Plan section identifier")}, "project_id", "section_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		project, e := getString(raw, "project_id")
		if e != nil {
			return nil, e
		}
		section, e := getString(raw, "section_id")
		if e != nil {
			return nil, e
		}
		return s.Service.PlanSectionRead(ctx, project, section)
	})
	add("plan_section_create", "Create one independently versioned plan section.", obj(map[string]any{"project_id": str("Project identifier"), "section_id": str("Plan section identifier"), "title": str("Section title"), "short_description": str("One-line section description"), "description": str("Full section description"), "updated_by": str("Author identity"), "expected_hub_revision": str("Optimistic hub revision")}, "project_id", "section_id", "title", "short_description", "description", "updated_by"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in service.PlanSectionCreateInput
		if e := decode(raw, &in); e != nil {
			return nil, e
		}
		return s.Service.PlanSectionCreate(ctx, in)
	})
	add("plan_section_update", "Partially update one plan section with its independent revision.", obj(map[string]any{"project_id": str("Project identifier"), "section_id": str("Plan section identifier"), "title": str("Section title"), "short_description": str("One-line section description"), "description": str("Full section description"), "updated_by": str("Author identity"), "expected_section_revision": integer("Expected section revision", 1, 1000000), "expected_hub_revision": str("Optimistic hub revision")}, "project_id", "section_id", "updated_by", "expected_section_revision"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in service.PlanSectionUpdateInput
		if e := decode(raw, &in); e != nil {
			return nil, e
		}
		return s.Service.PlanSectionUpdate(ctx, in)
	})
	add("plan_section_delete", "Delete one plan section from current state while retaining Git history.", obj(map[string]any{"project_id": str("Project identifier"), "section_id": str("Plan section identifier"), "updated_by": str("Author identity"), "expected_section_revision": integer("Expected section revision", 1, 1000000), "expected_hub_revision": str("Optimistic hub revision")}, "project_id", "section_id", "updated_by", "expected_section_revision"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in service.PlanSectionDeleteInput
		if e := decode(raw, &in); e != nil {
			return nil, e
		}
		return s.Service.PlanSectionDelete(ctx, in)
	})
	add("plan_render", "Render the complete plan and all sections explicitly.", obj(map[string]any{"project_id": str("Project identifier")}, "project_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		id, e := getString(raw, "project_id")
		if e != nil {
			return nil, e
		}
		return s.Service.PlanRender(ctx, id)
	})
	historyLimit := integer("Maximum commits", 1, service.MaxPublicCollectionLimit)
	historyLimit["default"] = service.DefaultPublicCollectionLimit
	add("plan_history", "List bounded plan Git history with deterministic continuation. New next_cursor values are compact server-owned tokens of at most 8 safe characters; legacy cursors remain input-compatible.", obj(map[string]any{"project_id": str("Project identifier"), "limit": historyLimit, "cursor": str("Server-owned continuation cursor; new values are <=8 safe characters and legacy values are accepted")}, "project_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		var args struct {
			ProjectID string `json:"project_id"`
			Limit     int    `json:"limit,omitempty"`
			Cursor    string `json:"cursor,omitempty"`
		}
		if e := decode(raw, &args); e != nil {
			return nil, e
		}
		return s.Service.PlanHistoryPage(ctx, args.ProjectID, service.CollectionPageInput{Limit: args.Limit, Cursor: args.Cursor})
	})
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
	add("adr_create_status", "Read the durable receipt for an asynchronous ADR create operation.", obj(map[string]any{"operation_id": str("Durable ADR create operation identifier.")}, "operation_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in struct {
			OperationID string `json:"operation_id"`
		}
		if e := decode(raw, &in); e != nil {
			return nil, e
		}
		return s.Service.ADRCreateOperationStatus(ctx, in.OperationID)
	})
}
