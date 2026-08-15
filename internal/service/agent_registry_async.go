package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

// AgentMutationReceipt is the bounded durable response shared by Agent
// register, update, and disable mutations.
type AgentMutationReceipt struct {
	OperationID string          `json:"operation_id"`
	Status      string          `json:"status"`
	Agent       *model.Agent    `json:"agent,omitempty"`
	Operation   OperationResult `json:"operation,omitempty"`
	Error       string          `json:"error,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

func agentMutationReceipt(operation durableMutationOperation) AgentMutationReceipt {
	receipt := AgentMutationReceipt{
		OperationID: operation.OperationID,
		Status:      operation.Status,
		Error:       operation.Error,
		CreatedAt:   operation.CreatedAt,
		UpdatedAt:   operation.UpdatedAt,
	}
	if operation.Status != "completed" || len(operation.Result) == 0 {
		return receipt
	}
	var result struct {
		Agent     model.Agent     `json:"agent"`
		Operation OperationResult `json:"operation"`
	}
	if err := json.Unmarshal(operation.Result, &result); err != nil {
		receipt.Status = "failed"
		receipt.Error = "invalid durable Agent result"
		return receipt
	}
	receipt.Agent = &result.Agent
	receipt.Operation = result.Operation
	return receipt
}

func (s *Service) AgentRegisterAsync(ctx context.Context, in AgentRegisterInput) (AgentMutationReceipt, error) {
	if err := s.requireAgentMutation(ctx); err != nil {
		return AgentMutationReceipt{}, err
	}
	if in.Agent.ProjectID == "" {
		return AgentMutationReceipt{}, fmt.Errorf("agent project_id is required")
	}
	operation, err := s.enqueueTypedDurableMutation(ctx, "agent-register", in.Agent.ProjectID, in)
	if err != nil {
		return AgentMutationReceipt{}, err
	}
	return agentMutationReceipt(operation), nil
}

func (s *Service) AgentUpdateAsync(ctx context.Context, in AgentUpdateInput) (AgentMutationReceipt, error) {
	if err := s.requireAgentMutation(ctx); err != nil {
		return AgentMutationReceipt{}, err
	}
	operation, err := s.enqueueTypedDurableMutation(ctx, "agent-update", in.ProjectID, in)
	if err != nil {
		return AgentMutationReceipt{}, err
	}
	return agentMutationReceipt(operation), nil
}

func (s *Service) AgentDisableAsync(ctx context.Context, in AgentDisableInput) (AgentMutationReceipt, error) {
	if err := s.requireAgentMutation(ctx); err != nil {
		return AgentMutationReceipt{}, err
	}
	operation, err := s.enqueueTypedDurableMutation(ctx, "agent-disable", in.ProjectID, in)
	if err != nil {
		return AgentMutationReceipt{}, err
	}
	return agentMutationReceipt(operation), nil
}

func (s *Service) AgentMutationOperationStatus(ctx context.Context, operationID string) (AgentMutationReceipt, error) {
	operation, err := s.readDurableMutation(operationID)
	if err != nil {
		return AgentMutationReceipt{}, err
	}
	switch operation.Kind {
	case "agent-register", "agent-update", "agent-disable":
	default:
		return AgentMutationReceipt{}, fmt.Errorf("operation is not an Agent registry mutation")
	}
	if sessionID := AgentSessionID(ctx); sessionID != "" && operation.SessionID != sessionID {
		return AgentMutationReceipt{}, fmt.Errorf("durable mutation session mismatch")
	}
	return agentMutationReceipt(operation), nil
}
