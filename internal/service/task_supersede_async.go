package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

type TaskSupersedeInput struct {
	OldTaskID string          `json:"old_task_id"`
	Task      TaskCreateInput `json:"task"`
}

type TaskSupersedeReceipt struct {
	OperationID string          `json:"operation_id"`
	Status      string          `json:"status"`
	Task        *model.Task     `json:"task,omitempty"`
	Operation   OperationResult `json:"operation,omitempty"`
	Error       string          `json:"error,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

func taskSupersedeReceipt(operation durableMutationOperation) TaskSupersedeReceipt {
	receipt := TaskSupersedeReceipt{
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
		Task      model.Task      `json:"task"`
		Operation OperationResult `json:"operation"`
	}
	if err := json.Unmarshal(operation.Result, &result); err != nil {
		receipt.Status = "failed"
		receipt.Error = "invalid durable task supersede result"
		return receipt
	}
	receipt.Task = &result.Task
	receipt.Operation = result.Operation
	return receipt
}

func (s *Service) TaskSupersedeAsync(ctx context.Context, in TaskSupersedeInput) (TaskSupersedeReceipt, error) {
	if in.Task.ProjectID == "" {
		return TaskSupersedeReceipt{}, fmt.Errorf("task project_id is required")
	}
	operation, err := s.enqueueTypedDurableMutation(ctx, "task-supersede", in.Task.ProjectID, in)
	if err != nil {
		return TaskSupersedeReceipt{}, err
	}
	return taskSupersedeReceipt(operation), nil
}

func (s *Service) TaskSupersedeOperationStatus(ctx context.Context, operationID string) (TaskSupersedeReceipt, error) {
	operation, err := s.readDurableMutation(operationID)
	if err != nil {
		return TaskSupersedeReceipt{}, err
	}
	if operation.Kind != "task-supersede" {
		return TaskSupersedeReceipt{}, fmt.Errorf("operation is not a task supersede mutation")
	}
	if sessionID := AgentSessionID(ctx); sessionID != "" && operation.SessionID != sessionID {
		return TaskSupersedeReceipt{}, fmt.Errorf("durable mutation session mismatch")
	}
	return taskSupersedeReceipt(operation), nil
}
