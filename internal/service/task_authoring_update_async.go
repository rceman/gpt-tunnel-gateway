package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

// TaskAuthoringUpdateReceipt is the bounded, durable response for task/update.
// The mutation itself may continue after the caller receives this receipt.
type TaskAuthoringUpdateReceipt struct {
	OperationID string               `json:"operation_id"`
	Status      string               `json:"status"`
	Task        *model.TaskAuthoring `json:"task,omitempty"`
	Operation   OperationResult      `json:"operation,omitempty"`
	Error       string               `json:"error,omitempty"`
	CreatedAt   time.Time            `json:"created_at"`
	UpdatedAt   time.Time            `json:"updated_at"`
}

func taskAuthoringUpdateReceipt(operation durableMutationOperation) TaskAuthoringUpdateReceipt {
	receipt := TaskAuthoringUpdateReceipt{
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

func (s *Service) TaskAuthoringUpdateAsync(ctx context.Context, in TaskAuthoringUpdateInput) (TaskAuthoringUpdateReceipt, error) {
	operation, err := s.enqueueTaskAuthoringUpdate(ctx, in)
	if err != nil {
		return TaskAuthoringUpdateReceipt{}, err
	}
	return taskAuthoringUpdateReceipt(operation), nil
}

func (s *Service) TaskAuthoringUpdateOperationStatus(ctx context.Context, operationID string) (TaskAuthoringUpdateReceipt, error) {
	operation, err := s.readDurableMutation(operationID)
	if err != nil {
		return TaskAuthoringUpdateReceipt{}, err
	}
	if operation.Kind != "task-authoring-update" {
		return TaskAuthoringUpdateReceipt{}, fmt.Errorf("operation is not a task/update mutation")
	}
	if sessionID := AgentSessionID(ctx); sessionID != "" && operation.SessionID != sessionID {
		return TaskAuthoringUpdateReceipt{}, fmt.Errorf("durable mutation session mismatch")
	}
	return taskAuthoringUpdateReceipt(operation), nil
}
