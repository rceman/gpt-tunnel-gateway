package service

import (
	"context"
	"fmt"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

func validateTrainV2AdvanceCurrentItem(item model.TrainV2Item, attemptNumber uint64) error {
	if attemptNumber == 0 || attemptNumber > uint64(len(item.Attempts)) || item.SuccessfulAttemptNumber != attemptNumber {
		return fmt.Errorf("current TrainItem Attempt is not successfully finalized")
	}
	attempt := item.Attempts[attemptNumber-1]
	if attempt.Status != model.TrainV2AttemptSucceeded {
		return fmt.Errorf("current TrainItem Attempt is not successfully finalized")
	}
	if item.Status == model.TrainV2ItemFinalized {
		return nil
	}
	if item.Status == model.TrainV2ItemReviewed && item.Proof != nil && item.Review != nil && item.Review.Outcome == model.ReviewOutcomeAccepted && item.Review.ReportID != "" && attempt.ReviewID == item.Review.ReportID {
		return nil
	}
	return fmt.Errorf("current TrainItem Attempt is not successfully finalized")
}
func (s *Service) dispatchNextTrainV2Attempt(ctx context.Context, train model.TrainV2, item model.TrainV2Item, attempt model.TrainV2Attempt, runtime trainv2.RuntimeBinding, expected string) (trainv2.StartResult, error) {
	if attempt.DispatchedAt != nil {
		return trainv2.StartResult{ItemPosition: item.Position, Attempt: attempt, Runtime: runtime}, nil
	}
	if err := trainv2.DispatchAttempt(ctx, trainv2.StartDependencies{Hub: s.Hub, Shared: s.Durability, OperationID: durableMutationOperationID(ctx), Airelay: s.Airelay, StateDir: s.Config.StateDir, SessionOrigin: AgentSessionID(ctx), MaterializePacket: s.materializeTrainV2Packet}, train, item, attempt, runtime, expected); err != nil {
		return trainv2.StartResult{}, err
	}
	var updated model.TrainV2
	var err error
	if s.Durability != nil {
		updated, err = s.trainV2ReadShared(ctx, train.ProjectID, train.ID)
	} else {
		updated, err = s.TrainV2Read(ctx, train.ProjectID, train.ID)
	}
	if err != nil {
		return trainv2.StartResult{}, err
	}
	item = updated.Items[item.Position]
	attempt = item.Attempts[attempt.Number-1]
	return trainv2.StartResult{ItemPosition: item.Position, Attempt: attempt, Runtime: runtime}, nil
}
func (s *Service) dispatchTrainV2Continuation(ctx context.Context, previous trainv2.RuntimeBinding, train model.TrainV2, item model.TrainV2Item, attempt model.TrainV2Attempt, expected string, now time.Time) (hub.TransactionResult, error) {
	runtime := previous
	runtime.ItemPosition, runtime.TaskID, runtime.AttemptNumber, runtime.StartedAt = item.Position, item.TaskID, attempt.Number, now
	if err := trainv2.ValidateRuntimeBinding(runtime, s.Config.StateDir); err != nil {
		return hub.TransactionResult{}, err
	}
	runtimePath := trainv2.RuntimePath(s.Config.StateDir, train.ProjectID, train.ID)
	if err := fsutil.WriteJSONAtomic(runtimePath, runtime, 0o600); err != nil {
		return hub.TransactionResult{}, err
	}
	keepRuntime := false
	defer func() {
		if !keepRuntime {
			_ = fsutil.WriteJSONAtomic(runtimePath, previous, 0o600)
		}
	}()
	packet, err := s.materializeTrainV2Packet(ctx, train, item, attempt, runtime)
	if err != nil {
		return hub.TransactionResult{}, fmt.Errorf("materialize Train continuation packet: %w", err)
	}
	dispatch, err := s.Airelay.PromptWithProvenance(ctx, attempt.AirelaySessionKey, AgentSessionID(ctx), trainv2.PacketDispatchMessage(packet))
	if err != nil {
		return hub.TransactionResult{}, fmt.Errorf("train continuation dispatch failed: %w", err)
	}
	dispatchedAt := dispatch.FinishedAt
	if dispatchedAt.IsZero() {
		dispatchedAt = now
	}
	tx, err := s.Hub.Transact(ctx, expected, "gateway: dispatch next Train v2 Attempt", func(worktree string) ([]string, error) {
		var current model.TrainV2
		if err := readWorktreeJSON(worktree, s.trainV2Path(train.ProjectID, train.ID), &current); err != nil {
			return nil, err
		}
		if item.Position < 0 || item.Position >= len(current.Items) {
			return nil, fmt.Errorf("next Train item disappeared")
		}
		currentItem := current.Items[item.Position]
		if currentItem.TaskID != item.TaskID || attempt.Number != 1 || len(currentItem.Attempts) != 1 {
			return nil, fmt.Errorf("next Train Attempt changed before dispatch")
		}
		currentItem.Attempts[0].DispatchedAt = &dispatchedAt
		current.Items[item.Position] = currentItem
		if err := model.ValidateTrainV2(current); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, s.trainV2Path(train.ProjectID, train.ID), current); err != nil {
			return nil, err
		}
		return []string{s.trainV2Path(train.ProjectID, train.ID)}, nil
	})
	if err != nil {
		return hub.TransactionResult{}, err
	}
	keepRuntime = true
	return tx, nil
}
