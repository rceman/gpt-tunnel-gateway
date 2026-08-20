package mcp

import (
	"context"
	"encoding/json"

	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func (s *Server) agent_action_set3() error {
	register := func(action GenericAction) error { return s.RegisterGenericAction(action) }
	if err := register(GenericAction{
		Path:         "agent/update_status",
		Description:  "Read the durable receipt for an asynchronous Agent update.",
		InputSchema:  obj(map[string]any{"operation_id": str("Durable Agent update operation identifier.")}, "operation_id"),
		OutputSchema: agentMutationReceiptOutputSchema(),
		Annotations: ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
		AuthorityRole:    actionRolePlannerOrDelivery,
		LocalReceiptOnly: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				OperationID string `json:"operation_id"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			return s.Service.AgentMutationOperationStatus(ctx, in.OperationID)
		},
	}); err != nil {
		return err
	}
	if err := register(GenericAction{
		Path:         "agent/register",
		Description:  "Register one portable project-scoped Agent identity.",
		InputSchema:  obj(map[string]any{"agent": agentInputSchema(), "expected_hub_revision": str("Optional exact Hub revision guard.")}, "agent"),
		OutputSchema: agentMutationReceiptOutputSchema(),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
		},
		AuthorityRole: actionRolePlannerOrDelivery,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in service.AgentRegisterInput
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			receipt, err := s.Service.AgentRegisterAsync(ctx, in)
			if err != nil {
				return nil, err
			}
			return receipt, nil
		},
	}); err != nil {
		return err
	}
	if err := register(GenericAction{
		Path:         "agent/disable_status",
		Description:  "Read the durable receipt for an asynchronous Agent disable.",
		InputSchema:  obj(map[string]any{"operation_id": str("Durable Agent disable operation identifier.")}, "operation_id"),
		OutputSchema: agentMutationReceiptOutputSchema(),
		Annotations: ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
		AuthorityRole:    actionRolePlannerOrDelivery,
		LocalReceiptOnly: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				OperationID string `json:"operation_id"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			return s.Service.AgentMutationOperationStatus(ctx, in.OperationID)
		},
	}); err != nil {
		return err
	}
	if err := register(GenericAction{
		Path:        "agent/update",
		Description: "Apply a typed partial update to one registered Agent.",
		InputSchema: obj(map[string]any{
			"project_id": str("Registered project identifier."), "agent_id": str("Stable agent identifier."),
			"enabled": map[string]any{"type": "boolean"}, "role": str("Agent role."),
			"recommended_reasoning": str("Routing preference."), "capabilities": array(str("Capability identifier.")),
			"updated_by": str("Trusted mutation author."), "expected_hub_revision": str("Optional exact Hub revision guard."),
		}, "project_id", "agent_id", "updated_by"),
		OutputSchema: agentMutationReceiptOutputSchema(),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
		},
		AuthorityRole: actionRolePlannerOrDelivery,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in service.AgentUpdateInput
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			receipt, err := s.Service.AgentUpdateAsync(ctx, in)
			if err != nil {
				return nil, err
			}
			return receipt, nil
		},
	}); err != nil {
		return err
	}
	return nil
}
