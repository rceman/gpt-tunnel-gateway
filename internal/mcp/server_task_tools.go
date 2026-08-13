package mcp

import (
	"context"
	"encoding/json"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func (s *Server) addTaskTools(add toolAdder) {
	operationClass := str("Closed workflow operation class")
	operationClass["enum"] = model.WorkflowOperationClasses()
	taskInputSchema := obj(map[string]any{"project_id": str("Project identifier"), "slug": str("Lowercase task slug"), "title": str("Task title"), "objective": str("Full objective"), "acceptance_criteria": array(str("Criterion")), "constraints": array(str("Constraint")), "required_gates": array(str("Gate")), "operation_class": operationClass, "created_by": str("Creator identity"), "expected_hub_revision": str("Optimistic hub revision")}, "project_id", "slug", "title", "objective", "operation_class", "created_by")
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
	add("task_revision_status", "Read one bounded Task revision status projection.", obj(map[string]any{"revision_id": str("Exact TASK.REV<N> identifier")}, "revision_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		id, err := getString(raw, "revision_id")
		if err != nil {
			return nil, err
		}
		return s.Service.TaskRevisionStatus(ctx, id)
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
	query := str("Case-insensitive search over task ID, slug, title, objective, branch, status, and task metadata")
	query["maxLength"] = 256
	cursor := str("Compact server cursor (<=8 chars); legacy accepted")
	limit := integer("Maximum tasks to return; defaults to the safe server limit", 1, service.MaxTaskListLimit)
	limit["default"] = service.DefaultTaskListLimit
	add("task_list", "List bounded project tasks with optional text search, workflow status filtering, and deterministic continuation.", obj(map[string]any{
		"project_id": str("Project identifier"), "query": query, "status": status,
		"limit": limit, "cursor": cursor,
	}, "project_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in service.TaskListInput
		if e := decode(raw, &in); e != nil {
			return nil, e
		}
		return s.Service.TaskListQuery(ctx, in)
	})
	add("task_read", "Read task record and active execution packet when a run exists.", obj(map[string]any{"task_id": str("Task identifier")}, "task_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		id, e := getString(raw, "task_id")
		if e != nil {
			return nil, e
		}
		packet, e := s.Service.TaskRead(ctx, id)
		if e == nil {
			return service.PublicTaskPacketView(packet), nil
		}
		task, e2 := s.Service.TaskReadRecord(ctx, id)
		if e2 != nil {
			return nil, e
		}
		response := map[string]any{"task": task.Task, "state": task.State, "run_summaries": task.RunSummaries, "active_run": false}
		if task.WorkflowPolicy != nil {
			response["workflow_policy"] = task.WorkflowPolicy
		}
		return response, nil
	})
	add("task_supersede", "Create a replacement immutable task.", obj(map[string]any{"old_task_id": str("Superseded task"), "task": taskInputSchema}, "old_task_id", "task"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		var envelope struct {
			OldTaskID string                  `json:"old_task_id"`
			Task      service.TaskCreateInput `json:"task"`
		}
		if e := decode(raw, &envelope); e != nil {
			return nil, e
		}
		task, res, e := s.Service.TaskSupersede(ctx, envelope.OldTaskID, envelope.Task)
		return map[string]any{"task": task, "operation": res}, e
	})
	add("task_cancel", "Cancel an undispatched task record.", obj(map[string]any{"task_id": str("Task identifier"), "expected_hub_revision": str("Optimistic hub revision")}, "task_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		id, e := getString(raw, "task_id")
		if e != nil {
			return nil, e
		}
		return s.Service.TaskCancel(ctx, id, optionalString(raw, "expected_hub_revision"))
	})
	add("task_mark_merge_ready", "Record that a completed task's latest successful report is ready for GPT merge review; this mutates durable lifecycle state only.", obj(map[string]any{"task_id": str("Task identifier"), "expected_hub_revision": str("Optimistic hub revision")}, "task_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in service.TaskMarkMergeReadyInput
		if e := decode(raw, &in); e != nil {
			return nil, e
		}
		return s.Service.TaskMarkMergeReady(ctx, in)
	})
	reason := str("Bounded reason for deferral")
	reason["minLength"] = 1
	reason["maxLength"] = 1024
	add("task_defer", "Defer a completed or merge-ready task with a bounded durable reason; this does not mutate a repository.", obj(map[string]any{"task_id": str("Task identifier"), "reason": reason, "expected_hub_revision": str("Optimistic hub revision")}, "task_id", "reason"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in service.TaskDeferInput
		if e := decode(raw, &in); e != nil {
			return nil, e
		}
		return s.Service.TaskDefer(ctx, in)
	})
	integrationHead := str("Exact remote develop commit SHA")
	integrationHead["pattern"] = "^[0-9a-f]{40}$"
	add("task_mark_merged", "Record a verified existing remote develop receipt for a merge-ready task; it performs no merge, push, checkout or branch deletion.", obj(map[string]any{"task_id": str("Task identifier"), "integration_head": integrationHead, "expected_hub_revision": str("Optimistic hub revision")}, "task_id", "integration_head"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in service.TaskMarkMergedInput
		if e := decode(raw, &in); e != nil {
			return nil, e
		}
		return s.Service.TaskMarkMerged(ctx, in)
	})
}
