package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func (s *Server) agent_action_set2() error {
	return s.RegisterGenericAction(GenericAction{
		Path:        "agent/interrupt",
		Description: "Interrupt the current Agent turn and optionally submit a replacement message.",
		InputSchema: canonicalAgentInterruptInputSchema(),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
			IdempotentHint:  true,
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
			if err := validateOptionalCanonicalAgentMessage(in.Message); err != nil {
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
			operational, err := s.Service.ProjectOperationalStatus(ctx)
			if err != nil {
				return nil, err
			}
			if operational.TrainID == "" || operational.TaskID == "" || operational.AttemptNumber == 0 || operational.Agent.AgentID != target.Agent.AgentID {
				return nil, fmt.Errorf("no active execution for Agent %q", target.Agent.AgentID)
			}
			operationID, err := newCanonicalAgentOperationID()
			if err != nil {
				return nil, err
			}
			receipt, err := s.Service.AgentInterruptAsync(ctx, service.AgentInterruptInput{
				OperationID:   operationID,
				ProjectID:     projectID,
				TrainID:       operational.TrainID,
				ItemPosition:  operational.ItemPosition,
				TaskID:        operational.TaskID,
				AttemptNumber: operational.AttemptNumber,
				AgentID:       target.Agent.AgentID,
				Message:       in.Message,
			})
			if err != nil {
				return nil, err
			}
			return map[string]any{"operation": receipt.OperationID, "status": receipt.Status}, nil
		},
	})
}
