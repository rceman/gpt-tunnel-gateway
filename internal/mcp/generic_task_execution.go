package mcp

import (
	"context"
	"encoding/json"

	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func taskExecutionOutputSchema() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": true}
}

func taskWorkSchema() map[string]any {
	return obj(map[string]any{
		"project_id":            str("Server-bound project identifier."),
		"task_id":               str("Canonical Task identifier."),
		"started_by":            str("Optional server-recorded start actor."),
		"agent_id":              str("Optional server-resolved coding Agent."),
		"recommended_reasoning": str("Optional reasoning preference."),
		"expected_hub_revision": str("Optimistic Hub revision."),
	}, "project_id", "task_id")
}

func taskFinalizeSchema() map[string]any {
	return obj(map[string]any{
		"project_id":            str("Optional project hint; server resolves the canonical project from Task identity."),
		"task_id":               str("Canonical Task identifier."),
		"summary":               str("Optional bounded semantic completion summary."),
		"acceptance_coverage":   array(str("Acceptance criterion identifier.")),
		"deviations":            array(str("Bounded deviation.")),
		"remaining_risks":       array(str("Bounded remaining risk.")),
		"expected_hub_revision": str("Optimistic Hub revision."),
	}, "task_id")
}

func (s *Server) registerTaskExecutionActions() error {
	if err := s.RegisterGenericAction(GenericAction{
		Path:         "task/work",
		Description:  "Start or resume the exact current TrainItem Attempt addressed by Task identity.",
		InputSchema:  taskWorkSchema(),
		OutputSchema: taskWorkReceiptOutputSchema(),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
			IdempotentHint:  true,
		},
		LocalReceiptOnly: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in service.TaskWorkInput
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			return s.Service.TaskWorkAsync(ctx, in)
		},
	}); err != nil {
		return err
	}
	if err := s.RegisterGenericAction(GenericAction{
		Path:         "task/work_status",
		Description:  "Read the durable receipt for an asynchronous task/work operation.",
		InputSchema:  obj(map[string]any{"operation_id": str("Durable task work operation identifier.")}, "operation_id"),
		OutputSchema: taskWorkReceiptOutputSchema(),
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
			return s.Service.TaskWorkOperationStatus(ctx, input.OperationID)
		},
	}); err != nil {
		return err
	}
	if err := s.RegisterGenericAction(GenericAction{
		Path:         "task/finalize",
		Description:  "Finalize the exact current TrainItem Attempt addressed by Task identity. Leave scoped edits uncommitted; the Gateway owns gates, checkpoint commit, completion/report/proof, and no completion file is required.",
		InputSchema:  taskFinalizeSchema(),
		OutputSchema: taskFinalizeReceiptOutputSchema(),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
			IdempotentHint:  true,
		},
		LocalReceiptOnly: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in service.TaskFinalizeInput
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			return s.Service.TaskFinalizeAsync(ctx, in)
		},
	}); err != nil {
		return err
	}
	return s.RegisterGenericAction(GenericAction{
		Path:         "task/finalize_status",
		Description:  "Read the durable receipt for an asynchronous task/finalize operation.",
		InputSchema:  obj(map[string]any{"operation_id": str("Durable task finalize operation identifier.")}, "operation_id"),
		OutputSchema: taskFinalizeReceiptOutputSchema(),
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
			return s.Service.TaskFinalizeOperationStatus(ctx, input.OperationID)
		},
	})
}
