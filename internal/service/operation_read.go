package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// OperationReadResult is the common local receipt projection for every
// durable asynchronous mutation.  The operation's input and session binding
// remain private; authorization is checked before this projection is returned.
type OperationReadResult struct {
	OperationID    string          `json:"operation_id"`
	Kind           string          `json:"kind"`
	Status         string          `json:"status"`
	ProjectID      string          `json:"project_id"`
	Result         json.RawMessage `json:"result,omitempty"`
	Error          string          `json:"error,omitempty"`
	RecoveryReason string          `json:"recovery_reason,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

func (s *Service) OperationRead(ctx context.Context, operationID string) (OperationReadResult, error) {
	operation, err := s.readDurableMutation(operationID)
	if err != nil {
		return OperationReadResult{}, err
	}
	if sessionID := AgentSessionID(ctx); sessionID != "" && operation.SessionID != sessionID {
		return OperationReadResult{}, fmt.Errorf("durable mutation session mismatch")
	}
	return OperationReadResult{
		OperationID:    operation.OperationID,
		Kind:           operation.Kind,
		Status:         operation.Status,
		ProjectID:      operation.ProjectID,
		Result:         operation.Result,
		Error:          operation.Error,
		RecoveryReason: operation.RecoveryReason,
		CreatedAt:      operation.CreatedAt,
		UpdatedAt:      operation.UpdatedAt,
	}, nil
}
