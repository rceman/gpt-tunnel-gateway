package service

import (
	"context"
	"encoding/json"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
)

func (s *Service) durableMutationExecutionSet2(ctx context.Context, operation durableMutationOperation) (json.RawMessage, error) {
	switch operation.Kind {
	case "adr-create":
		var input ADRCreateInput
		if err := json.Unmarshal(operation.Input, &input); err != nil {
			return nil, err
		}
		result, err := s.ADRCreate(ctx, input)
		if err != nil {
			return nil, err
		}
		result.OperationID = operation.OperationID
		return json.Marshal(result)
	case "agent-prompt":
		var input AgentPromptInput
		if err := json.Unmarshal(operation.Input, &input); err != nil {
			return nil, err
		}
		result, err := s.AgentPromptForAgent(authority.WithPlanner(ctx), input.ProjectID, input.AgentID, input.Message)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	case "agent-recover":
		var input AgentRecoverInput
		if err := json.Unmarshal(operation.Input, &input); err != nil {
			return nil, err
		}
		result, err := s.AgentRecover(authority.WithPlanner(ctx), input)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	case "agent-interrupt":
		var input AgentInterruptInput
		if err := json.Unmarshal(operation.Input, &input); err != nil {
			return nil, err
		}
		result, err := s.AgentInterrupt(authority.WithPlanner(ctx), input)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	case "agent-update":
		var input AgentUpdateInput
		if err := json.Unmarshal(operation.Input, &input); err != nil {
			return nil, err
		}
		agent, result, err := s.AgentUpdate(authority.WithPlanner(ctx), input)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"agent": agent, "operation": result})
	}
	return nil, nil
}
