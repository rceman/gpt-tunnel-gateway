package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s *Service) TrainV2Abandon(ctx context.Context, in TrainV2AbandonInput) (TrainV2AbandonResult, error) {
	if err := authority.RequirePlanner(ctx); err != nil {
		return TrainV2AbandonResult{}, err
	}
	if err := requireTrainV2Authoring(ctx, s, in.ProjectID); err != nil {
		return TrainV2AbandonResult{}, err
	}
	if _, _, err := model.ParseTrainV2ID(in.TrainID); err != nil {
		return TrainV2AbandonResult{}, err
	}
	if strings.TrimSpace(in.Reason) == "" || strings.ContainsAny(in.Reason, "\x00\r\n") || len(in.Reason) > 512 {
		return TrainV2AbandonResult{}, fmt.Errorf("bounded abandonment reason is required")
	}
	actor := AgentSessionID(ctx)
	if actor == "" {
		return TrainV2AbandonResult{}, fmt.Errorf("abandonment requires a bound Planner session")
	}
	current, err := s.TrainV2Read(ctx, in.ProjectID, in.TrainID)
	if err != nil {
		return TrainV2AbandonResult{}, err
	}
	if err := rejectTrainV2AbandonStatus(current); err != nil {
		return TrainV2AbandonResult{}, err
	}
	active, err := findAbandonableTrainV2Attempt(current)
	if err != nil {
		return TrainV2AbandonResult{}, err
	}
	if live, err := s.trainV2HasLiveOperationWithContext(ctx, in.ProjectID, in.TrainID); err != nil {
		return TrainV2AbandonResult{}, err
	} else if live {
		return TrainV2AbandonResult{}, fmt.Errorf("Train cannot be abandoned: TRAIN_OPERATION_LIVE")
	}
	expected := in.ExpectedHubRevision
	if expected == "" {
		expected, err = s.hubRevision(ctx)
		if err != nil {
			return TrainV2AbandonResult{}, err
		}
	}
	now := s.durableNow()
	var abandoned model.TrainV2
	_, err = s.Hub.Transact(ctx, expected, "gateway: abandon Train "+in.TrainID, func(worktree string) ([]string, error) {
		var latest model.TrainV2
		if err := readWorktreeJSON(worktree, s.trainV2Path(in.ProjectID, in.TrainID), &latest); err != nil {
			return nil, err
		}
		if latest.Revision != current.Revision || latest.Status != current.Status {
			return nil, fmt.Errorf("Train changed before abandonment")
		}
		if err := rejectTrainV2AbandonStatus(latest); err != nil {
			return nil, err
		}
		latestActive, err := findAbandonableTrainV2Attempt(latest)
		if err != nil {
			return nil, err
		}
		if latestActive.Position != active.Position || latestActive.Attempt.Number != active.Attempt.Number || latestActive.Attempt.AgentID != active.Attempt.AgentID {
			return nil, fmt.Errorf("Train active Attempt changed before abandonment")
		}
		if live, err := s.trainV2HasLiveOperationInWorktree(in.ProjectID, in.TrainID, worktree); err != nil {
			return nil, err
		} else if live {
			return nil, fmt.Errorf("Train became active before abandonment: TRAIN_OPERATION_LIVE")
		}
		attempt := &latest.Items[latestActive.Position].Attempts[latestActive.Attempt.Number-1]
		attempt.Status = model.TrainV2AttemptAborted
		attempt.FinishedAt = &now
		latest.Items[latestActive.Position].ActiveAttemptNumber = 0
		latest.Items[latestActive.Position].Status = model.TrainV2ItemBlocked
		latest.Status = model.TrainV2Retired
		latest.Revision++
		latest.UpdatedAt = now
		latest.Retirement = &model.TrainV2Retirement{
			PreviousStatus: current.Status,
			Classification: "abandoned",
			Reason:         in.Reason,
			ActorSessionID: actor,
			RetiredAt:      now,
		}
		if err := model.ValidateTrainV2(latest); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, s.trainV2Path(in.ProjectID, in.TrainID), latest); err != nil {
			return nil, err
		}
		abandoned = latest
		return []string{s.trainV2Path(in.ProjectID, in.TrainID)}, nil
	})
	if err != nil {
		return TrainV2AbandonResult{}, err
	}
	return TrainV2AbandonResult{
		Train:                abandoned,
		PreviousStatus:       current.Status,
		AbortedItemPosition:  active.Position,
		AbortedAttemptNumber: active.Attempt.Number,
		AbortedAttemptStatus: model.TrainV2AttemptAborted,
		RetirementReason:     in.Reason,
		Status:               abandoned.Status,
	}, nil
}

type activeTrainV2Attempt struct {
	Position int
	Attempt  model.TrainV2Attempt
}

func rejectTrainV2AbandonStatus(train model.TrainV2) error {
	switch train.Status {
	case model.TrainV2Completed, model.TrainV2Retired:
		return fmt.Errorf("Train cannot be abandoned: TRAIN_TERMINAL")
	case model.TrainV2ReadyForIntegration:
		return fmt.Errorf("Train cannot be abandoned: TRAIN_INTEGRATION_PENDING")
	}
	return nil
}

func findAbandonableTrainV2Attempt(train model.TrainV2) (activeTrainV2Attempt, error) {
	var active activeTrainV2Attempt
	found := false
	for position, item := range train.Items {
		if item.ActiveAttemptNumber == 0 || item.ActiveAttemptNumber > uint64(len(item.Attempts)) {
			continue
		}
		if item.Status != model.TrainV2ItemRunning {
			return activeTrainV2Attempt{}, fmt.Errorf("Train active Attempt item is not running")
		}
		attempt := item.Attempts[item.ActiveAttemptNumber-1]
		if attempt.Status != model.TrainV2AttemptRunning {
			return activeTrainV2Attempt{}, fmt.Errorf("Train active Attempt pointer is not running")
		}
		if found {
			return activeTrainV2Attempt{}, fmt.Errorf("Train has multiple active Attempts")
		}
		active = activeTrainV2Attempt{Position: position, Attempt: attempt}
		found = true
	}
	if !found {
		return activeTrainV2Attempt{}, fmt.Errorf("Train cannot be abandoned: TRAIN_NO_ACTIVE_ATTEMPT")
	}
	return active, nil
}
