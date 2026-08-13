package service

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

// TrainV2Advance appends Attempt 1 for the next queued TrainItem. It is
// idempotent after durable progression: a dispatched next Attempt is returned
// without sending another prompt or creating another Attempt.
func (s *Service) TrainV2Advance(ctx context.Context, in TrainV2AdvanceInput) (trainv2.StartResult, error) {
	if err := requireTrainV2Authoring(ctx, s, in.ProjectID); err != nil {
		return trainv2.StartResult{}, err
	}
	if err := model.ValidateProjectIdentifier(in.ProjectID); err != nil {
		return trainv2.StartResult{}, err
	}
	if _, _, err := model.ParseTrainV2ID(in.TrainID); err != nil {
		return trainv2.StartResult{}, err
	}
	lock, err := lockfile.Acquire(filepath.Join(s.Config.StateDir, "locks"), "train-"+in.TrainID)
	if err != nil {
		return trainv2.StartResult{}, err
	}
	defer lock.Release()

	train, err := s.TrainV2Read(ctx, in.ProjectID, in.TrainID)
	if err != nil {
		return trainv2.StartResult{}, err
	}
	if train.Status != model.TrainV2Running {
		return trainv2.StartResult{}, fmt.Errorf("Train is not running")
	}
	var start model.TrainV2StartRecord
	startPath := hub.ProtocolRoot + "/projects/" + in.ProjectID + "/train-v2-starts/" + in.TrainID + ".json"
	if err := s.Hub.ReadJSON(ctx, startPath, &start); err != nil {
		return trainv2.StartResult{}, fmt.Errorf("read Train start: %w", err)
	}
	if err := model.ValidateTrainV2StartRecord(start); err != nil {
		return trainv2.StartResult{}, err
	}
	if start.Status != model.TrainV2StartActive || start.CurrentItemPosition < 0 || start.CurrentItemPosition >= len(train.Items) {
		return trainv2.StartResult{}, fmt.Errorf("Train start has no valid current item")
	}
	runtime, err := trainv2.ReadRuntime(s.Config.StateDir, in.ProjectID, in.TrainID)
	if err != nil {
		return trainv2.StartResult{}, fmt.Errorf("read Train runtime: %w", err)
	}
	if runtime.ItemPosition != start.CurrentItemPosition || runtime.TaskID != start.CurrentTaskID || runtime.AttemptNumber != start.CurrentAttemptNumber {
		return trainv2.StartResult{}, fmt.Errorf("Train runtime does not match the current Attempt")
	}

	currentItem := train.Items[start.CurrentItemPosition]
	if start.CurrentAttemptNumber == 0 || start.CurrentAttemptNumber > uint64(len(currentItem.Attempts)) {
		return trainv2.StartResult{}, fmt.Errorf("Train start has no current Attempt")
	}
	currentAttempt := currentItem.Attempts[start.CurrentAttemptNumber-1]
	if currentItem.Status == model.TrainV2ItemRunning && currentAttempt.Status == model.TrainV2AttemptRunning {
		result, err := s.dispatchNextTrainV2Attempt(ctx, train, currentItem, currentAttempt, runtime, in.ExpectedHubRevision)
		if err != nil {
			return trainv2.StartResult{}, err
		}
		result.Record = start
		return result, nil
	}
	if currentAttempt.Status != model.TrainV2AttemptSucceeded || currentItem.Status != model.TrainV2ItemFinalized {
		return trainv2.StartResult{}, fmt.Errorf("current TrainItem Attempt is not successfully finalized")
	}
	nextPosition := start.CurrentItemPosition + 1
	if nextPosition >= len(train.Items) {
		return trainv2.StartResult{}, fmt.Errorf("Train has no next queued item")
	}
	nextItem := train.Items[nextPosition]
	if nextItem.Status != model.TrainV2ItemQueued || len(nextItem.Attempts) != 0 {
		return trainv2.StartResult{}, fmt.Errorf("next TrainItem is not queued and unstarted")
	}

	lane := s.Config.Projects[in.ProjectID]
	lane.Root = runtime.WorktreePath
	head, branch, clean, err := s.Git.CurrentHead(ctx, lane)
	if err != nil {
		return trainv2.StartResult{}, err
	}
	if !clean || branch != start.LaneBranch {
		return trainv2.StartResult{}, fmt.Errorf("Train lane is not clean and bound to its branch")
	}
	now := time.Now().UTC()
	attempt, err := trainv2.BuildNextAttempt(trainv2.NextAttemptInput{
		CurrentAttempt: currentAttempt,
		Next:           nextItem,
		AgentID:        currentAttempt.AgentID,
		GatewayID:      currentAttempt.GatewayID,
		SessionKey:     currentAttempt.AirelaySessionKey,
		StartHead:      head,
		CreatedAt:      now,
	})
	if err != nil {
		return trainv2.StartResult{}, err
	}
	updatedItem := nextItem
	updatedItem.Status = model.TrainV2ItemRunning
	updatedItem.ActiveAttemptNumber = 1
	updatedItem.Attempts = []model.TrainV2Attempt{attempt}
	updatedTrain := train
	updatedTrain.Items[nextPosition] = updatedItem
	updatedTrain.Revision++
	updatedTrain.UpdatedAt = now
	updatedStart := start
	updatedStart.CurrentItemPosition = nextPosition
	updatedStart.CurrentAttemptNumber = 1
	updatedStart.CurrentTaskID = nextItem.TaskID
	updatedStart.CurrentTaskRevision = nextItem.TaskRevision
	updatedStart.CurrentTaskRevisionSHA256 = nextItem.TaskRevisionSHA256
	if err := model.ValidateTrainV2(updatedTrain); err != nil {
		return trainv2.StartResult{}, err
	}
	if err := model.ValidateTrainV2StartRecord(updatedStart); err != nil {
		return trainv2.StartResult{}, err
	}
	newRuntime := runtime
	newRuntime.ItemPosition = nextPosition
	newRuntime.TaskID = nextItem.TaskID
	newRuntime.AttemptNumber = 1
	newRuntime.StartedAt = now
	if err := trainv2.ValidateRuntimeBinding(newRuntime, s.Config.StateDir); err != nil {
		return trainv2.StartResult{}, err
	}
	runtimePath := trainv2.RuntimePath(s.Config.StateDir, in.ProjectID, in.TrainID)
	if err := fsutil.WriteJSONAtomic(runtimePath, newRuntime, 0o600); err != nil {
		return trainv2.StartResult{}, err
	}
	keepRuntime := false
	defer func() {
		if !keepRuntime {
			_ = fsutil.WriteJSONAtomic(runtimePath, runtime, 0o600)
		}
	}()
	expected := in.ExpectedHubRevision
	if expected == "" {
		expected, err = s.hubRevision(ctx)
		if err != nil {
			return trainv2.StartResult{}, err
		}
	}
	tx, err := s.Hub.Transact(ctx, expected, "gateway: advance Train v2 Attempt", func(worktree string) ([]string, error) {
		var latest model.TrainV2
		if err := readWorktreeJSON(worktree, s.trainV2Path(in.ProjectID, in.TrainID), &latest); err != nil {
			return nil, err
		}
		if latest.Revision != train.Revision || start.CurrentItemPosition >= len(latest.Items) || latest.Items[start.CurrentItemPosition].Status != model.TrainV2ItemFinalized || latest.Items[start.CurrentItemPosition].Attempts[start.CurrentAttemptNumber-1].Status != model.TrainV2AttemptSucceeded || nextPosition >= len(latest.Items) || latest.Items[nextPosition].Status != model.TrainV2ItemQueued || len(latest.Items[nextPosition].Attempts) != 0 {
			return nil, fmt.Errorf("Train changed before next Attempt start")
		}
		if err := hub.WriteJSON(worktree, s.trainV2Path(in.ProjectID, in.TrainID), updatedTrain); err != nil {
			return nil, err
		}
		var latestStart model.TrainV2StartRecord
		if err := readWorktreeJSON(worktree, startPath, &latestStart); err != nil {
			return nil, err
		}
		if latestStart.CurrentItemPosition != start.CurrentItemPosition || latestStart.CurrentAttemptNumber != start.CurrentAttemptNumber || latestStart.CurrentTaskID != start.CurrentTaskID {
			return nil, fmt.Errorf("Train start changed before next Attempt start")
		}
		if err := hub.WriteJSON(worktree, startPath, updatedStart); err != nil {
			return nil, err
		}
		return []string{s.trainV2Path(in.ProjectID, in.TrainID), startPath}, nil
	})
	if err != nil {
		return trainv2.StartResult{}, err
	}
	keepRuntime = true
	result, err := s.dispatchNextTrainV2Attempt(ctx, updatedTrain, updatedItem, attempt, newRuntime, tx.After)
	if err != nil {
		return trainv2.StartResult{}, err
	}
	result.Record = updatedStart
	return result, nil
}

func (s *Service) dispatchNextTrainV2Attempt(ctx context.Context, train model.TrainV2, item model.TrainV2Item, attempt model.TrainV2Attempt, runtime trainv2.RuntimeBinding, expected string) (trainv2.StartResult, error) {
	if attempt.DispatchedAt != nil {
		return trainv2.StartResult{ItemPosition: item.Position, Attempt: attempt, Runtime: runtime}, nil
	}
	if err := trainv2.DispatchAttempt(ctx, trainv2.StartDependencies{Hub: s.Hub, Airelay: s.Airelay, StateDir: s.Config.StateDir, MaterializePacket: s.materializeTrainV2Packet}, train, item, attempt, runtime, expected); err != nil {
		return trainv2.StartResult{}, err
	}
	updated, err := s.TrainV2Read(ctx, train.ProjectID, train.ID)
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
