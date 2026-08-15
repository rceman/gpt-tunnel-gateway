package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

// WatcherGuideMutationReceipt is the bounded durable response for the active
// watcher guide Hub mutation.
type WatcherGuideMutationReceipt struct {
	OperationID string              `json:"operation_id"`
	Status      string              `json:"status"`
	Guide       *model.WatcherGuide `json:"guide,omitempty"`
	Operation   OperationResult     `json:"operation,omitempty"`
	Error       string              `json:"error,omitempty"`
	CreatedAt   time.Time           `json:"created_at"`
	UpdatedAt   time.Time           `json:"updated_at"`
}

func watcherGuideMutationReceipt(operation durableMutationOperation) WatcherGuideMutationReceipt {
	receipt := WatcherGuideMutationReceipt{
		OperationID: operation.OperationID,
		Status:      operation.Status,
		Error:       operation.Error,
		CreatedAt:   operation.CreatedAt,
		UpdatedAt:   operation.UpdatedAt,
	}
	if operation.Status != "completed" || len(operation.Result) == 0 {
		return receipt
	}
	var result struct {
		Guide     model.WatcherGuide `json:"guide"`
		Operation OperationResult    `json:"operation"`
	}
	if err := json.Unmarshal(operation.Result, &result); err != nil {
		receipt.Status = "failed"
		receipt.Error = "invalid durable watcher guide result"
		return receipt
	}
	receipt.Guide = &result.Guide
	receipt.Operation = result.Operation
	return receipt
}

func (s *Service) WatcherGuideUpdateAsync(ctx context.Context, in WatcherGuideUpdateInput) (WatcherGuideMutationReceipt, error) {
	if in.ProjectID == "" {
		return WatcherGuideMutationReceipt{}, fmt.Errorf("watcher guide project_id is required")
	}
	operation, err := s.enqueueTypedDurableMutation(ctx, "watcher-guide-update", in.ProjectID, in)
	if err != nil {
		return WatcherGuideMutationReceipt{}, err
	}
	return watcherGuideMutationReceipt(operation), nil
}

func (s *Service) WatcherGuideUpdateOperationStatus(ctx context.Context, operationID string) (WatcherGuideMutationReceipt, error) {
	operation, err := s.readDurableMutation(operationID)
	if err != nil {
		return WatcherGuideMutationReceipt{}, err
	}
	if operation.Kind != "watcher-guide-update" {
		return WatcherGuideMutationReceipt{}, fmt.Errorf("operation is not a watcher guide mutation")
	}
	if sessionID := AgentSessionID(ctx); sessionID != "" && operation.SessionID != sessionID {
		return WatcherGuideMutationReceipt{}, fmt.Errorf("durable mutation session mismatch")
	}
	return watcherGuideMutationReceipt(operation), nil
}
