package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

// TrainV2AdmissionReceipt is the bounded durable response for train/create
// and train/add. The existing admission transaction remains the worker.
type TrainV2AdmissionReceipt struct {
	OperationID string           `json:"operation_id"`
	Kind        string           `json:"kind"`
	Status      string           `json:"status"`
	Train       *model.TrainV2   `json:"train,omitempty"`
	Operation   *OperationResult `json:"operation,omitempty"`
	Error       string           `json:"error,omitempty"`
	CreatedAt   time.Time        `json:"created_at"`
	UpdatedAt   time.Time        `json:"updated_at"`
}

func trainV2AdmissionReceipt(operation durableMutationOperation) TrainV2AdmissionReceipt {
	receipt := TrainV2AdmissionReceipt{
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
	var result struct {
		Train     model.TrainV2   `json:"train"`
		Operation OperationResult `json:"operation"`
	}
	if err := json.Unmarshal(operation.Result, &result); err != nil {
		receipt.Status = "failed"
		receipt.Error = "invalid durable Train admission result"
		return receipt
	}
	receipt.Train = &result.Train
	receipt.Operation = &result.Operation
	return receipt
}

func (s *Service) TrainV2CreateAsync(ctx context.Context, in TrainV2CreateInput) (TrainV2AdmissionReceipt, error) {
	if in.CreatedBy == "" {
		return TrainV2AdmissionReceipt{}, fmt.Errorf("created_by is required")
	}
	if err := trainv2.ValidateTaskIDs(in.TaskIDs); err != nil {
		return TrainV2AdmissionReceipt{}, err
	}
	operation, err := s.enqueueTypedDurableMutation(ctx, "train-v2-create", in.ProjectID, in)
	if err != nil {
		return TrainV2AdmissionReceipt{}, err
	}
	return trainV2AdmissionReceipt(operation), nil
}

func (s *Service) TrainV2AddAsync(ctx context.Context, in TrainV2AddInput) (TrainV2AdmissionReceipt, error) {
	if _, _, err := model.ParseTrainV2ID(in.TrainID); err != nil {
		return TrainV2AdmissionReceipt{}, err
	}
	if in.AddedBy == "" || in.ExpectedRevision < 1 {
		return TrainV2AdmissionReceipt{}, fmt.Errorf("expected_revision and added_by are required")
	}
	if err := trainv2.ValidateTaskIDs(in.TaskIDs); err != nil {
		return TrainV2AdmissionReceipt{}, err
	}
	operation, err := s.enqueueTypedDurableMutation(ctx, "train-v2-add", in.ProjectID, in)
	if err != nil {
		return TrainV2AdmissionReceipt{}, err
	}
	return trainV2AdmissionReceipt(operation), nil
}

func (s *Service) TrainV2AdmissionOperationStatus(ctx context.Context, operationID, kind string) (TrainV2AdmissionReceipt, error) {
	operation, err := s.readDurableMutation(operationID)
	if err != nil {
		return TrainV2AdmissionReceipt{}, err
	}
	if operation.Kind != kind {
		return TrainV2AdmissionReceipt{}, fmt.Errorf("operation is not a %s Train admission mutation", kind)
	}
	if sessionID := AgentSessionID(ctx); sessionID != "" && operation.SessionID != sessionID {
		return TrainV2AdmissionReceipt{}, fmt.Errorf("durable mutation session mismatch")
	}
	return trainV2AdmissionReceipt(operation), nil
}
