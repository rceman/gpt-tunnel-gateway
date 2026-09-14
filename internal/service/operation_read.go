package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
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
	if err := model.ValidateOperationID(operationID); err != nil {
		return OperationReadResult{}, fmt.Errorf("operation/read requires a canonical operation key: %w", err)
	}
	result, err := s.readCanonicalOperation(operationID)
	if err != nil {
		return OperationReadResult{}, err
	}
	return s.authorizeOperationResult(ctx, result)
}

func (s *Service) OperationAwait(ctx context.Context, operationID string, requested time.Duration) (OperationReadResult, error) {
	if err := model.ValidateOperationID(operationID); err != nil {
		return OperationReadResult{}, fmt.Errorf("operation/await requires a canonical operation key: %w", err)
	}
	wait := requested
	if wait <= 0 {
		wait = 30 * time.Second
	}
	if wait > 60*time.Second {
		wait = 60 * time.Second
	}
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	result, err := s.readCanonicalOperation(operationID)
	if err != nil {
		return OperationReadResult{}, err
	}
	if _, err := s.authorizeOperationResult(ctx, result); err != nil {
		return OperationReadResult{}, err
	}
	for {
		if operationTerminal(result.Status) {
			return result, nil
		}
		select {
		case <-ctx.Done():
			return result, nil
		case <-deadline.C:
			return result, nil
		case <-ticker.C:
			result, err = s.readCanonicalOperation(operationID)
			if err != nil {
				return OperationReadResult{}, err
			}
			if _, err := s.authorizeOperationResult(ctx, result); err != nil {
				return OperationReadResult{}, err
			}
		}
	}
}

func (s *Service) readCanonicalOperation(operationID string) (OperationReadResult, error) {
	if s.Durability == nil {
		return OperationReadResult{}, fmt.Errorf("local durability is unavailable")
	}
	operation, err := s.Durability.ReadLocalOperation(context.Background(), operationID)
	if err != nil {
		return OperationReadResult{}, err
	}
	return OperationReadResult{
		OperationID:    operation.OperationID,
		Kind:           operation.Kind,
		Status:         operation.Status,
		ProjectID:      operation.ProjectID,
		Result:         append(json.RawMessage(nil), operation.ResultPayload...),
		Error:          operation.Error,
		RecoveryReason: operation.RecoveryReason,
		CreatedAt:      operation.CreatedAt,
		UpdatedAt:      operation.UpdatedAt,
	}, nil
}

func (s *Service) authorizeOperationResult(ctx context.Context, result OperationReadResult) (OperationReadResult, error) {
	sessionID := AgentSessionID(ctx)
	if sessionID == "" {
		return OperationReadResult{}, fmt.Errorf("durable operation session is required")
	}
	session, err := durableSession.NewStoreWithDurability(s.Durability).Get(sessionID)
	if err != nil {
		return OperationReadResult{}, fmt.Errorf("read bound durable session: %w", err)
	}
	if session.Status != durableSession.StatusActive || session.ProjectID == "" || result.ProjectID != session.ProjectID {
		return OperationReadResult{}, fmt.Errorf("durable operation is outside the bound project scope")
	}
	return result, nil
}

func operationTerminal(status string) bool {
	switch status {
	case "completed", "failed", "outcome_unknown":
		return true
	default:
		return false
	}
}
