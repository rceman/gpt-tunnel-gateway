package mcp

import (
	"context"
	"encoding/json"

	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func (s *Server) addTaskTools(add toolAdder) {
	taskInputSchema := obj(map[string]any{"project_id": str("Project identifier"), "slug": str("Lowercase task slug"), "type": taskTypeSchema(), "title": str("Task title"), "objective": str("Full objective"), "acceptance_criteria": array(str("Criterion")), "constraints": array(str("Constraint")), "required_gates": array(str("Gate")), "created_by": str("Creator identity"), "expected_hub_revision": str("Optimistic hub revision")}, "project_id", "slug", "title", "objective", "created_by")
	add("task_create", "Create immutable hashed task from a normalized slug and the refreshed project default branch; CI behavior is derived from durable project policy.", taskInputSchema, func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in service.TaskCreateInput
		if e := decode(raw, &in); e != nil {
			return nil, e
		}
		task, res, e := s.Service.TaskCreate(ctx, in)
		return map[string]any{"task": task, "operation": res}, e
	})
	revisionListLimit := integer("Maximum revisions", 1, service.MaxPublicCollectionLimit)
	revisionListLimit["default"] = service.DefaultPublicCollectionLimit
	add("task_revision_list", "List bounded immutable Task revisions with compact continuation.", obj(map[string]any{"task_id": str("Stable task identifier"), "limit": revisionListLimit, "cursor": str("Compact server cursor (<=8 chars); legacy accepted")}, "task_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		var args struct {
			TaskID string `json:"task_id"`
			Limit  int    `json:"limit,omitempty"`
			Cursor string `json:"cursor,omitempty"`
		}
		if err := decode(raw, &args); err != nil {
			return nil, err
		}
		return s.Service.TaskRevisionListPage(ctx, args.TaskID, service.CollectionPageInput{Limit: args.Limit, Cursor: args.Cursor})
	})
	add("task_revision_read", "Read one complete immutable Task revision.", obj(map[string]any{"revision_id": str("Exact TASK.REV<N> identifier")}, "revision_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		id, err := getString(raw, "revision_id")
		if err != nil {
			return nil, err
		}
		return s.Service.TaskRevisionRead(ctx, id)
	})
	add("task_correction_create", "Delivery-authorized creation of one immutable bounded post-finalization Task revision.", taskCorrectionInputSchema(), func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in service.TaskCorrectionCreateInput
		if err := decode(raw, &in); err != nil {
			return nil, err
		}
		revision, operation, err := s.Service.TaskCorrectionCreate(ctx, in)
		if err != nil {
			return nil, err
		}
		return map[string]any{"revision": revision, "operation": operation}, nil
	})
	status := outputEnum("created", "ready", "dispatched", "cancelled", "superseded", "completed", "merge_ready", "deferred", "merged")
	query := str("Case-insensitive search over task ID, slug, title, objective, branch, type, status, and task metadata")
	query["maxLength"] = 256
	cursor := str("Compact server cursor (<=8 chars); legacy accepted")
	limit := integer("Maximum tasks to return; defaults to the safe server limit", 1, service.MaxTaskListLimit)
	limit["default"] = service.DefaultTaskListLimit
	add("task_list", "List bounded project tasks with optional text search, workflow status filtering, and deterministic continuation.", obj(map[string]any{
		"project_id": str("Project identifier"), "query": query, "type": taskTypeSchema(), "status": status,
		"limit": limit, "cursor": cursor,
	}, "project_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in service.TaskListInput
		if e := decode(raw, &in); e != nil {
			return nil, e
		}
		return s.Service.TaskListQuery(ctx, in)
	})
	add("task_read", "Read the canonical Task authoring record.", obj(map[string]any{"task_id": str("Task identifier")}, "task_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		id, e := getString(raw, "task_id")
		if e != nil {
			return nil, e
		}
		return s.Service.TaskAuthoringFind(ctx, id)
	})
	add("task_supersede", "Create a replacement immutable task.", obj(map[string]any{"old_task_id": str("Superseded task"), "task": taskInputSchema}, "old_task_id", "task"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		var envelope service.TaskSupersedeInput
		if e := decode(raw, &envelope); e != nil {
			return nil, e
		}
		return s.Service.TaskSupersedeAsync(ctx, envelope)
	})
}
