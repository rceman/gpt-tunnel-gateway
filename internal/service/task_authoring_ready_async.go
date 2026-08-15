package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

// TaskAuthoringReadyReceipt is the bounded durable response for task/ready.
type TaskAuthoringReadyReceipt struct {
	OperationID string               `json:"operation_id"`
	Status      string               `json:"status"`
	Task        *model.TaskAuthoring `json:"task,omitempty"`
	Operation   OperationResult      `json:"operation,omitempty"`
	Error       string               `json:"error,omitempty"`
	CreatedAt   time.Time            `json:"created_at"`
	UpdatedAt   time.Time            `json:"updated_at"`
}

func taskAuthoringReadyReceipt(operation durableMutationOperation) TaskAuthoringReadyReceipt {
	receipt := TaskAuthoringReadyReceipt{OperationID: operation.OperationID, Status: operation.Status, Error: operation.Error, CreatedAt: operation.CreatedAt, UpdatedAt: operation.UpdatedAt}
	if operation.Status != "completed" || len(operation.Result) == 0 {
		return receipt
	}
	var result struct {
		Task      model.TaskAuthoring `json:"task"`
		Operation OperationResult     `json:"operation"`
	}
	if err := json.Unmarshal(operation.Result, &result); err != nil {
		receipt.Status = "failed"
		receipt.Error = "invalid durable mutation result"
		return receipt
	}
	receipt.Task = &result.Task
	receipt.Operation = result.Operation
	return receipt
}

func (s *Service) TaskAuthoringReadyAsync(ctx context.Context, in TaskAuthoringReadyInput) (TaskAuthoringReadyReceipt, error) {
	operation, err := s.enqueueTaskAuthoringReady(ctx, in)
	if err != nil {
		return TaskAuthoringReadyReceipt{}, err
	}
	return taskAuthoringReadyReceipt(operation), nil
}

func (s *Service) TaskAuthoringReadyOperationStatus(ctx context.Context, operationID string) (TaskAuthoringReadyReceipt, error) {
	operation, err := s.readDurableMutation(operationID)
	if err != nil {
		return TaskAuthoringReadyReceipt{}, err
	}
	if operation.Kind != "task-authoring-ready" {
		return TaskAuthoringReadyReceipt{}, fmt.Errorf("operation is not a task/ready mutation")
	}
	if sessionID := AgentSessionID(ctx); sessionID != "" && operation.SessionID != sessionID {
		return TaskAuthoringReadyReceipt{}, fmt.Errorf("durable mutation session mismatch")
	}
	return taskAuthoringReadyReceipt(operation), nil
}
