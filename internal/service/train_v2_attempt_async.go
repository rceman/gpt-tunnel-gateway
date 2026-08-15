package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

type TrainV2AttemptFinalizeReceipt struct {
	OperationID string                        `json:"operation_id"`
	Status      string                        `json:"status"`
	Result      *TrainV2AttemptFinalizeResult `json:"result,omitempty"`
	Error       string                        `json:"error,omitempty"`
	CreatedAt   time.Time                     `json:"created_at"`
	UpdatedAt   time.Time                     `json:"updated_at"`
}

type TrainV2AttemptReviewReceipt struct {
	OperationID string                      `json:"operation_id"`
	Status      string                      `json:"status"`
	Result      *TrainV2AttemptReviewResult `json:"result,omitempty"`
	Error       string                      `json:"error,omitempty"`
	CreatedAt   time.Time                   `json:"created_at"`
	UpdatedAt   time.Time                   `json:"updated_at"`
}

func trainV2AttemptFinalizeReceipt(operation durableMutationOperation) TrainV2AttemptFinalizeReceipt {
	receipt := TrainV2AttemptFinalizeReceipt{
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
		receipt.Error = "invalid durable Train Attempt finalize result"
		return receipt
	}
	receipt.Result = &result
	return receipt
}

func trainV2AttemptReviewReceipt(operation durableMutationOperation) TrainV2AttemptReviewReceipt {
	receipt := TrainV2AttemptReviewReceipt{
		OperationID: operation.OperationID,
		Status:      operation.Status,
		Error:       operation.Error,
		CreatedAt:   operation.CreatedAt,
		UpdatedAt:   operation.UpdatedAt,
	}
	if operation.Status != "completed" || len(operation.Result) == 0 {
		return receipt
	}
	var result TrainV2AttemptReviewResult
	if err := json.Unmarshal(operation.Result, &result); err != nil {
		receipt.Status = "failed"
		receipt.Error = "invalid durable Train Attempt review result"
		return receipt
	}
	receipt.Result = &result
	return receipt
}

func (s *Service) TrainV2AttemptFinalizeAsync(ctx context.Context, in TrainV2AttemptFinalizeInput) (TrainV2AttemptFinalizeReceipt, error) {
	if in.ProjectID == "" {
		return TrainV2AttemptFinalizeReceipt{}, fmt.Errorf("project_id is required")
	}
	operation, err := s.enqueueTypedDurableMutation(ctx, "train-attempt-finalize", in.ProjectID, in)
	if err != nil {
		return TrainV2AttemptFinalizeReceipt{}, err
	}
	return trainV2AttemptFinalizeReceipt(operation), nil
}

func (s *Service) TrainV2AttemptReviewAsync(ctx context.Context, in TrainV2AttemptReviewInput) (TrainV2AttemptReviewReceipt, error) {
	if in.ProjectID == "" {
		return TrainV2AttemptReviewReceipt{}, fmt.Errorf("project_id is required")
	}
	operation, err := s.enqueueTypedDurableMutation(ctx, "train-attempt-review", in.ProjectID, in)
	if err != nil {
		return TrainV2AttemptReviewReceipt{}, err
	}
	return trainV2AttemptReviewReceipt(operation), nil
}

func (s *Service) TrainV2AttemptOperationStatus(ctx context.Context, operationID string) (any, error) {
	operation, err := s.readDurableMutation(operationID)
	if err != nil {
		return nil, err
	}
	if sessionID := AgentSessionID(ctx); sessionID != "" && operation.SessionID != sessionID {
		return nil, fmt.Errorf("durable mutation session mismatch")
	}
	switch operation.Kind {
	case "train-attempt-finalize":
		return trainV2AttemptFinalizeReceipt(operation), nil
	case "train-attempt-review":
		return trainV2AttemptReviewReceipt(operation), nil
	default:
		return nil, fmt.Errorf("operation is not a Train Attempt mutation")
	}
}
