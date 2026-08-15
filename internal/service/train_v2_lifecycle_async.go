package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

// TrainV2LifecycleReceipt is the bounded initiation/status view for a
// server-owned Train start or advance. The underlying Train state machine
// remains the sole authority for execution state.
type TrainV2LifecycleReceipt struct {
	OperationID string               `json:"operation_id"`
	Kind        string               `json:"kind"`
	Status      string               `json:"status"`
	Result      *trainv2.StartResult `json:"result,omitempty"`
	Error       string               `json:"error,omitempty"`
	CreatedAt   time.Time            `json:"created_at"`
	UpdatedAt   time.Time            `json:"updated_at"`
}

func trainV2LifecycleReceipt(operation durableMutationOperation) TrainV2LifecycleReceipt {
	receipt := TrainV2LifecycleReceipt{OperationID: operation.OperationID, Kind: operation.Kind, Status: operation.Status, Error: operation.Error, CreatedAt: operation.CreatedAt, UpdatedAt: operation.UpdatedAt}
	if operation.Status != "completed" || len(operation.Result) == 0 {
		return receipt
	}
	var result struct {
		Result trainv2.StartResult `json:"result"`
	}
	if err := json.Unmarshal(operation.Result, &result); err != nil {
		receipt.Status = "failed"
		receipt.Error = "invalid durable Train lifecycle result"
		return receipt
	}
	receipt.Result = &result.Result
	return receipt
}

func (s *Service) TrainV2StartAsync(ctx context.Context, in TrainV2StartInput) (TrainV2LifecycleReceipt, error) {
	if in.StartedBy == "" {
		return TrainV2LifecycleReceipt{}, fmt.Errorf("started_by is required")
	}
	if _, _, err := model.ParseTrainV2ID(in.TrainID); err != nil {
		return TrainV2LifecycleReceipt{}, err
	}
	operation, err := s.enqueueTypedDurableMutation(ctx, "train-v2-start", in.ProjectID, in)
	if err != nil {
		return TrainV2LifecycleReceipt{}, err
	}
	return trainV2LifecycleReceipt(operation), nil
}

func (s *Service) TrainV2StartOperationStatus(ctx context.Context, operationID string) (TrainV2LifecycleReceipt, error) {
	return s.trainV2LifecycleOperationStatus(ctx, operationID, "train-v2-start")
}

func (s *Service) TrainV2AdvanceAsync(ctx context.Context, in TrainV2AdvanceInput) (TrainV2LifecycleReceipt, error) {
	if _, _, err := model.ParseTrainV2ID(in.TrainID); err != nil {
		return TrainV2LifecycleReceipt{}, err
	}
	operation, err := s.enqueueTypedDurableMutation(ctx, "train-v2-advance", in.ProjectID, in)
	if err != nil {
		return TrainV2LifecycleReceipt{}, err
	}
	return trainV2LifecycleReceipt(operation), nil
}

func (s *Service) TrainV2AdvanceOperationStatus(ctx context.Context, operationID string) (TrainV2LifecycleReceipt, error) {
	return s.trainV2LifecycleOperationStatus(ctx, operationID, "train-v2-advance")
}

func (s *Service) trainV2LifecycleOperationStatus(ctx context.Context, operationID, kind string) (TrainV2LifecycleReceipt, error) {
	operation, err := s.readDurableMutation(operationID)
	if err != nil {
		return TrainV2LifecycleReceipt{}, err
	}
	if operation.Kind != kind {
		return TrainV2LifecycleReceipt{}, fmt.Errorf("operation is not a %s lifecycle mutation", kind)
	}
	if sessionID := AgentSessionID(ctx); sessionID != "" && operation.SessionID != sessionID {
		return TrainV2LifecycleReceipt{}, fmt.Errorf("durable mutation session mismatch")
	}
	return trainV2LifecycleReceipt(operation), nil
}
