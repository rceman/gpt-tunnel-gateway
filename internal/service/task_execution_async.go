package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

type TaskWorkReceipt struct {
	OperationID string          `json:"operation_id"`
	Status      string          `json:"status"`
	Result      *TaskWorkResult `json:"result,omitempty"`
	Error       string          `json:"error,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

type TaskFinalizeReceipt struct {
	OperationID string                        `json:"operation_id"`
	Status      string                        `json:"status"`
	Result      *TrainV2AttemptFinalizeResult `json:"result,omitempty"`
	Error       string                        `json:"error,omitempty"`
	CreatedAt   time.Time                     `json:"created_at"`
	UpdatedAt   time.Time                     `json:"updated_at"`
}

func taskWorkReceipt(operation durableMutationOperation) TaskWorkReceipt {
	receipt := TaskWorkReceipt{
		OperationID: operation.OperationID,
		Status:      operation.Status,
		Error:       operation.Error,
		CreatedAt:   operation.CreatedAt,
		UpdatedAt:   operation.UpdatedAt,
	}
	if operation.Status != "completed" || len(operation.Result) == 0 {
		return receipt
	}
	var result TaskWorkResult
	if err := json.Unmarshal(operation.Result, &result); err != nil {
		receipt.Status = "failed"
		receipt.Error = "invalid durable task work result"
		return receipt
	}
	receipt.Result = &result
	return receipt
}

func taskFinalizeReceipt(operation durableMutationOperation) TaskFinalizeReceipt {
	receipt := TaskFinalizeReceipt{
		OperationID: operation.OperationID,
		Status:      operation.Status,
		Error:       operation.Error,
		CreatedAt:   operation.CreatedAt,
		UpdatedAt:   operation.UpdatedAt,
	}
	if operation.Status != "completed" || len(operation.Result) == 0 {
		return receipt
	}
	var result TrainV2AttemptFinalizeResult
	if err := json.Unmarshal(operation.Result, &result); err != nil {
		receipt.Status = "failed"
		receipt.Error = "invalid durable task finalize result"
		return receipt
	}
	receipt.Result = &result
	return receipt
}

func (s *Service) TaskWorkAsync(ctx context.Context, in TaskWorkInput) (TaskWorkReceipt, error) {
	if in.ProjectID == "" || in.TaskID == "" {
		return TaskWorkReceipt{}, fmt.Errorf("project_id and task_id are required for asynchronous task/work")
	}
	operation, err := s.enqueueTypedDurableMutation(ctx, "task-work", in.ProjectID, in)
	if err != nil {
		return TaskWorkReceipt{}, err
	}
	return taskWorkReceipt(operation), nil
}

func (s *Service) TaskWorkOperationStatus(ctx context.Context, operationID string) (TaskWorkReceipt, error) {
	operation, err := s.readDurableMutation(operationID)
	if err != nil {
		return TaskWorkReceipt{}, err
	}
	if operation.Kind != "task-work" {
		return TaskWorkReceipt{}, fmt.Errorf("operation is not a task work mutation")
	}
	if sessionID := AgentSessionID(ctx); sessionID != "" && operation.SessionID != sessionID {
		return TaskWorkReceipt{}, fmt.Errorf("durable mutation session mismatch")
	}
	return taskWorkReceipt(operation), nil
}

func (s *Service) TaskFinalizeAsync(ctx context.Context, in TaskFinalizeInput) (TaskFinalizeReceipt, error) {
	if in.ProjectID == "" || in.TaskID == "" {
		return TaskFinalizeReceipt{}, fmt.Errorf("project_id and task_id are required for asynchronous task/finalize")
	}
	operation, err := s.enqueueTypedDurableMutation(ctx, "task-finalize", in.ProjectID, in)
	if err != nil {
		return TaskFinalizeReceipt{}, err
	}
	return taskFinalizeReceipt(operation), nil
}

func (s *Service) TaskFinalizeOperationStatus(ctx context.Context, operationID string) (TaskFinalizeReceipt, error) {
	operation, err := s.readDurableMutation(operationID)
	if err != nil {
		return TaskFinalizeReceipt{}, err
	}
	if operation.Kind != "task-finalize" {
		return TaskFinalizeReceipt{}, fmt.Errorf("operation is not a task finalize mutation")
	}
	if sessionID := AgentSessionID(ctx); sessionID != "" && operation.SessionID != sessionID {
		return TaskFinalizeReceipt{}, fmt.Errorf("durable mutation session mismatch")
	}
	return taskFinalizeReceipt(operation), nil
}
