package mcp

import (
	"context"
	"encoding/json"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func (s *Server) ensureTaskAuthoringActions() {
	s.taskAuthoringActions.Do(func() {
		s.taskAuthoringActionErr = s.registerTaskAuthoringActions()
	})
	if s.taskAuthoringActionErr != nil {
		panic(s.taskAuthoringActionErr)
	}
}

func taskAuthoringProperties() map[string]any {
	priority := str("Bounded authoring priority.")
	priority["maxLength"] = 32
	metadata := map[string]any{"type": "object", "additionalProperties": str("Bounded preparation metadata value.")}
	relation := str("Closed ADR relation.")
	relation["enum"] = []any{model.TaskADRNoRequired, model.TaskADRImplementsExisting, model.TaskADRRequiresNew, model.TaskADRSupersedesExisting}
	return map[string]any{
		"project_id": str("Registered project identifier."), "task_id": str("Stable Task identifier."),
		"title": str("Task title."), "objective": str("Task objective."),
		"acceptance_criteria": array(str("Acceptance criterion.")), "constraints": array(str("Task constraint.")),
		"priority": priority, "dependencies": array(str("Bounded Task dependency.")),
		"preparation_references": array(str("Preparation reference.")), "metadata": metadata,
		"adr_relation": relation, "adr_references": array(str("Accepted ADR identifier.")),
		"created_by": str("Author identity."), "updated_by": str("Author identity."), "ready_by": str("Ready seal author identity."),
		"expected_revision":        integer("Expected authoring revision.", 1, 1000000),
		"expected_revision_sha256": str("Optional exact authoring revision hash."),
		"expected_hub_revision":    str("Optimistic Hub revision."),
	}
}

func taskAuthoringCreateSchema() map[string]any {
	properties := taskAuthoringProperties()
	delete(properties, "task_id")
	delete(properties, "updated_by")
	delete(properties, "ready_by")
	delete(properties, "expected_revision")
	delete(properties, "expected_revision_sha256")
	return obj(properties, "project_id", "title", "objective", "adr_relation", "created_by")
}

func taskAuthoringUpdateSchema() map[string]any {
	properties := taskAuthoringProperties()
	delete(properties, "created_by")
	delete(properties, "ready_by")
	return obj(properties, "project_id", "task_id", "expected_revision", "updated_by")
}

func taskAuthoringReadySchema() map[string]any {
	properties := taskAuthoringProperties()
	for _, key := range []string{"title", "objective", "acceptance_criteria", "constraints", "priority", "dependencies", "preparation_references", "metadata", "adr_relation", "adr_references", "created_by", "updated_by"} {
		delete(properties, key)
	}
	return obj(properties, "project_id", "task_id", "expected_revision", "ready_by")
}

func taskAuthoringOutputSchema() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": true}
}

func (s *Server) registerTaskAuthoringActions() error {
	if err := s.RegisterGenericAction(GenericAction{
		Path: "task/create", Description: "Create a branchless train_v2 planned Task specification.",
		InputSchema: taskAuthoringCreateSchema(), OutputSchema: taskAuthoringOutputSchema(),
		Annotations: ToolAnnotations{DestructiveHint: true, IdempotentHint: false}, AuthorityRole: actionRolePlannerOrDelivery, AllowLegacyOverride: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in service.TaskAuthoringCreateInput
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			task, operation, err := s.Service.TaskAuthoringCreate(ctx, in)
			if err != nil {
				return nil, err
			}
			return map[string]any{"task": task, "operation": operation}, nil
		},
	}); err != nil {
		return err
	}
	if err := s.RegisterGenericAction(GenericAction{
		Path: "task/update", Description: "Optimistically update a train_v2 Task specification.",
		InputSchema: taskAuthoringUpdateSchema(), OutputSchema: taskAuthoringOutputSchema(),
		Annotations: ToolAnnotations{DestructiveHint: true, IdempotentHint: false}, AuthorityRole: actionRolePlannerOrDelivery,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in service.TaskAuthoringUpdateInput
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			task, operation, err := s.Service.TaskAuthoringUpdate(ctx, in)
			if err != nil {
				return nil, err
			}
			return map[string]any{"task": task, "operation": operation}, nil
		},
	}); err != nil {
		return err
	}
	if err := s.RegisterGenericAction(GenericAction{
		Path: "task/ready", Description: "Persist the exact train_v2 Task readiness seal.",
		InputSchema: taskAuthoringReadySchema(), OutputSchema: taskAuthoringOutputSchema(),
		Annotations: ToolAnnotations{DestructiveHint: true, IdempotentHint: true}, AuthorityRole: actionRolePlannerOrDelivery,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in service.TaskAuthoringReadyInput
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			task, operation, err := s.Service.TaskAuthoringReady(ctx, in)
			if err != nil {
				return nil, err
			}
			return map[string]any{"task": task, "operation": operation}, nil
		},
	}); err != nil {
		return err
	}
	if err := s.RegisterGenericAction(GenericAction{
		Path: "task/list", Description: "List bounded train_v2 Task specifications or historical Tasks.",
		InputSchema: obj(map[string]any{
			"project_id": str("Registered project identifier."), "query": str("Legacy Task search text."),
			"status": str("Optional Task status."), "limit": integer("Maximum Tasks.", 1, service.MaxTaskListLimit), "cursor": str("Legacy continuation cursor."),
		}, "project_id"),
		OutputSchema: taskAuthoringOutputSchema(), Annotations: ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}, AuthorityRole: actionRolePlannerOrDelivery, AllowLegacyOverride: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var input struct {
				ProjectID string `json:"project_id"`
				Query     string `json:"query,omitempty"`
				Status    string `json:"status,omitempty"`
				Limit     int    `json:"limit,omitempty"`
				Cursor    string `json:"cursor,omitempty"`
			}
			if err := decode(raw, &input); err != nil {
				return nil, err
			}
			enabled, err := s.Service.TrainV2Enabled(ctx, input.ProjectID)
			if err != nil {
				return nil, err
			}
			if enabled {
				return s.Service.TaskAuthoringList(ctx, service.TaskAuthoringListInput{ProjectID: input.ProjectID, Status: input.Status, Limit: input.Limit})
			}
			return s.Service.TaskListQuery(ctx, service.TaskListInput{ProjectID: input.ProjectID, Query: input.Query, Status: input.Status, Limit: input.Limit, Cursor: input.Cursor})
		},
	}); err != nil {
		return err
	}
	return s.RegisterGenericAction(GenericAction{
		Path: "task/read", Description: "Read a train_v2 Task specification or historical Task record.",
		InputSchema: obj(map[string]any{"task_id": str("Stable Task identifier.")}, "task_id"), OutputSchema: taskAuthoringOutputSchema(),
		Annotations: ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}, AuthorityRole: actionRolePlannerOrDelivery, AllowLegacyOverride: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var input struct {
				TaskID string `json:"task_id"`
			}
			if err := decode(raw, &input); err != nil {
				return nil, err
			}
			if task, err := s.Service.TaskAuthoringFind(ctx, input.TaskID); err == nil {
				return map[string]any{"task": task, "authoring": true}, nil
			}
			packet, err := s.Service.TaskRead(ctx, input.TaskID)
			if err == nil {
				return service.PublicTaskPacketView(packet), nil
			}
			record, recordErr := s.Service.TaskReadRecord(ctx, input.TaskID)
			if recordErr != nil {
				return nil, err
			}
			return map[string]any{"task": record.Task, "state": record.State, "run_summaries": record.RunSummaries, "active_run": false}, nil
		},
	})
}
