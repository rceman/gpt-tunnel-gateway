package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

type TrainV2CutoverOperationReceipt struct {
	OperationID string                       `json:"operation_id"`
	Status      string                       `json:"status"`
	Receipt     *model.TrainV2CutoverReceipt `json:"receipt,omitempty"`
	Operation   *OperationResult             `json:"operation,omitempty"`
	Error       string                       `json:"error,omitempty"`
	CreatedAt   time.Time                    `json:"created_at"`
	UpdatedAt   time.Time                    `json:"updated_at"`
}

func trainV2CutoverOperationReceipt(operation durableMutationOperation) TrainV2CutoverOperationReceipt {
	receipt := TrainV2CutoverOperationReceipt{OperationID: operation.OperationID, Status: operation.Status, Error: operation.Error, CreatedAt: operation.CreatedAt, UpdatedAt: operation.UpdatedAt}
	if operation.Status != "completed" || len(operation.Result) == 0 {
		return receipt
	}
	var result struct {
		Receipt   model.TrainV2CutoverReceipt `json:"receipt"`
		Operation OperationResult             `json:"operation"`
	}
	if err := json.Unmarshal(operation.Result, &result); err != nil {
		receipt.Status = "failed"
		receipt.Error = "invalid durable Train cutover result"
		return receipt
	}
	receipt.Receipt = &result.Receipt
	receipt.Operation = &result.Operation
	return receipt
}

func (s *Service) TrainV2CutoverAsync(ctx context.Context, in TrainV2CutoverInput) (TrainV2CutoverOperationReceipt, error) {
	if in.UpdatedBy == "" {
		return TrainV2CutoverOperationReceipt{}, fmt.Errorf("updated_by is required")
	}
	operation, err := s.enqueueTypedDurableMutation(ctx, "train-v2-cutover", in.ProjectID, in)
	if err != nil {
		return TrainV2CutoverOperationReceipt{}, err
	}
	return trainV2CutoverOperationReceipt(operation), nil
}

func (s *Service) TrainV2CutoverOperationStatus(ctx context.Context, operationID string) (TrainV2CutoverOperationReceipt, error) {
	operation, err := s.readDurableMutation(operationID)
	if err != nil {
		return TrainV2CutoverOperationReceipt{}, err
	}
	if operation.Kind != "train-v2-cutover" {
		return TrainV2CutoverOperationReceipt{}, fmt.Errorf("operation is not a Train cutover mutation")
	}
	if sessionID := AgentSessionID(ctx); sessionID != "" && operation.SessionID != sessionID {
		return TrainV2CutoverOperationReceipt{}, fmt.Errorf("durable mutation session mismatch")
	}
	return trainV2CutoverOperationReceipt(operation), nil
}
