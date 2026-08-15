package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

// TrainV2IntegrateReceipt is the fast initiation receipt. Phase is read from
// the existing Train integration lifecycle, not from a second state machine.
type TrainV2IntegrateReceipt struct {
	OperationID string                      `json:"operation_id"`
	Status      string                      `json:"status"`
	Phase       string                      `json:"phase,omitempty"`
	Receipt     *trainv2.IntegrationReceipt `json:"receipt,omitempty"`
	Error       string                      `json:"error,omitempty"`
	CreatedAt   time.Time                   `json:"created_at"`
	UpdatedAt   time.Time                   `json:"updated_at"`
}

func (s *Service) trainV2IntegrateReceipt(ctx context.Context, operation durableMutationOperation) TrainV2IntegrateReceipt {
	receipt := TrainV2IntegrateReceipt{OperationID: operation.OperationID, Status: operation.Status, Error: operation.Error, CreatedAt: operation.CreatedAt, UpdatedAt: operation.UpdatedAt}
	if lifecycle, err := s.readIntegrationOperation(ctx, operation.ProjectID, integrationTrainID(operation)); err == nil {
		receipt.Phase = lifecycle.Phase
	}
	if operation.Status != "completed" || len(operation.Result) == 0 {
		return receipt
	}
	var result struct {
		Receipt   trainv2.IntegrationReceipt `json:"receipt"`
		Operation OperationResult            `json:"operation"`
	}
	if err := json.Unmarshal(operation.Result, &result); err != nil {
		receipt.Status = "failed"
		receipt.Error = "invalid durable integration result"
		return receipt
	}
	receipt.Receipt = &result.Receipt
	return receipt
}

func integrationTrainID(operation durableMutationOperation) string {
	var input TrainV2IntegrateInput
	if err := json.Unmarshal(operation.Input, &input); err != nil {
		return ""
	}
	return input.TrainID
}

func (s *Service) TrainV2IntegrateAsync(ctx context.Context, in TrainV2IntegrateInput) (TrainV2IntegrateReceipt, error) {
	operation, err := s.enqueueTrainV2Integrate(ctx, in)
	if err != nil {
		return TrainV2IntegrateReceipt{}, err
	}
	return s.trainV2IntegrateReceipt(ctx, operation), nil
}

func (s *Service) TrainV2IntegrateOperationStatus(ctx context.Context, operationID string) (TrainV2IntegrateReceipt, error) {
	operation, err := s.readDurableMutation(operationID)
	if err != nil {
		return TrainV2IntegrateReceipt{}, err
	}
	if operation.Kind != "train-v2-integrate" {
		return TrainV2IntegrateReceipt{}, fmt.Errorf("operation is not a train/integrate mutation")
	}
	if sessionID := AgentSessionID(ctx); sessionID != "" && operation.SessionID != sessionID {
		return TrainV2IntegrateReceipt{}, fmt.Errorf("durable mutation session mismatch")
	}
	return s.trainV2IntegrateReceipt(ctx, operation), nil
}
