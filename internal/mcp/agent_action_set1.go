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
		Description: "Send one bounded prompt to the server-selected Agent.",
		InputSchema: canonicalAgentPromptInputSchema(),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
			IdempotentHint:  false,
		},
		AuthorityRole:    actionRolePlannerOrDelivery,
		OutputSchema:     canonicalAgentOperationOutputSchema(),
		LocalReceiptOnly: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				Agent   string `json:"agent"`
				Message string `json:"message"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			if err := validateCanonicalAgentMessage(in.Message); err != nil {
				return nil, err
			}
			projectID, err := s.boundAgentProject(ctx)
			if err != nil {
				return nil, err
			}
			target, err := s.resolveCanonicalAgent(ctx, projectID, in.Agent, true)
			if err != nil {
				return nil, err
			}
			receipt, err := s.Service.AgentPromptAsync(ctx, service.AgentPromptInput{ProjectID: projectID, AgentID: target.Agent.AgentID, Message: in.Message})
			if err != nil {
				return nil, err
			}
			return map[string]any{"operation": receipt.OperationID, "status": receipt.Status}, nil
		},
	}); err != nil {
		return err
	}
	return nil
}
