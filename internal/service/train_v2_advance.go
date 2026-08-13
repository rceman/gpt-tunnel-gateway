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
	dispatch, err := s.Airelay.Prompt(ctx, attempt.AirelaySessionKey, trainv2.PacketDispatchMessage(packet))
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
