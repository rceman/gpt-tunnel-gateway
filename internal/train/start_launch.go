package train

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

// Start creates Attempt 1 for the first TrainItem. Attempts are children of
// the Train item; this path intentionally never allocates or writes a Run.
func Start(ctx context.Context, in StartInput, deps StartDependencies) (StartResult, error) {
	if err := model.ValidateProjectIdentifier(in.ProjectID); err != nil {
		return StartResult{}, err
	}
	if deps.Shared == nil {
		return StartResult{}, fmt.Errorf("Shared Train authority is unavailable")
	}
	if _, _, err := model.ParseTrainV2ID(in.TrainID); err != nil {
		return StartResult{}, err
	}
	if err := model.ValidateTrainV2(deps.Train); err != nil || deps.Train.ProjectID != in.ProjectID || deps.Train.ID != in.TrainID || len(deps.Train.Items) == 0 {
		return StartResult{}, fmt.Errorf("train v2 is not startable")
	}
	now := time.Now().UTC()
	if deps.Now != nil {
		now = deps.Now().UTC()
	}
	if binding, err := ReadRuntime(deps.StateDir, in.ProjectID, in.TrainID); err == nil {
		train := deps.Train
		var record model.TrainV2StartRecord
		if binding.ItemPosition < 0 || binding.ItemPosition >= len(train.Items) {
			return StartResult{}, fmt.Errorf("Train attempt item binding is invalid")
		}
		item := train.Items[binding.ItemPosition]
		attempt, attemptErr := itemAttempt(item, binding.AttemptNumber)
		if attemptErr != nil {
			return StartResult{}, attemptErr
		}
		if attempt.AgentID != in.ResolvedAgentID || attempt.AirelaySessionKey != in.SessionKey {
			return StartResult{}, fmt.Errorf("Train already has a different Agent/session binding")
		}
		if binding.AgentID != attempt.AgentID || binding.SessionKey != attempt.AirelaySessionKey || binding.TaskID != item.TaskID {
			return StartResult{}, fmt.Errorf("Train runtime does not match Attempt authority")
		}
		record = DeriveStartRecord(train, item, attempt, deps.Policy, deps.Project, attempt.StartedAt)
		if record.CurrentItemPosition < 0 || record.CurrentItemPosition >= len(train.Items) {
			return StartResult{}, fmt.Errorf("Train attempt item binding is invalid")
		}
		item = train.Items[record.CurrentItemPosition]
		attempt, err = itemAttempt(item, record.CurrentAttemptNumber)
		if err != nil {
			return StartResult{}, err
		}
		if (attempt.Status == model.TrainV2AttemptRunning || attempt.Status == model.TrainV2AttemptRecovered) && attempt.DispatchedAt == nil {
			if err := dispatchAttempt(ctx, deps, train, item, attempt, binding, ""); err != nil {
				return StartResult{}, err
			}
			train, err = readSharedTrain(ctx, deps.Shared, in.ProjectID, in.TrainID)
			if err != nil {
				return StartResult{}, err
			}
			item = train.Items[record.CurrentItemPosition]
			attempt, err = itemAttempt(item, record.CurrentAttemptNumber)
			if err != nil {
				return StartResult{}, err
			}
		}
		return StartResult{
			Record:       record,
			ItemPosition: record.CurrentItemPosition,
			Attempt:      attempt,
			Runtime:      binding,
		}, nil
	} else if !os.IsNotExist(err) {
		return StartResult{}, err
	}
	if deps.Train.Status != model.TrainV2Planned {
		return StartResult{}, fmt.Errorf("Train has no durable Attempt runtime")
	}
	if in.StartedBy == "" || strings.ContainsAny(in.StartedBy, "\x00\r\n") || in.SessionKey == "" || in.ResolvedAgentID == "" {
		return StartResult{}, fmt.Errorf("train start identity is incomplete")
	}
	item := deps.Train.Items[0]
	if item.Status != model.TrainV2ItemQueued || len(item.Attempts) != 0 || item.ActiveAttemptNumber != 0 {
		return StartResult{}, fmt.Errorf("Train first item is not available for Attempt admission")
	}
	var currentTask model.TaskAuthoring
	if deps.ReadTask != nil {
		var err error
		currentTask, err = deps.ReadTask(ctx, in.ProjectID, item.TaskID)
		if err != nil {
			return StartResult{}, err
		}
		if err := ValidateExecutionTask(currentTask); err != nil {
			return StartResult{}, err
		}
		if currentTask.ID != item.TaskID || currentTask.ProjectID != in.ProjectID {
			return StartResult{}, fmt.Errorf("Train item Task identity does not match the current Task")
		}
		item.TaskRevision = currentTask.Revision
		item.TaskRevisionSHA256 = currentTask.RevisionSHA256
	}
	status, err := deps.Git.WorktreeStatus(ctx, deps.ProjectConfig)
	if err != nil {
		return StartResult{}, err
	}
	if !status.Clean {
		return StartResult{}, fmt.Errorf("project worktree is dirty")
	}
	if _, err := os.Stat(filepath.Join(deps.ProjectConfig.Mirror, "HEAD")); err != nil {
		return StartResult{}, fmt.Errorf("local project mirror is unavailable: %w", err)
	}
	integrationBranch := deps.Policy.IntegrationBranch
	if integrationBranch == "" {
		integrationBranch = deps.Project.DefaultBranch
	}
	base, exists, err := deps.Git.MirrorBranchHead(ctx, deps.ProjectConfig, integrationBranch)
	if err != nil || !exists || model.ValidateCommitSHA(base) != nil {
		return StartResult{}, fmt.Errorf("integration branch %q does not resolve to an exact commit", integrationBranch)
	}
	laneBranch := "train/" + deps.Train.ID
	worktreePath, err := CompactWorktreePath(deps.StateDir, deps.ProjectCode, in.TrainID)
	if err != nil {
		return StartResult{}, err
	}
	if err := deps.Git.CreateTrainWorktreeCompact(ctx, deps.ProjectConfig, deps.StateDir, deps.ProjectCode, in.TrainID, laneBranch, base); err != nil {
		return StartResult{}, err
	}
	createdWorktree := true
	defer func() {
		if createdWorktree {
			_ = deps.Git.RemoveTrainWorktreeCompact(context.Background(), deps.ProjectConfig, deps.StateDir, deps.ProjectCode, in.TrainID)
			_ = deps.Git.DeleteTrainBranch(context.Background(), deps.ProjectConfig, laneBranch, base)
		}
	}()
	attempt := model.TrainV2Attempt{Number: 1, Status: model.TrainV2AttemptRunning, AgentID: in.ResolvedAgentID, AirelaySessionKey: in.SessionKey, GatewayID: deps.GatewayID, StartHead: base, StartedAt: now}
	runtime := RuntimeBinding{
		SchemaVersion: runtimeSchemaVersion,
		ProjectID:     in.ProjectID,
		ProjectCode:   deps.ProjectCode,
		TrainID:       in.TrainID,
		WorktreePath:  worktreePath,
		AgentID:       in.ResolvedAgentID,
		SessionKey:    in.SessionKey,
		ItemPosition:  item.Position,
		TaskID:        item.TaskID,
		AttemptNumber: attempt.Number,
		StartedAt:     now,
	}
	if err := ValidateRuntimeBinding(runtime, deps.StateDir); err != nil {
		return StartResult{}, err
	}
	record := model.TrainV2StartRecord{SchemaVersion: model.TrainV2StartSchemaVersion, ProjectID: in.ProjectID, TrainID: in.TrainID, Status: model.TrainV2StartActive, IntegrationBranch: integrationBranch, BaseRevision: base, LaneBranch: laneBranch, CurrentItemPosition: item.Position, CurrentAttemptNumber: 1, CurrentTaskID: item.TaskID, CurrentTaskRevision: item.TaskRevision, CurrentTaskRevisionSHA256: item.TaskRevisionSHA256, StartedAt: now}
	if err := model.ValidateTrainV2StartRecord(record); err != nil {
		return StartResult{}, err
	}
	updatedTrain := deps.Train
	updatedTrain.Status = model.TrainV2Running
	updatedTrain.Revision++
	updatedTrain.UpdatedAt = now
	updatedTrain.Items[0] = item
	updatedTrain.Items[0].Status = model.TrainV2ItemRunning
	updatedTrain.Items[0].Attempts = []model.TrainV2Attempt{attempt}
	updatedTrain.Items[0].ActiveAttemptNumber = 1
	if err := model.ValidateTrainV2(updatedTrain); err != nil {
		return StartResult{}, err
	}
	if err := CommitSharedTrain(ctx, deps, updatedTrain, "train-v2-start", now); err != nil {
		return StartResult{}, err
	}
	createdWorktree = false
	if err := fsutil.WriteJSONAtomic(runtimePath(deps.StateDir, in.ProjectID, in.TrainID), runtime, 0o600); err != nil {
		return StartResult{}, err
	}
	item = updatedTrain.Items[0]
	if err := dispatchAttempt(ctx, deps, updatedTrain, item, attempt, runtime, ""); err != nil {
		return StartResult{}, err
	}
	var updated model.TrainV2
	updated, err = readSharedTrain(ctx, deps.Shared, in.ProjectID, in.TrainID)
	if err != nil {
		return StartResult{}, err
	}
	item = updated.Items[0]
	attempt, err = itemAttempt(item, 1)
	if err != nil {
		return StartResult{}, err
	}
	return StartResult{
		Record:       record,
		ItemPosition: 0,
		Attempt:      attempt,
		Runtime:      runtime,
	}, nil
}

func itemAttempt(item model.TrainV2Item, number uint64) (model.TrainV2Attempt, error) {
	if number == 0 || number > uint64(len(item.Attempts)) {
		return model.TrainV2Attempt{}, fmt.Errorf("Train item has no exact Attempt %d", number)
	}
	attempt := item.Attempts[number-1]
	if attempt.Number != number {
		return model.TrainV2Attempt{}, fmt.Errorf("Train item Attempt number mismatch")
	}
	return attempt, nil
}
