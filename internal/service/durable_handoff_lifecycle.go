package service

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/pagination"
)

func deliveryHandoffStatusProjection(item model.DeliveryHandoff, summary model.OwnerSummary) model.DeliveryHandoffStatus {
	return model.DeliveryHandoffStatus{SchemaVersion: item.SchemaVersion, ID: item.ID, ProjectID: item.ProjectID, TaskID: item.TaskID, RunID: item.RunID, Status: item.Status, OwnerSummary: summary, CurrentReportID: item.CurrentReportID, SupersedesHandoffID: item.SupersedesHandoffID, SupersededByHandoffID: item.SupersededByHandoffID, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt}
}

func (s *Service) DeliveryHandoffStatus(ctx context.Context, id string) (model.DeliveryHandoffStatus, error) {
	handoff, err := s.findDeliveryHandoff(ctx, id)
	if err != nil {
		return model.DeliveryHandoffStatus{}, err
	}
	summary, err := s.deliveryHandoffStatusSummary(ctx, handoff)
	if err != nil {
		return model.DeliveryHandoffStatus{}, err
	}
	return deliveryHandoffStatusProjection(handoff, summary), nil
}

func (s *Service) DeliveryHandoffList(ctx context.Context, in DeliveryHandoffListInput) ([]model.DeliveryHandoffStatus, error) {
	limit, err := boundedDurableListLimit(in.Limit, s.Config.MaxListItems)
	if err != nil {
		return nil, err
	}
	paths, err := s.Hub.List(ctx, s.deliveryHandoffPrefix(in.ProjectID), ".json")
	if err != nil {
		return nil, err
	}
	items := make([]model.DeliveryHandoffStatus, 0, len(paths))
	for _, path := range paths {
		var item model.DeliveryHandoff
		if err := s.Hub.ReadJSON(ctx, path, &item); err != nil {
			return nil, err
		}
		if err := model.ValidateDeliveryHandoff(item); err != nil {
			return nil, err
		}
		summary, err := s.deliveryHandoffStatusSummary(ctx, item)
		if err != nil {
			return nil, err
		}
		items = append(items, deliveryHandoffStatusProjection(item, summary))
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].UpdatedAt.Equal(items[j].UpdatedAt) {
			return items[i].ID > items[j].ID
		}
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
	})
	if len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func (s *Service) DeliveryHandoffListPage(ctx context.Context, in DeliveryHandoffListInput) (DeliveryHandoffListPageResult, error) {
	limit, err := publicDurableListLimit(in.Limit, s.Config.MaxListItems)
	if err != nil {
		return DeliveryHandoffListPageResult{}, err
	}
	items, err := s.DeliveryHandoffList(ctx, DeliveryHandoffListInput{
		ProjectID: in.ProjectID,
		Limit:     s.Config.MaxListItems,
	})
	if err != nil {
		return DeliveryHandoffListPageResult{}, err
	}
	page, info, err := pagination.Page("delivery_handoff_list:"+in.ProjectID, items, limit, in.Cursor, func(item model.DeliveryHandoffStatus) string {
		return item.UpdatedAt.UTC().Format(time.RFC3339Nano) + "|" + item.ID
	})
	if err != nil {
		return DeliveryHandoffListPageResult{}, err
	}
	return DeliveryHandoffListPageResult{
		Handoffs:   page,
		NextCursor: info.NextCursor,
		HasMore:    info.HasMore,
	}, nil
}

func (s *Service) DeliveryHandoffAcknowledge(ctx context.Context, in DeliveryHandoffAcknowledgeInput) (model.DeliveryHandoff, OperationResult, error) {
	if err := authority.RequireDelivery(ctx); err != nil {
		return model.DeliveryHandoff{}, OperationResult{}, err
	}
	current, err := s.findDeliveryHandoff(ctx, in.HandoffID)
	if err != nil {
		return model.DeliveryHandoff{}, OperationResult{}, err
	}
	if current.Status != model.DeliveryHandoffPending {
		return model.DeliveryHandoff{}, OperationResult{}, fmt.Errorf("handoff cannot be acknowledged from %q", current.Status)
	}
	if strings.TrimSpace(in.AcknowledgedBy) == "" {
		return model.DeliveryHandoff{}, OperationResult{}, fmt.Errorf("acknowledged_by is required")
	}
	path := s.deliveryHandoffPath(current.ProjectID, current.ID)
	now := s.handoffNow(current)
	next := current
	next.Status = model.DeliveryHandoffAcknowledged
	next.AcknowledgedBy = in.AcknowledgedBy
	next.AcknowledgedAt = &now
	next.UpdatedAt = now
	tx, err := s.Hub.Transact(ctx, in.ExpectedHubRevision, "delivery: acknowledge handoff "+current.ID, func(worktree string) ([]string, error) {
		var stored model.DeliveryHandoff
		if err := readWorktreeJSON(worktree, path, &stored); err != nil {
			return nil, err
		}
		if err := model.ValidateDeliveryHandoff(stored); err != nil {
			return nil, err
		}
		if stored.Status != model.DeliveryHandoffPending || stored.ID != current.ID {
			return nil, fmt.Errorf("handoff changed before acknowledgement")
		}
		if err := hub.WriteJSON(worktree, path, next); err != nil {
			return nil, err
		}
		_, journalPaths, err := s.appendOperatorEventInWorktree(worktree, handoffJournalInput(next, "", nil, "delivery handoff acknowledged", "delivery"))
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

// DeliveryHandoffNext claims the exact acknowledged handoff for Delivery and
// advances it into the only state from which a report may be published.
func (s *Service) DeliveryHandoffNext(ctx context.Context, in DeliveryHandoffNextInput) (model.DeliveryHandoff, OperationResult, error) {
	if err := authority.RequireDelivery(ctx); err != nil {
		return model.DeliveryHandoff{}, OperationResult{}, err
	}
	if strings.TrimSpace(in.NextBy) == "" {
		return model.DeliveryHandoff{}, OperationResult{}, fmt.Errorf("next_by is required")
	}
	current, err := s.findDeliveryHandoff(ctx, in.HandoffID)
	if err != nil {
		return model.DeliveryHandoff{}, OperationResult{}, err
	}
	if current.Status != model.DeliveryHandoffAcknowledged {
		return model.DeliveryHandoff{}, OperationResult{}, fmt.Errorf("handoff cannot advance from %q", current.Status)
	}
	path := s.deliveryHandoffPath(current.ProjectID, current.ID)
	now := s.handoffNow(current)
	next := current
	next.Status = model.DeliveryHandoffInProgress
	next.StartedBy = in.NextBy
	next.StartedAt = &now
	next.UpdatedAt = now
	tx, err := s.Hub.Transact(ctx, in.ExpectedHubRevision, "delivery: advance handoff "+current.ID, func(worktree string) ([]string, error) {
		var stored model.DeliveryHandoff
		if err := readWorktreeJSON(worktree, path, &stored); err != nil {
			return nil, err
		}
		if err := model.ValidateDeliveryHandoff(stored); err != nil {
			return nil, err
		}
		if stored.Status != model.DeliveryHandoffAcknowledged || stored.UpdatedAt != current.UpdatedAt {
			return nil, fmt.Errorf("handoff changed before advancement")
		}
		if err := hub.WriteJSON(worktree, path, next); err != nil {
			return nil, err
		}
		_, journalPaths, err := s.appendOperatorEventInWorktree(worktree, handoffJournalInput(next, "", nil, "delivery handoff advanced to in_progress", "delivery"))
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
