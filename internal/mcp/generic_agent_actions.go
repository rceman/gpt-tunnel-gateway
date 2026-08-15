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

func agentPromptOutputSchema() map[string]any {
	return map[string]any{
		"oneOf": []any{
			closedOutput(map[string]any{
				"project_id": outputString(),
				"delivered":  map[string]any{"type": "boolean", "const": true},
			}, "project_id", "delivered"),
			closedOutput(map[string]any{
				"project_id": outputString(),
				"delivered":  map[string]any{"type": "boolean", "const": false},
				"exit_code":  outputInteger(),
				"stderr":     outputString(),
				"error":      outputString(),
			}, "project_id", "delivered"),
		},
	}
}

func (s *Server) registerAgentActions() error {
	register := func(action GenericAction) error {
		return s.RegisterGenericAction(action)
	}
	if err := register(GenericAction{
		Path:        "agent/prompt",
		Description: "Send one bounded non-interrupting prompt to the exact project-bound Agent session.",
		InputSchema: obj(map[string]any{
			"project_id": str("Registered project identifier."),
			"message":    boundedAgentMessageSchema(),
		}, "project_id", "message"),
		OutputSchema: agentPromptOutputSchema(),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
			IdempotentHint:  false,
		},
		AuthorityRole: actionRolePlannerOrDelivery,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				ProjectID string `json:"project_id"`
				Message   string `json:"message"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			return s.Service.AgentPrompt(ctx, in.ProjectID, in.Message)
		},
	}); err != nil {
		return err
	}
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
		OutputSchema: agentObjectOutputSchema(),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
			IdempotentHint:  true,
		},
		AuthorityRole: actionRolePlannerOrDelivery,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in service.AgentRecoverInput
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			return s.Service.AgentRecover(ctx, in)
		},
	}); err != nil {
		return err
	}
	if err := register(GenericAction{
		Path:         "agent/register_status",
		Description:  "Read the durable receipt for an asynchronous Agent registration.",
		InputSchema:  obj(map[string]any{"operation_id": str("Durable Agent registration operation identifier.")}, "operation_id"),
		OutputSchema: agentMutationReceiptOutputSchema(),
		Annotations: ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
		AuthorityRole: actionRolePlannerOrDelivery,
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
		OutputSchema: agentInterruptOutputSchema(),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
			IdempotentHint:  true,
		},
		AuthorityRole: actionRolePlannerOrDelivery,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in service.AgentInterruptInput
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			return s.Service.AgentInterrupt(ctx, in)
		},
	}); err != nil {
		return err
	}
	if err := register(GenericAction{
		Path:         "agent/update_status",
		Description:  "Read the durable receipt for an asynchronous Agent update.",
		InputSchema:  obj(map[string]any{"operation_id": str("Durable Agent update operation identifier.")}, "operation_id"),
		OutputSchema: agentMutationReceiptOutputSchema(),
		Annotations: ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
		AuthorityRole: actionRolePlannerOrDelivery,
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
		AuthorityRole: actionRolePlannerOrDelivery,
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
}

func boundedAgentMessageSchema() map[string]any {
	message := str("Bounded non-interrupting Agent prompt.")
	message["minLength"], message["maxLength"] = 1, 256
	return message
}

func agentInterruptOutputSchema() map[string]any {
	return obj(map[string]any{
		"operation_id": str("Durable interrupt operation identity."), "project_id": str("Project identity."), "train_id": str("Train identity."),
		"item_position": integer("TrainItem position.", 0, 1000000), "task_id": str("Task identity."), "attempt_number": integer("Attempt number.", 1, 1000000),
		"agent_id": str("Agent identity."), "outcome": outputEnum("interrupt_acknowledged", "already_idle", "timed_out", "failed", "turn_changed", "unsupported", "stale_execution", "in_flight"),
		"interrupt_outcome": outputString(), "prompt_outcome": outputString(), "requested": map[string]any{"type": "boolean"}, "prompt_delivered": map[string]any{"type": "boolean"},
		"elapsed_ms": outputInteger(), "error": outputString(), "reason": outputString(), "started_at": outputDateTime(), "finished_at": outputDateTime(),
	}, "operation_id", "project_id", "train_id", "item_position", "task_id", "attempt_number", "agent_id", "outcome", "requested", "started_at", "finished_at")
}
