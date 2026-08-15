package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

type WatcherNudgeMutationReceipt struct {
	OperationID string                     `json:"operation_id"`
	Status      string                     `json:"status"`
	Result      *model.WatcherNudgeReceipt `json:"result,omitempty"`
	Error       string                     `json:"error,omitempty"`
	CreatedAt   time.Time                  `json:"created_at"`
	UpdatedAt   time.Time                  `json:"updated_at"`
}

func watcherNudgeMutationReceipt(operation durableMutationOperation) WatcherNudgeMutationReceipt {
	receipt := WatcherNudgeMutationReceipt{
		OperationID: operation.OperationID,
		Status:      operation.Status,
		Error:       operation.Error,
		CreatedAt:   operation.CreatedAt,
		UpdatedAt:   operation.UpdatedAt,
	}
	if operation.Status != "completed" || len(operation.Result) == 0 {
		return receipt
	}
	var result model.WatcherNudgeReceipt
	if err := json.Unmarshal(operation.Result, &result); err != nil {
		receipt.Status = "failed"
		receipt.Error = "invalid durable watcher nudge result"
		return receipt
	}
	receipt.Result = &result
	return receipt
}

func (s *Service) WatcherNudgeAsync(ctx context.Context, in WatcherNudgeInput) (WatcherNudgeMutationReceipt, error) {
	if in.ProjectID == "" || in.Text == "" {
		return WatcherNudgeMutationReceipt{}, fmt.Errorf("project_id and text are required")
	}
	operation, err := s.enqueueTypedDurableMutation(ctx, "watcher-nudge", in.ProjectID, in)
	if err != nil {
		return WatcherNudgeMutationReceipt{}, err
	}
	return watcherNudgeMutationReceipt(operation), nil
}

func (s *Service) WatcherNudgeOperationStatus(ctx context.Context, operationID string) (WatcherNudgeMutationReceipt, error) {
	operation, err := s.readDurableMutation(operationID)
	if err != nil {
		return WatcherNudgeMutationReceipt{}, err
	}
	if operation.Kind != "watcher-nudge" {
		return WatcherNudgeMutationReceipt{}, fmt.Errorf("operation is not a watcher nudge mutation")
	}
	if sessionID := AgentSessionID(ctx); sessionID != "" && operation.SessionID != sessionID {
		return WatcherNudgeMutationReceipt{}, fmt.Errorf("durable mutation session mismatch")
	}
	return watcherNudgeMutationReceipt(operation), nil
}
