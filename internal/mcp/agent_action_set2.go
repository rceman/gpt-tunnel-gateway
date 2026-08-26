package mcp

import (
	"context"
	"encoding/json"

	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func (s *Server) agent_action_set2() error {
	register := func(action GenericAction) error { return s.RegisterGenericAction(action) }
	if err := register(GenericAction{
		Path:        "agent/recover",
		Description: "Reconcile one exact running Train Attempt with its host-local Agent session and redeliver the server-owned packet only when safe.",
		InputSchema: obj(map[string]any{
			"project_id":     str("Registered project identifier."),
			"train_id":       str("Exact Train identifier."),
			"item_position":  integer("Zero-based TrainItem position.", 0, 1000000),
			"task_id":        str("Exact current Task identifier."),
			"attempt_number": integer("TrainItem-local Attempt number.", 1, 1000000),
			"agent_id":       str("Exact durable coding Agent identity."),
		}, "project_id", "train_id", "item_position", "task_id", "attempt_number", "agent_id"),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
			IdempotentHint:  true,
		},
		AuthorityRole:    actionRolePlannerOrDelivery,
		OutputSchema:     agentIPCReceiptOutputSchema(),
		LocalReceiptOnly: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in service.AgentRecoverInput
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			return s.Service.AgentRecoveryAsync(ctx, in)
		},
	}); err != nil {
		return err
	}
	if err := register(GenericAction{
		Path:        "agent/interrupt",
		Description: "Interrupt exactly the current TrainItem Attempt turn once.",
		InputSchema: obj(map[string]any{
			"operation_id":   str("Durable idempotency key for this interrupt operation."),
			"project_id":     str("Registered project identifier."),
			"train_id":       str("Exact Train identifier."),
			"item_position":  integer("Zero-based TrainItem position.", 0, 1000000),
			"task_id":        str("Exact current Task identifier."),
			"attempt_number": integer("TrainItem-local Attempt number.", 1, 1000000),
			"agent_id":       str("Exact current Agent identity."),
			"message":        boundedAgentMessageSchema(),
		}, "operation_id", "project_id", "train_id", "item_position", "task_id", "attempt_number", "agent_id"),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
			IdempotentHint:  true,
		},
		AuthorityRole:    actionRolePlannerOrDelivery,
		OutputSchema:     agentIPCReceiptOutputSchema(),
		LocalReceiptOnly: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in service.AgentInterruptInput
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			return s.Service.AgentInterruptAsync(ctx, in)
		},
	}); err != nil {
		return err
	}
	return nil
}
