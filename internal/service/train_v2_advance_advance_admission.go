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
	return s.advanceTrainV2Locked(ctx, in, false)
}

func (s *Service) deriveSharedTrainStartRecord(ctx context.Context, train model.TrainV2) (model.TrainV2StartRecord, error) {
	projectConfig, ok := s.Config.Projects[train.ProjectID]
	if !ok {
		return model.TrainV2StartRecord{}, fmt.Errorf("project %q has no local runtime configuration", train.ProjectID)
	}
	policy, err := s.ProjectWorkflowPolicyRead(ctx, train.ProjectID)
	if err != nil {
		return model.TrainV2StartRecord{}, err
	}
	project := model.Project{SchemaVersion: model.SchemaVersion, ID: train.ProjectID, DefaultBranch: projectConfig.DefaultBranch, Status: "active"}
	var record *model.TrainV2StartRecord
	for _, item := range train.Items {
		if item.ActiveAttemptNumber == 0 || item.ActiveAttemptNumber > uint64(len(item.Attempts)) {
			continue
		}
		attempt := item.Attempts[item.ActiveAttemptNumber-1]
		candidate := trainv2.DeriveStartRecord(train, item, attempt, policy, project, attempt.StartedAt)
		if record != nil {
			return model.TrainV2StartRecord{}, fmt.Errorf("Train has ambiguous active Attempt authority")
		}
		record = &candidate
	}
	if record == nil {
		return model.TrainV2StartRecord{}, fmt.Errorf("Train has no local Attempt start authority")
	}
	return *record, nil
}

func (s *Service) validateSharedTrainAdvance(before, updated model.TrainV2, start, updatedStart model.TrainV2StartRecord, nextPosition int, updatedItem model.TrainV2Item) error {
	if updated.Revision != before.Revision+1 || updated.Status != model.TrainV2Running || nextPosition < 0 || nextPosition >= len(updated.Items) {
		return fmt.Errorf("Train changed before next Attempt start")
	}
	if err := validateTrainV2AdvanceCurrentItem(updated.Items[start.CurrentItemPosition], start.CurrentAttemptNumber); err != nil {
		return fmt.Errorf("Train changed before next Attempt start: %w", err)
	}
	if updated.Items[nextPosition].TaskID != updatedItem.TaskID || updated.Items[nextPosition].Status != model.TrainV2ItemRunning || len(updated.Items[nextPosition].Attempts) != 1 || updatedStart.CurrentItemPosition != nextPosition || updatedStart.CurrentAttemptNumber != 1 {
		return fmt.Errorf("Train changed before next Attempt start")
	}
	return model.ValidateTrainV2(updated)
}

