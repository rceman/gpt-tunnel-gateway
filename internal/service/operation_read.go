package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
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
	var result OperationReadResult
	switch {
	case strings.HasPrefix(operationID, "mutation-"):
		operation, err := s.readDurableMutation(operationID)
		if err != nil {
			return OperationReadResult{}, err
		}
		result = OperationReadResult{
			OperationID:    operation.OperationID,
			Kind:           operation.Kind,
			Status:         operation.Status,
			ProjectID:      operation.ProjectID,
			Result:         operation.Result,
			Error:          operation.Error,
			RecoveryReason: operation.RecoveryReason,
			CreatedAt:      operation.CreatedAt,
			UpdatedAt:      operation.UpdatedAt,
		}
	case strings.HasPrefix(operationID, "task-create-"):
		operation, err := s.TaskCreateOperationRead(ctx, operationID)
		if err != nil {
			return OperationReadResult{}, err
		}
		resultBytes, err := json.Marshal(operation.Receipt())
		if err != nil {
			return OperationReadResult{}, fmt.Errorf("encode task/create operation result: %w", err)
		}
		result = OperationReadResult{
			OperationID: operation.OperationID,
			Kind:        "task-create",
			Status:      operation.Status,
			ProjectID:   operation.Input.ProjectID,
			Result:      resultBytes,
			Error:       operation.Error,
			CreatedAt:   operation.CreatedAt,
			UpdatedAt:   operation.UpdatedAt,
		}
	case strings.HasPrefix(operationID, "verify-"):
		receipt, err := s.VerifyStatus(ctx, operationID)
		if err != nil {
			return OperationReadResult{}, err
		}
		resultBytes, err := json.Marshal(receipt)
		if err != nil {
			return OperationReadResult{}, fmt.Errorf("encode verify operation result: %w", err)
		}
		result = OperationReadResult{
			OperationID: receipt.OperationID,
			Kind:        "verify",
			Status:      receipt.Status,
			ProjectID:   receipt.ProjectID,
			Result:      resultBytes,
			Error:       receipt.Error,
			CreatedAt:   receipt.CreatedAt,
			UpdatedAt:   receipt.UpdatedAt,
		}
	default:
		return OperationReadResult{}, fmt.Errorf("unsupported durable operation identifier")
	}
	sessionID := AgentSessionID(ctx)
	if sessionID == "" {
		return OperationReadResult{}, fmt.Errorf("durable mutation session is required")
	}
	session, err := durableSession.NewStore(s.Config.StateDir).Get(sessionID)
	if err != nil {
		return OperationReadResult{}, fmt.Errorf("read bound durable session: %w", err)
	}
	if session.Status != durableSession.StatusActive || session.ProjectID == "" || result.ProjectID != session.ProjectID {
		return OperationReadResult{}, fmt.Errorf("durable mutation is outside the bound project scope")
	}
	return result, nil
}
