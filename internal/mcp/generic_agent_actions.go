package mcp

import (
	"context"
	"encoding/json"

	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

func (s *Server) ensureAgentActions() {
	if s.Service == nil {
		return
	}
	s.agentActions.Do(func() {
		s.agentActionErr = s.registerAgentActions()
	})
	if s.agentActionErr != nil {
		panic(s.agentActionErr)
	}
}

func agentInputSchema() map[string]any {
	return obj(map[string]any{
		"schema_version":        integer("Agent schema version.", 1, 1),
		"project_id":            str("Registered project identifier."),
		"agent_id":              str("Stable project-scoped agent identifier."),
		"role":                  str("Agent role: coding or watcher."),
		"enabled":               map[string]any{"type": "boolean"},
		"recommended_reasoning": str("Routing preference: low, medium, high, max, or best_available."),
		"capabilities":          array(str("Bounded capability identifier.")),
		"created_at":            str("Agent creation timestamp."),
		"updated_at":            str("Agent update timestamp."),
	}, "schema_version", "project_id", "agent_id", "role", "enabled", "recommended_reasoning", "capabilities", "created_at", "updated_at")
}

func agentObjectOutputSchema() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": true}
}

func (s *Server) registerAgentActions() error {
	register := func(action GenericAction) error {
		return s.RegisterGenericAction(action)
	}
	if err := register(GenericAction{
		Path:          "agent/register",
		Description:   "Register one portable project-scoped Agent identity.",
		InputSchema:   obj(map[string]any{"agent": agentInputSchema(), "expected_hub_revision": str("Optional exact Hub revision guard.")}, "agent"),
		OutputSchema:  agentObjectOutputSchema(),
		Annotations:   ToolAnnotations{DestructiveHint: true},
		AuthorityRole: actionRolePlannerOrDelivery,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in service.AgentRegisterInput
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			agent, operation, err := s.Service.AgentRegister(ctx, in)
			if err != nil {
				return nil, err
			}
			return map[string]any{"agent": agent, "operation": operation}, nil
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
		OutputSchema:  agentObjectOutputSchema(),
		Annotations:   ToolAnnotations{DestructiveHint: true},
		AuthorityRole: actionRolePlannerOrDelivery,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in service.AgentUpdateInput
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			agent, operation, err := s.Service.AgentUpdate(ctx, in)
			if err != nil {
				return nil, err
			}
			return map[string]any{"agent": agent, "operation": operation}, nil
		},
	}); err != nil {
		return err
	}
	if err := register(GenericAction{
		Path:        "agent/disable",
		Description: "Disable one registered Agent without deleting its history.",
		InputSchema: obj(map[string]any{
			"project_id": str("Registered project identifier."), "agent_id": str("Stable agent identifier."),
			"updated_by": str("Trusted mutation author."), "expected_hub_revision": str("Optional exact Hub revision guard."),
		}, "project_id", "agent_id", "updated_by"),
		OutputSchema:  agentObjectOutputSchema(),
		Annotations:   ToolAnnotations{DestructiveHint: true},
		AuthorityRole: actionRolePlannerOrDelivery,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in service.AgentDisableInput
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			agent, operation, err := s.Service.AgentDisable(ctx, in)
			if err != nil {
				return nil, err
			}
			return map[string]any{"agent": agent, "operation": operation}, nil
		},
	}); err != nil {
		return err
	}
	if err := register(GenericAction{
		Path:         "agent/read",
		Description:  "Read one portable project-scoped Agent identity.",
		InputSchema:  obj(map[string]any{"project_id": str("Registered project identifier."), "agent_id": str("Stable agent identifier.")}, "project_id", "agent_id"),
		OutputSchema: agentObjectOutputSchema(),
		Annotations:  ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
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
		Annotations:  ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
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
		Path:                "agent/status",
		Description:         "Read bounded registered, bound, and usable Agent status.",
		InputSchema:         obj(map[string]any{"project_id": str("Registered project identifier."), "agent_id": str("Stable agent identifier.")}, "project_id", "agent_id"),
		OutputSchema:        agentObjectOutputSchema(),
		Annotations:         ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
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
}
