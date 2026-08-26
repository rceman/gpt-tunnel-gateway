package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

type TrainV2RetirementReceipt struct {
	OperationID string                  `json:"operation_id"`
	Kind        string                  `json:"kind"`
	Status      string                  `json:"status"`
	Retirement  *TrainV2RetireResult    `json:"retirement,omitempty"`
	Abandonment *TrainV2AbandonResult   `json:"abandonment,omitempty"`
	Reconcile   *TrainV2ReconcileResult `json:"reconcile,omitempty"`
	Error       string                  `json:"error,omitempty"`
	CreatedAt   time.Time               `json:"created_at"`
	UpdatedAt   time.Time               `json:"updated_at"`
}

func trainV2RetirementReceipt(operation durableMutationOperation) TrainV2RetirementReceipt {
	receipt := TrainV2RetirementReceipt{
		OperationID: operation.OperationID,
		Kind:        operation.Kind,
		Status:      operation.Status,
		Error:       operation.Error,
		CreatedAt:   operation.CreatedAt,
		UpdatedAt:   operation.UpdatedAt,
	}
	if operation.Status != "completed" || len(operation.Result) == 0 {
		return receipt
	}
	if operation.Kind == "train-v2-retire" {
		var result TrainV2RetireResult
		if err := json.Unmarshal(operation.Result, &result); err != nil {
			receipt.Status = "failed"
			receipt.Error = "invalid durable Train retirement result"
			return receipt
		}
		receipt.Retirement = &result
	} else if operation.Kind == "train-v2-abandon" {
		var result TrainV2AbandonResult
		if err := json.Unmarshal(operation.Result, &result); err != nil {
			receipt.Status = "failed"
			receipt.Error = "invalid durable Train abandonment result"
			return receipt
		}
		receipt.Abandonment = &result
	} else {
		var result TrainV2ReconcileResult
		if err := json.Unmarshal(operation.Result, &result); err != nil {
			receipt.Status = "failed"
			receipt.Error = "invalid durable Train reconciliation result"
			return receipt
		}
		receipt.Reconcile = &result
	}
	return receipt
}

func (s *Service) TrainV2AbandonAsync(ctx context.Context, in TrainV2AbandonInput) (TrainV2RetirementReceipt, error) {
	if _, _, err := model.ParseTrainV2ID(in.TrainID); err != nil {
		return TrainV2RetirementReceipt{}, err
	}
	operation, err := s.enqueueTypedDurableMutation(ctx, "train-v2-abandon", in.ProjectID, in)
	if err != nil {
		return TrainV2RetirementReceipt{}, err
	}
	return trainV2RetirementReceipt(operation), nil
}

func (s *Service) TrainV2RetireAsync(ctx context.Context, in TrainV2RetireInput) (TrainV2RetirementReceipt, error) {
	if _, _, err := model.ParseTrainV2ID(in.TrainID); err != nil {
		return TrainV2RetirementReceipt{}, err
	}
	operation, err := s.enqueueTypedDurableMutation(ctx, "train-v2-retire", in.ProjectID, in)
	if err != nil {
		return TrainV2RetirementReceipt{}, err
	}
	return trainV2RetirementReceipt(operation), nil
}

func (s *Service) TrainV2ReconcileAsync(ctx context.Context, in TrainV2ReconcileInput) (TrainV2RetirementReceipt, error) {
	operation, err := s.enqueueTypedDurableMutation(ctx, "train-v2-reconcile", in.ProjectID, in)
	if err != nil {
		return TrainV2RetirementReceipt{}, err
	}
	return trainV2RetirementReceipt(operation), nil
}

func (s *Service) TrainV2RetirementOperationStatus(ctx context.Context, operationID string) (TrainV2RetirementReceipt, error) {
	operation, err := s.readDurableMutation(operationID)
	if err != nil {
		return TrainV2RetirementReceipt{}, err
	}
	if operation.Kind != "train-v2-abandon" && operation.Kind != "train-v2-retire" && operation.Kind != "train-v2-reconcile" {
		return TrainV2RetirementReceipt{}, fmt.Errorf("operation is not a Train retirement mutation")
	}
	if sessionID := AgentSessionID(ctx); sessionID != "" && operation.SessionID != sessionID {
		return TrainV2RetirementReceipt{}, fmt.Errorf("durable mutation session mismatch")
	}
	return trainV2RetirementReceipt(operation), nil
}
