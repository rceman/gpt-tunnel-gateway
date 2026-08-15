package mcp

import (
	"context"
	"encoding/json"
	"fmt"

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
	all := taskAuthoringProperties()
	all["slug"] = str("Legacy pre-cutover task slug.")
	operationClass := str("Legacy pre-cutover operation class.")
	operationClass["enum"] = model.WorkflowOperationClasses()
	all["operation_class"] = operationClass
	all["required_gates"] = array(str("Legacy pre-cutover required gate."))
	for _, key := range []string{"task_id", "updated_by", "ready_by", "expected_revision", "expected_revision_sha256"} {
		delete(all, key)
	}

	legacy := make(map[string]any, len(all))
	for _, key := range []string{"project_id", "slug", "title", "objective", "acceptance_criteria", "constraints", "required_gates", "operation_class", "created_by", "expected_hub_revision"} {
		legacy[key] = all[key]
	}
	v2 := make(map[string]any, len(all))
	for key, value := range all {
		if key != "slug" && key != "operation_class" && key != "required_gates" {
			v2[key] = value
		}
	}
	// task/create is intentionally a mode-dispatched boundary. Discovery must
	// describe both valid inputs, while oneOf makes the selected mode's required
	// fields explicit instead of advertising a misleading hybrid contract.
	schema := obj(all, "project_id")
	schema["oneOf"] = []any{
		obj(legacy, "project_id", "slug", "title", "objective", "operation_class", "created_by"),
		obj(v2, "project_id", "title", "objective", "adr_relation", "created_by"),
	}
	return schema
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
		Path:         "task/create",
		Description:  "Create a branchless train_v2 planned Task specification.",
		InputSchema:  taskAuthoringCreateSchema(),
		OutputSchema: taskAuthoringOutputSchema(),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
			IdempotentHint:  false,
		},
		AuthorityRole:       actionRolePlannerOrDelivery,
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
		Path:         "task/create_status",
		Description:  "Read the bounded durable receipt for an asynchronous task/create operation.",
		InputSchema:  obj(map[string]any{"operation_id": str("Durable task/create operation identifier.")}, "operation_id"),
		OutputSchema: taskAuthoringOutputSchema(),
		Annotations: ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
		AuthorityRole:    actionRolePlannerOrDelivery,
		LocalReceiptOnly: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var input struct {
				OperationID string `json:"operation_id"`
			}
			if err := decode(raw, &input); err != nil {
				return nil, err
			}
			return s.Service.TaskCreateOperationStatus(ctx, input.OperationID)
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
		AuthorityRole:    actionRolePlannerOrDelivery,
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
		Path:         "task/update_status",
		Description:  "Read the bounded durable receipt for an asynchronous task/update operation.",
		InputSchema:  obj(map[string]any{"operation_id": str("Durable task/update operation identifier.")}, "operation_id"),
		OutputSchema: taskAuthoringOutputSchema(),
		Annotations: ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
		AuthorityRole:    actionRolePlannerOrDelivery,
		LocalReceiptOnly: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var input struct {
				OperationID string `json:"operation_id"`
			}
			if err := decode(raw, &input); err != nil {
				return nil, err
			}
			return s.Service.TaskAuthoringUpdateOperationStatus(ctx, input.OperationID)
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
		AuthorityRole:    actionRolePlannerOrDelivery,
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
		Path:         "task/ready_status",
		Description:  "Read the bounded durable receipt for an asynchronous task/ready operation.",
		InputSchema:  obj(map[string]any{"operation_id": str("Durable task/ready operation identifier.")}, "operation_id"),
		OutputSchema: taskAuthoringOutputSchema(),
		Annotations: ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
		AuthorityRole:    actionRolePlannerOrDelivery,
		LocalReceiptOnly: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var input struct {
				OperationID string `json:"operation_id"`
			}
			if err := decode(raw, &input); err != nil {
				return nil, err
			}
			return s.Service.TaskAuthoringReadyOperationStatus(ctx, input.OperationID)
		},
	}); err != nil {
		return err
	}
	if err := s.RegisterGenericAction(GenericAction{
		Path:        "task/list",
		Description: "List bounded train_v2 Task specifications or historical Tasks.",
		InputSchema: obj(map[string]any{
			"project_id": str("Registered project identifier."), "query": str("Legacy Task search text."),
			"status": str("Optional Task status."), "limit": integer("Maximum Tasks.", 1, service.MaxTaskListLimit), "cursor": str("Legacy continuation cursor."),
		}, "project_id"),
		OutputSchema: taskAuthoringOutputSchema(),
		Annotations: ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
		AuthorityRole:       actionRolePlannerOrDelivery,
		AllowLegacyOverride: true,
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
	if err := s.RegisterGenericAction(GenericAction{
		Path:         "task/read",
		Description:  "Read a train_v2 Task specification or historical Task record.",
		InputSchema:  obj(map[string]any{"task_id": str("Stable Task identifier.")}, "task_id"),
		OutputSchema: taskAuthoringOutputSchema(),
		Annotations: ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
		AuthorityRole:       actionRolePlannerOrDelivery,
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
