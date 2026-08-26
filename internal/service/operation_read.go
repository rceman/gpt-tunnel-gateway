package service

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/onboarding"
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

var onboardingOperationIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

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
		if !onboardingOperationIDPattern.MatchString(operationID) {
			return OperationReadResult{}, fmt.Errorf("unsupported durable operation identifier")
		}
		receipt, err := onboarding.LoadOnboardingJournal(s.Config.StateDir, operationID)
		if err != nil {
			return OperationReadResult{}, fmt.Errorf("read onboarding operation: %w", err)
		}
		createdAt, err := time.Parse(time.RFC3339Nano, receipt.Timestamps.StartedAt)
		if err != nil {
			return OperationReadResult{}, fmt.Errorf("invalid onboarding started_at: %w", err)
		}
		updatedAt, err := time.Parse(time.RFC3339Nano, receipt.Timestamps.UpdatedAt)
		if err != nil {
			return OperationReadResult{}, fmt.Errorf("invalid onboarding updated_at: %w", err)
		}
		step := ""
		if receipt.Recovery.LastDurableStep != nil {
			step = string(*receipt.Recovery.LastDurableStep)
		}
		resultBytes, err := json.Marshal(map[string]any{
			"operation_id":    receipt.OperationID,
			"project_id":      receipt.ProjectID,
			"state":           string(receipt.State),
			"recovery_status": string(receipt.Recovery.Status),
			"recovery_step":   step,
		})
		if err != nil {
			return OperationReadResult{}, fmt.Errorf("encode onboarding operation result: %w", err)
		}
		result = OperationReadResult{
			OperationID: receipt.OperationID,
			Kind:        "project-onboard",
			Status:      string(receipt.State),
			ProjectID:   receipt.ProjectID,
			Result:      resultBytes,
			CreatedAt:   createdAt,
			UpdatedAt:   updatedAt,
		}
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
