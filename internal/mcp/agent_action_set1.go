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
				Message   string `json:"message"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			return s.Service.AgentPromptAsync(ctx, service.AgentPromptInput{ProjectID: in.ProjectID, Message: in.Message})
		},
	}); err != nil {
		return err
	}
	return nil
}
