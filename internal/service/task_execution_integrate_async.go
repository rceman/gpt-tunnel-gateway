package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

type TaskExecutionIntegrateReceipt struct {
	OperationID string                     `json:"-"`
	Status      string                     `json:"status"`
	Result      *TaskExecutionPublicOutput `json:"result,omitempty"`
	Error       string                     `json:"error,omitempty"`
	CreatedAt   time.Time                  `json:"created_at"`
	UpdatedAt   time.Time                  `json:"updated_at"`
}

func taskExecutionIntegrateReceipt(operation durableMutationOperation) TaskExecutionIntegrateReceipt {
	receipt := TaskExecutionIntegrateReceipt{
		OperationID: operation.OperationID,
		Status:      operation.Status,
		Error:       operation.Error,
		CreatedAt:   operation.CreatedAt,
		UpdatedAt:   operation.UpdatedAt,
	}
	if operation.Status != "completed" || len(operation.Result) == 0 {
		return receipt
	}
	var result TaskExecutionPublicOutput
	if err := json.Unmarshal(operation.Result, &result); err != nil {
		receipt.Status = "failed"
		receipt.Error = "invalid durable Task integration result"
		return receipt
	}
	receipt.Result = &result
	return receipt
}

func (s *Service) TaskExecutionIntegrateAsync(ctx context.Context, in TaskExecutionIntegrateInput) (TaskExecutionIntegrateReceipt, error) {
	if err := validateTaskExecutionReviewInput(in.ProjectID, in.Key, "code"); err != nil {
		return TaskExecutionIntegrateReceipt{}, err
	}
	operation, err := s.enqueueTypedDurableMutation(ctx, "task-execution-integrate", in.ProjectID, in)
	if err != nil {
		return TaskExecutionIntegrateReceipt{}, err
	}
	return taskExecutionIntegrateReceipt(operation), nil
}

func (s *Service) TaskExecutionIntegrateOperationStatus(ctx context.Context, operationID string) (TaskExecutionIntegrateReceipt, error) {
	operation, err := s.readDurableMutation(operationID)
	if err != nil {
		return TaskExecutionIntegrateReceipt{}, err
	}
	if operation.Kind != "task-execution-integrate" {
		return TaskExecutionIntegrateReceipt{}, fmt.Errorf("operation is not a task/integrate mutation")
	}
	if sessionID := AgentSessionID(ctx); sessionID != "" && operation.SessionID != sessionID {
		return TaskExecutionIntegrateReceipt{}, fmt.Errorf("durable mutation session mismatch")
	}
	return taskExecutionIntegrateReceipt(operation), nil
}
