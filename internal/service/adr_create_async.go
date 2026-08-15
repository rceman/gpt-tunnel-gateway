package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// ADRCreateReceipt is the bounded durable response for adr_create.
type ADRCreateReceipt struct {
	OperationID string           `json:"operation_id"`
	Status      string           `json:"status"`
	Operation   *OperationResult `json:"operation,omitempty"`
	Error       string           `json:"error,omitempty"`
	CreatedAt   time.Time        `json:"created_at"`
	UpdatedAt   time.Time        `json:"updated_at"`
}

func adrCreateReceipt(operation durableMutationOperation) ADRCreateReceipt {
	receipt := ADRCreateReceipt{
		OperationID: operation.OperationID,
		Status:      operation.Status,
		Error:       operation.Error,
		CreatedAt:   operation.CreatedAt,
		UpdatedAt:   operation.UpdatedAt,
	}
	if operation.Status != "completed" || len(operation.Result) == 0 {
		return receipt
	}
	var result OperationResult
	if err := json.Unmarshal(operation.Result, &result); err != nil {
		receipt.Status = "failed"
		receipt.Error = "invalid durable ADR result"
		return receipt
	}
	receipt.Operation = &result
	return receipt
}

func (s *Service) ADRCreateAsync(ctx context.Context, in ADRCreateInput) (ADRCreateReceipt, error) {
	if in.ADR.ProjectID == "" {
		return ADRCreateReceipt{}, fmt.Errorf("ADR project_id is required")
	}
	operation, err := s.enqueueTypedDurableMutation(ctx, "adr-create", in.ADR.ProjectID, in)
	if err != nil {
		return ADRCreateReceipt{}, err
	}
	return adrCreateReceipt(operation), nil
}

func (s *Service) ADRCreateOperationStatus(ctx context.Context, operationID string) (ADRCreateReceipt, error) {
	operation, err := s.readDurableMutation(operationID)
	if err != nil {
		return ADRCreateReceipt{}, err
	}
	if operation.Kind != "adr-create" {
		return ADRCreateReceipt{}, fmt.Errorf("operation is not an ADR create mutation")
	}
	if sessionID := AgentSessionID(ctx); sessionID != "" && operation.SessionID != sessionID {
		return ADRCreateReceipt{}, fmt.Errorf("durable mutation session mismatch")
	}
	return adrCreateReceipt(operation), nil
}