func (s *Service) advanceTrainV2Locked(ctx context.Context, in TrainV2AdvanceInput, lockHeld bool) (trainv2.StartResult, error) {
	var lock *lockfile.Lock
	var err error
	if !lockHeld {
		lock, err = lockfile.Acquire(filepath.Join(s.Config.StateDir, "locks"), "train-"+in.TrainID)
		if err != nil {
			return trainv2.StartResult{}, err
		}
		defer lock.Release()
	}

	var train model.TrainV2
	if s.Durability != nil {
		train, err = s.trainV2ReadShared(ctx, in.ProjectID, in.TrainID)
	} else {
		train, err = s.TrainV2Read(ctx, in.ProjectID, in.TrainID)
	}
	if err != nil {
		return trainv2.StartResult{}, err
	}
	if train.Status != model.TrainV2Running {
		return trainv2.StartResult{}, fmt.Errorf("Train is not running")
	}
	var start model.TrainV2StartRecord
	startPath := hub.ProtocolRoot + "/projects/" + in.ProjectID + "/train-v2-starts/" + in.TrainID + ".json"
	if s.Durability != nil {
		start, err = s.deriveSharedTrainStartRecord(ctx, train)
	} else if err := s.Hub.ReadJSON(ctx, startPath, &start); err != nil {
		return trainv2.StartResult{}, fmt.Errorf("read Train start: %w", err)
	}
	if err != nil {
		return trainv2.StartResult{}, err
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
	if err := validateTrainV2AdvanceCurrentItem(currentItem, start.CurrentAttemptNumber); err != nil {
		return trainv2.StartResult{}, err
	}
	nextPosition := start.CurrentItemPosition + 1
	if nextPosition >= len(train.Items) {
		return trainv2.StartResult{}, fmt.Errorf("Train has no next queued item")
	}
	nextItem := train.Items[nextPosition]
	if nextItem.Status != model.TrainV2ItemQueued || len(nextItem.Attempts) != 0 {
		return trainv2.StartResult{}, fmt.Errorf("next TrainItem is not queued and unstarted")
	}
	currentTask, err := s.TaskAuthoringRead(ctx, in.ProjectID, nextItem.TaskID)
	if err != nil {
		return trainv2.StartResult{}, err
	}
	if err := trainv2.ValidateExecutionTask(currentTask); err != nil {
		return trainv2.StartResult{}, err
	}
	if currentTask.ID != nextItem.TaskID || currentTask.ProjectID != in.ProjectID {
		return trainv2.StartResult{}, fmt.Errorf("Train item Task identity does not match the current Task")
	}
	if s.Durability != nil {
		if err := s.validateTaskDependenciesShared(ctx, in.ProjectID, currentTask); err != nil {
			return trainv2.StartResult{}, err
		}
	} else if err := s.validateTaskDependencies(ctx, in.ProjectID, currentTask); err != nil {
		return trainv2.StartResult{}, err
	}
	nextItem.TaskRevision = currentTask.Revision
	nextItem.TaskRevisionSHA256 = currentTask.RevisionSHA256

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
	var tx hub.TransactionResult
	if s.Durability != nil {
		if err := s.validateSharedTrainAdvance(train, updatedTrain, start, updatedStart, nextPosition, updatedItem); err != nil {
			return trainv2.StartResult{}, err
		}
		if err := trainv2.CommitSharedTrain(ctx, trainv2.StartDependencies{Shared: s.Durability, OperationID: durableMutationOperationID(ctx)}, updatedTrain, "train-v2-advance", now); err != nil {
			return trainv2.StartResult{}, err
		}
	} else {
		if expected == "" {
			expected, err = s.hubRevision(ctx)
			if err != nil {
				return trainv2.StartResult{}, err
			}
		}
		tx, err = s.Hub.Transact(ctx, expected, "gateway: advance Train v2 Attempt", func(worktree string) ([]string, error) {
			var latest model.TrainV2
			if err := readWorktreeJSON(worktree, s.trainV2Path(in.ProjectID, in.TrainID), &latest); err != nil {
				return nil, err
			}
			if latest.Revision != train.Revision || start.CurrentItemPosition < 0 || start.CurrentItemPosition >= len(latest.Items) || nextPosition >= len(latest.Items) {
				return nil, fmt.Errorf("Train changed before next Attempt start")
			}
			if err := validateTrainV2AdvanceCurrentItem(latest.Items[start.CurrentItemPosition], start.CurrentAttemptNumber); err != nil {
				return nil, fmt.Errorf("Train changed before next Attempt start: %w", err)
			}
			if latest.Items[nextPosition].Status != model.TrainV2ItemQueued || len(latest.Items[nextPosition].Attempts) != 0 {
				return nil, fmt.Errorf("Train changed before next Attempt start")
			}
			var latestTask model.TaskAuthoring
			if err := readWorktreeJSON(worktree, s.taskAuthoringPath(in.ProjectID, nextItem.TaskID), &latestTask); err != nil {
				return nil, err
			}
			if err := trainv2.ValidateExecutionTask(latestTask); err != nil {
				return nil, err
			}
			if latestTask.ID != nextItem.TaskID || latestTask.ProjectID != in.ProjectID {
				return nil, fmt.Errorf("Train item Task identity does not match the current Task")
			}
			if err := s.validateTaskDependenciesInWorktree(worktree, in.ProjectID, []model.TaskAuthoring{latestTask}); err != nil {
				return nil, err
			}
			updatedItem.TaskRevision = latestTask.Revision
			updatedItem.TaskRevisionSHA256 = latestTask.RevisionSHA256
			updatedTrain.Items[nextPosition] = updatedItem
			updatedStart.CurrentTaskRevision = latestTask.Revision
			updatedStart.CurrentTaskRevisionSHA256 = latestTask.RevisionSHA256
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
	}
	keepRuntime = true
	result, err := s.dispatchNextTrainV2Attempt(ctx, updatedTrain, updatedItem, attempt, newRuntime, tx.After)
	if err != nil {
		return trainv2.StartResult{}, err
	}
	result.Record = updatedStart
	return result, nil
}
