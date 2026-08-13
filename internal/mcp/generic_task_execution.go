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
		"project_id":            str("Server-bound project identifier."),
		"task_id":               str("Canonical Task identifier."),
		"expected_hub_revision": str("Optimistic Hub revision."),
	}, "project_id", "task_id")
}

func (s *Server) registerTaskExecutionActions() error {
	if err := s.RegisterGenericAction(GenericAction{
		Path:         "task/work",
		Description:  "Start or resume the exact current TrainItem Attempt addressed by Task identity.",
		InputSchema:  taskWorkSchema(),
		OutputSchema: taskExecutionOutputSchema(),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
			IdempotentHint:  true,
		},
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in service.TaskWorkInput
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			return s.Service.TaskWork(ctx, in)
		},
	}); err != nil {
		return err
	}
	return s.RegisterGenericAction(GenericAction{
		Path:         "task/finalize",
		Description:  "Finalize the exact current TrainItem Attempt addressed by Task identity.",
		InputSchema:  taskFinalizeSchema(),
		OutputSchema: taskExecutionOutputSchema(),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
			IdempotentHint:  true,
		},
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in service.TaskFinalizeInput
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			return s.Service.TaskFinalize(ctx, in)
		},
	})
}
