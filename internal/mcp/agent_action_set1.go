package mcp

import (
	"context"
	"encoding/json"

	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func (s *Server) agent_action_set1() error {
	register := func(action GenericAction) error { return s.RegisterGenericAction(action) }
	if err := register(GenericAction{
		Path:        "agent/prompt",
		Description: "Send one bounded non-interrupting prompt to the exact project-bound Agent session.",
		InputSchema: obj(map[string]any{
			"project_id": str("Registered project identifier."),
			"title":      str("Bounded prompt title."),
			"message":    boundedAgentMessageSchema(),
		}, "project_id", "message"),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
			IdempotentHint:  false,
		},
		AuthorityRole:    actionRolePlannerOrDelivery,
		OutputSchema:     agentIPCReceiptOutputSchema(),
		LocalReceiptOnly: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				ProjectID string `json:"project_id"`
				Title     string `json:"title"`
				Message   string `json:"message"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			return s.Service.AgentPromptAsync(ctx, service.AgentPromptInput{ProjectID: in.ProjectID, Title: in.Title, Message: in.Message})
		},
	}); err != nil {
		return err
	}
	if err := register(GenericAction{
		Path:         "agent/prompt_status",
		Description:  "Read the durable receipt for an asynchronous Agent prompt.",
		InputSchema:  obj(map[string]any{"operation_id": str("Durable Agent prompt operation identifier.")}, "operation_id"),
		OutputSchema: agentIPCReceiptOutputSchema(),
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
			return s.Service.AgentIPCOperationStatus(ctx, input.OperationID, "agent-prompt")
		},
	}); err != nil {
		return err
	}
	if err := register(GenericAction{
		Path:         "agent/recover_status",
		Description:  "Read the durable receipt for asynchronous Agent recovery.",
		InputSchema:  obj(map[string]any{"operation_id": str("Durable Agent recovery operation identifier.")}, "operation_id"),
		OutputSchema: agentIPCReceiptOutputSchema(),
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
			return s.Service.AgentIPCOperationStatus(ctx, input.OperationID, "agent-recover")
		},
	}); err != nil {
		return err
	}
	return nil
}
