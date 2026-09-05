package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func taskAuthoringReadySchema() map[string]any {
	properties := taskAuthoringProperties()
	for _, key := range []string{"type", "execution", "scope", "title", "objective", "acceptance_criteria", "constraints", "priority", "dependencies", "preparation_references", "metadata", "adr_relation", "adr_references", "created_by", "updated_by"} {
		delete(properties, key)
	}
	return obj(properties, "project_id", "task_id", "expected_revision", "ready_by")
}
func taskAuthoringOutputSchema() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": true}
}
func (s *Server) registerTaskAuthoringActions() error {
	if err := s.RegisterGenericAction(GenericAction{
		Path:         "task/create",
		Description:  "Create a branchless train_v2 planned Task specification.",
		InputSchema:  taskAuthoringCreateSchema(),
		OutputSchema: taskAuthoringOutputSchema(),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
			IdempotentHint:  false,
		},
		AuthorityRole:       "planner",
		LocalReceiptOnly:    true,
		AllowLegacyOverride: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in service.TaskAuthoringCreateInput
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			operation, err := s.Service.TaskAuthoringCreateAsync(ctx, in)
			if err != nil {
				return nil, err
			}
			return operation.Receipt(), nil
		},
	}); err != nil {
		return err
	}
	if err := s.RegisterGenericAction(GenericAction{
		Path:         "task/update",
		Description:  "Optimistically update a train_v2 Task specification.",
		InputSchema:  taskAuthoringUpdateSchema(),
		OutputSchema: taskAuthoringOutputSchema(),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
			IdempotentHint:  true,
		},
		AuthorityRole:    "planner",
		LocalReceiptOnly: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in service.TaskAuthoringUpdateInput
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			return s.Service.TaskAuthoringUpdateAsync(ctx, in)
		},
	}); err != nil {
		return err
	}
	if err := s.RegisterGenericAction(GenericAction{
		Path:         "task/ready",
		Description:  "Persist the exact train_v2 Task readiness seal.",
		InputSchema:  taskAuthoringReadySchema(),
		OutputSchema: taskAuthoringOutputSchema(),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
			IdempotentHint:  true,
		},
		AuthorityRole:    "planner",
		LocalReceiptOnly: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in service.TaskAuthoringReadyInput
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			return s.Service.TaskAuthoringReadyAsync(ctx, in)
		},
	}); err != nil {
		return err
	}
	if err := s.RegisterGenericAction(GenericAction{
		Path:        "task/list",
		Description: "List bounded train_v2 Task specifications or historical Tasks.",
		InputSchema: obj(map[string]any{
			"project_id": str("Registered project identifier."), "query": str("Case-insensitive Task search text."),
			"type": taskTypeSchema(), "execution": taskExecutionSchema(), "status": str("Optional Task status."), "limit": integer("Maximum Tasks.", 1, service.MaxTaskListLimit), "cursor": str("Legacy continuation cursor."),
		}, "project_id"),
		OutputSchema: taskAuthoringOutputSchema(),
		Annotations: ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
		AuthorityRole:       "planner",
		AllowLegacyOverride: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var input struct {
				ProjectID string              `json:"project_id"`
				Query     string              `json:"query,omitempty"`
				Type      model.TaskType      `json:"type,omitempty"`
				Execution model.TaskExecution `json:"execution,omitempty"`
				Status    string              `json:"status,omitempty"`
				Limit     int                 `json:"limit,omitempty"`
				Cursor    string              `json:"cursor,omitempty"`
			}
			if err := decode(raw, &input); err != nil {
				return nil, err
			}
			enabled, err := s.Service.TrainV2Enabled(ctx, input.ProjectID)
			if err != nil {
				return nil, err
			}
			if enabled {
				return s.Service.TaskAuthoringList(ctx, service.TaskAuthoringListInput{ProjectID: input.ProjectID, Query: input.Query, Type: input.Type, Execution: input.Execution, Status: input.Status, Limit: input.Limit})
			}
			return s.Service.TaskListQuery(ctx, service.TaskListInput{ProjectID: input.ProjectID, Query: input.Query, Type: input.Type, Execution: input.Execution, Status: input.Status, Limit: input.Limit, Cursor: input.Cursor})
		},
	}); err != nil {
		return err
	}
	if err := s.RegisterGenericAction(GenericAction{
		Path:         "task/read",
		Description:  "Read a train_v2 Task specification or historical Task record.",
		InputSchema:  obj(map[string]any{"task_id": str("Stable Task identifier.")}, "task_id"),
		OutputSchema: taskAuthoringOutputSchema(),
		Annotations: ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
		AuthorityRole:       "planner",
		AllowLegacyOverride: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var input struct {
				TaskID string `json:"task_id"`
			}
			if err := decode(raw, &input); err != nil {
				return nil, err
			}
			if task, err := s.Service.TaskAuthoringFind(ctx, input.TaskID); err == nil {
				if enabled, enabledErr := s.Service.TrainV2Enabled(ctx, task.ProjectID); enabledErr != nil {
					return nil, enabledErr
				} else if enabled {
					return s.Service.TrainV2TaskRead(ctx, task.ProjectID, task.ID)
				}
				return map[string]any{"task": task, "authoring": true}, nil
			}
			return nil, fmt.Errorf("Task is not admitted to Train-v2")
		},
	}); err != nil {
		return err
	}
	return s.registerTaskExecutionActions()
}
