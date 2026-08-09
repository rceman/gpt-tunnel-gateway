package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s *Service) DeliveryHandoffCancel(ctx context.Context, in DeliveryHandoffCancelInput) (model.DeliveryHandoff, OperationResult, error) {
	if err := authority.RequirePlanner(ctx); err != nil {
		return model.DeliveryHandoff{}, OperationResult{}, err
	}
	if strings.TrimSpace(in.CancelledBy) == "" || strings.TrimSpace(in.Reason) == "" {
		return model.DeliveryHandoff{}, OperationResult{}, fmt.Errorf("cancelled_by and reason are required")
	}
	current, err := s.findDeliveryHandoff(ctx, in.HandoffID)
	if err != nil {
		return model.DeliveryHandoff{}, OperationResult{}, err
	}
	if current.Status == model.DeliveryHandoffCompleted || current.Status == model.DeliveryHandoffCancelled || current.Status == model.DeliveryHandoffSuperseded {
		return model.DeliveryHandoff{}, OperationResult{}, fmt.Errorf("handoff cannot be cancelled from %q", current.Status)
	}
	path := s.deliveryHandoffPath(current.ProjectID, current.ID)
	now := time.Now().UTC()
	next := current
	next.Status = model.DeliveryHandoffCancelled
	next.CancelledBy = in.CancelledBy
	next.CancelReason = in.Reason
	next.CancelledAt = &now
	next.UpdatedAt = now
	tx, err := s.Hub.Transact(ctx, in.ExpectedHubRevision, "planner: cancel handoff "+current.ID, func(worktree string) ([]string, error) {
		var stored model.DeliveryHandoff
		if err := readWorktreeJSON(worktree, path, &stored); err != nil {
			return nil, err
		}
		if err := model.ValidateDeliveryHandoff(stored); err != nil {
			return nil, err
		}
		if stored.Status != current.Status || stored.UpdatedAt != current.UpdatedAt {
			return nil, fmt.Errorf("handoff changed before cancellation")
		}
		if err := hub.WriteJSON(worktree, path, next); err != nil {
			return nil, err
		}
		_, journalPaths, err := s.appendOperatorEventInWorktree(worktree, handoffJournalInput(next, "", nil, "delivery handoff cancelled", "planner"))
		if err != nil {
			return nil, err
		}
		return append([]string{path}, journalPaths...), nil
	})
	if err != nil {
		return model.DeliveryHandoff{}, OperationResult{}, err
	}
	return next, OperationResult{
		Hub:       tx,
		ProjectID: next.ProjectID,
		Status:    next.Status,
	}, nil
}
