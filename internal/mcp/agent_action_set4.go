package mcp

import (
	"context"
	"encoding/json"

	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

func (s *Server) agent_action_set4() error {
	register := func(action GenericAction) error { return s.RegisterGenericAction(action) }
	if err := register(GenericAction{
		Path:        "agent/disable",
		Description: "Disable one registered Agent without deleting its history.",
		InputSchema: obj(map[string]any{
			"project_id": str("Registered project identifier."), "agent_id": str("Stable agent identifier."),
			"updated_by": str("Trusted mutation author."), "expected_hub_revision": str("Optional exact Hub revision guard."),
		}, "project_id", "agent_id", "updated_by"),
		OutputSchema: agentMutationReceiptOutputSchema(),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
		},
		AuthorityRole: actionRolePlannerOrDelivery,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in service.AgentDisableInput
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			receipt, err := s.Service.AgentDisableAsync(ctx, in)
			if err != nil {
				return nil, err
			}
			return receipt, nil
		},
	}); err != nil {
		return err
	}
	if err := register(GenericAction{
		Path:         "agent/read",
		Description:  "Read one portable project-scoped Agent identity.",
		InputSchema:  obj(map[string]any{"project_id": str("Registered project identifier."), "agent_id": str("Stable agent identifier.")}, "project_id", "agent_id"),
		OutputSchema: agentObjectOutputSchema(),
		Annotations: ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				ProjectID string `json:"project_id"`
				AgentID   string `json:"agent_id"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			return s.Service.AgentRead(ctx, in.ProjectID, in.AgentID)
		},
	}); err != nil {
		return err
	}
	if err := register(GenericAction{
		Path:         "agent/list",
		Description:  "List project Agents in deterministic agent_id order.",
		InputSchema:  obj(map[string]any{"project_id": str("Registered project identifier.")}, "project_id"),
		OutputSchema: agentObjectOutputSchema(),
		Annotations: ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				ProjectID string `json:"project_id"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			agents, err := s.Service.AgentList(ctx, in.ProjectID)
			return map[string]any{"agents": agents}, err
		},
	}); err != nil {
		return err
	}
	return register(GenericAction{
		Path:         "agent/status",
		Description:  "Read bounded registered, bound, and usable Agent status.",
		InputSchema:  obj(map[string]any{"project_id": str("Registered project identifier."), "agent_id": str("Stable agent identifier.")}, "project_id", "agent_id"),
		OutputSchema: agentObjectOutputSchema(),
		Annotations: ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
		AuthorityRole:       durableSession.RoleDelivery,
		AllowLegacyOverride: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				ProjectID string `json:"project_id"`
				AgentID   string `json:"agent_id"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			return s.Service.AgentRegistryStatus(ctx, in.ProjectID, in.AgentID)
		},
	})
	return nil
}
