package service

import (
	"context"
	"encoding/json"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
)

func (s *Service) durableMutationExecutionSet2(ctx context.Context, operation durableMutationOperation) (json.RawMessage, error) {
	switch operation.Kind {
	case "train-v2-retire":
		var input TrainV2RetireInput
		if err := json.Unmarshal(operation.Input, &input); err != nil {
			return nil, err
		}
		result, err := s.TrainV2Retire(ctx, input)
		if err != nil {
			return nil, err
		}
		result.OperationID = operation.OperationID
		return json.Marshal(result)
	case "train-v2-reconcile":
		var input TrainV2ReconcileInput
		if err := json.Unmarshal(operation.Input, &input); err != nil {
			return nil, err
		}
		result, err := s.TrainV2Reconcile(ctx, input)
		if err != nil {
			return nil, err
		}
		result.OperationID = operation.OperationID
		return json.Marshal(result)
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
	case "agent-register":
		var input AgentRegisterInput
		if err := json.Unmarshal(operation.Input, &input); err != nil {
			return nil, err
		}
		agent, result, err := s.AgentRegister(authority.WithPlannerOrDelivery(ctx), input)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"agent": agent, "operation": result})
	case "agent-prompt":
		var input AgentPromptInput
		if err := json.Unmarshal(operation.Input, &input); err != nil {
			return nil, err
		}
		result, err := s.AgentPrompt(authority.WithPlannerOrDelivery(ctx), input.ProjectID, input.Message)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	case "agent-recover":
		var input AgentRecoverInput
		if err := json.Unmarshal(operation.Input, &input); err != nil {
			return nil, err
		}
		result, err := s.AgentRecover(authority.WithPlannerOrDelivery(ctx), input)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	case "agent-interrupt":
		var input AgentInterruptInput
		if err := json.Unmarshal(operation.Input, &input); err != nil {
			return nil, err
		}
		result, err := s.AgentInterrupt(authority.WithPlannerOrDelivery(ctx), input)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	case "agent-update":
		var input AgentUpdateInput
		if err := json.Unmarshal(operation.Input, &input); err != nil {
			return nil, err
		}
		agent, result, err := s.AgentUpdate(authority.WithPlannerOrDelivery(ctx), input)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"agent": agent, "operation": result})
	}
	return nil, nil
}
