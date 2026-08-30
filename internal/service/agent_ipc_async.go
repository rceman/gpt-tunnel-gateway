package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

type AgentPromptInput struct {
	ProjectID string `json:"project_id"`
	AgentID   string `json:"agent"`
	Message   string `json:"message"`
}

type AgentPromptReceipt struct {
	OperationID string             `json:"operation_id"`
	Status      string             `json:"status"`
	Result      *AgentPromptResult `json:"result,omitempty"`
	Error       string             `json:"error,omitempty"`
	CreatedAt   time.Time          `json:"created_at"`
	UpdatedAt   time.Time          `json:"updated_at"`
}

type AgentRecoveryReceipt struct {
	OperationID string               `json:"operation_id"`
	Status      string               `json:"status"`
	Result      *AgentRecoveryResult `json:"result,omitempty"`
	Error       string               `json:"error,omitempty"`
	CreatedAt   time.Time            `json:"created_at"`
	UpdatedAt   time.Time            `json:"updated_at"`
}

type AgentInterruptReceipt struct {
	OperationID string                `json:"operation_id"`
	Status      string                `json:"status"`
	Result      *AgentInterruptResult `json:"result,omitempty"`
	Error       string                `json:"error,omitempty"`
	CreatedAt   time.Time             `json:"created_at"`
	UpdatedAt   time.Time             `json:"updated_at"`
}

func agentPromptReceipt(operation durableMutationOperation) AgentPromptReceipt {
	receipt := AgentPromptReceipt{
		OperationID: operation.OperationID,
		Status:      operation.Status,
		Error:       operation.Error,
		CreatedAt:   operation.CreatedAt,
		UpdatedAt:   operation.UpdatedAt,
	}
	if operation.Status != "completed" || len(operation.Result) == 0 {
		return receipt
	}
	var result AgentPromptResult
	if err := json.Unmarshal(operation.Result, &result); err != nil {
		receipt.Status = "failed"
		receipt.Error = "invalid durable Agent prompt result"
		return receipt
	}
	receipt.Result = &result
	return receipt
}

func agentRecoveryReceipt(operation durableMutationOperation) AgentRecoveryReceipt {
	receipt := AgentRecoveryReceipt{
		OperationID: operation.OperationID,
		Status:      operation.Status,
		Error:       operation.Error,
		CreatedAt:   operation.CreatedAt,
		UpdatedAt:   operation.UpdatedAt,
	}
	if operation.Status != "completed" || len(operation.Result) == 0 {
		return receipt
	}
	var result AgentRecoveryResult
	if err := json.Unmarshal(operation.Result, &result); err != nil {
		receipt.Status = "failed"
		receipt.Error = "invalid durable Agent recovery result"
		return receipt
	}
	receipt.Result = &result
	return receipt
}

func agentInterruptReceiptFromOperation(operation durableMutationOperation) AgentInterruptReceipt {
	receipt := AgentInterruptReceipt{
		OperationID: operation.OperationID,
		Status:      operation.Status,
		Error:       operation.Error,
		CreatedAt:   operation.CreatedAt,
		UpdatedAt:   operation.UpdatedAt,
	}
	if operation.Status != "completed" || len(operation.Result) == 0 {
		return receipt
	}
	var result AgentInterruptResult
	if err := json.Unmarshal(operation.Result, &result); err != nil {
		receipt.Status = "failed"
		receipt.Error = "invalid durable Agent interrupt result"
		return receipt
	}
	receipt.Result = &result
	return receipt
}

func (s *Service) AgentPromptAsync(ctx context.Context, in AgentPromptInput) (AgentPromptReceipt, error) {
	if in.ProjectID == "" || in.Message == "" {
		return AgentPromptReceipt{}, fmt.Errorf("project_id and message are required")
	}
	operation, err := s.enqueueTypedDurableMutation(ctx, "agent-prompt", in.ProjectID, in)
	if err != nil {
		return AgentPromptReceipt{}, err
	}
	return agentPromptReceipt(operation), nil
}

func (s *Service) AgentRecoveryAsync(ctx context.Context, in AgentRecoverInput) (AgentRecoveryReceipt, error) {
	if in.ProjectID == "" {
		return AgentRecoveryReceipt{}, fmt.Errorf("project_id is required")
	}
	operation, err := s.enqueueTypedDurableMutation(ctx, "agent-recover", in.ProjectID, in)
	if err != nil {
		return AgentRecoveryReceipt{}, err
	}
	return agentRecoveryReceipt(operation), nil
}

func (s *Service) AgentInterruptAsync(ctx context.Context, in AgentInterruptInput) (AgentInterruptReceipt, error) {
	if in.ProjectID == "" || in.OperationID == "" {
		return AgentInterruptReceipt{}, fmt.Errorf("project_id and operation_id are required")
	}
	operation, err := s.enqueueTypedDurableMutation(ctx, "agent-interrupt", in.ProjectID, in)
	if err != nil {
		return AgentInterruptReceipt{}, err
	}
	return agentInterruptReceiptFromOperation(operation), nil
}

func (s *Service) AgentIPCOperationStatus(ctx context.Context, operationID, kind string) (any, error) {
	operation, err := s.readDurableMutation(operationID)
	if err != nil {
		return nil, err
	}
	if operation.Kind != kind {
		return nil, fmt.Errorf("operation is not an %s Agent IPC mutation", kind)
	}
	if sessionID := AgentSessionID(ctx); sessionID != "" && operation.SessionID != sessionID {
		return nil, fmt.Errorf("durable mutation session mismatch")
	}
	switch kind {
	case "agent-prompt":
		return agentPromptReceipt(operation), nil
	case "agent-recover":
		return agentRecoveryReceipt(operation), nil
	case "agent-interrupt":
		return agentInterruptReceiptFromOperation(operation), nil
	default:
		return nil, fmt.Errorf("unsupported Agent IPC mutation %q", kind)
	}
}
