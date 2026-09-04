package train

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/airelay"
	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

// Start creates Attempt 1 for the first TrainItem. Attempts are children of
// the Train item; this path intentionally never allocates or writes a Run.
func Start(ctx context.Context, in StartInput, deps StartDependencies) (StartResult, error) {
	if err := model.ValidateProjectIdentifier(in.ProjectID); err != nil {
		return StartResult{}, err
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
		if binding.AgentID != in.ResolvedAgentID || binding.SessionKey != in.SessionKey {
			return StartResult{}, fmt.Errorf("Train already has a different Agent/session binding")
		}
		record, err := readStartRecord(ctx, deps.Hub, in.ProjectID, in.TrainID)
		if err != nil {
			return StartResult{}, fmt.Errorf("durable Train attempt record is unavailable: %w", err)
		}
		train := deps.Train
		if record.CurrentItemPosition < 0 || record.CurrentItemPosition >= len(train.Items) {
			return StartResult{}, fmt.Errorf("Train attempt item binding is invalid")
		}
		item := train.Items[record.CurrentItemPosition]
		attempt, err := itemAttempt(item, record.CurrentAttemptNumber)
		if err != nil {
			return StartResult{}, err
		}
		if attempt.Status == model.TrainV2AttemptRunning || attempt.Status == model.TrainV2AttemptRecovered {
			if err := dispatchAttempt(ctx, deps, train, item, attempt, binding, ""); err != nil {
				return StartResult{}, err
			}
			train, err = readTrain(ctx, deps.Hub, in.ProjectID, in.TrainID)
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
	if err := deps.Git.Refresh(ctx, deps.ProjectConfig); err != nil {
		return StartResult{}, fmt.Errorf("refresh integration branch: %w", err)
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
	executionSession, err := deps.Airelay.EnsureExecutionSession(ctx, airelay.ExecutionSessionRequest{
		BaseSessionKey: in.SessionKey,
		Profile:        in.Profile,
		WorktreePath:   worktreePath,
		Identity:       "train:" + in.ProjectID + ":" + in.TrainID,
		LockDir:        filepath.Join(deps.StateDir, "locks"),
	})
	if err != nil {
		return StartResult{}, err
	}
	attempt := model.TrainV2Attempt{Number: 1, Status: model.TrainV2AttemptRunning, AgentID: in.ResolvedAgentID, AirelaySessionKey: executionSession, GatewayID: deps.GatewayID, StartHead: base, StartedAt: now}
	runtime := RuntimeBinding{
		SchemaVersion: runtimeSchemaVersion,
		ProjectID:     in.ProjectID,
		ProjectCode:   deps.ProjectCode,
		TrainID:       in.TrainID,
		WorktreePath:  worktreePath,
		AgentID:       in.ResolvedAgentID,
		SessionKey:    executionSession,
		ItemPosition:  item.Position,
		TaskID:        item.TaskID,
		AttemptNumber: attempt.Number,
		StartedAt:     now,
	}
	if err := ValidateRuntimeBinding(runtime, deps.StateDir); err != nil {
		return StartResult{}, err
	}
	expected := in.ExpectedHubRevision
	if expected == "" {
		expected, err = deps.Hub.RemoteRevision(ctx)
		if err != nil {
			return StartResult{}, err
		}
	}
	record := model.TrainV2StartRecord{SchemaVersion: model.TrainV2StartSchemaVersion, ProjectID: in.ProjectID, TrainID: in.TrainID, Status: model.TrainV2StartActive, IntegrationBranch: integrationBranch, BaseRevision: base, LaneBranch: laneBranch, CurrentItemPosition: item.Position, CurrentAttemptNumber: 1, CurrentTaskID: item.TaskID, CurrentTaskRevision: item.TaskRevision, CurrentTaskRevisionSHA256: item.TaskRevisionSHA256, StartedAt: now}
	if err := model.ValidateTrainV2StartRecord(record); err != nil {
		return StartResult{}, err
	}
	updatedTrain := deps.Train
	tx, err := deps.Hub.Transact(ctx, expected, "gateway: start Train v2 Attempt "+in.TrainID, func(worktree string) ([]string, error) {
		var latest model.TrainV2
		if err := readWorktreeJSON(worktree, trainPath(in.ProjectID, in.TrainID), &latest); err != nil {
			return nil, err
		}
		if deps.ValidateTaskMembershipInWorktree != nil {
			if err := deps.ValidateTaskMembershipInWorktree(worktree, in.ProjectID, in.TrainID); err != nil {
				return nil, err
			}
		}
		if latest.Revision != deps.Train.Revision || latest.Status != model.TrainV2Planned || len(latest.Items) == 0 || latest.Items[0].TaskID != item.TaskID || len(latest.Items[0].Attempts) != 0 {
			return nil, fmt.Errorf("Train v2 changed before Attempt start")
		}
		if deps.ReadTaskInWorktree != nil {
			latestTask, err := deps.ReadTaskInWorktree(worktree, in.ProjectID, item.TaskID)
			if err != nil {
				return nil, err
			}
			if err := ValidateExecutionTask(latestTask); err != nil {
				return nil, err
			}
			if latestTask.ID != item.TaskID || latestTask.ProjectID != in.ProjectID {
				return nil, fmt.Errorf("Train item Task identity does not match the current Task")
			}
			latest.Items[0].TaskRevision = latestTask.Revision
			latest.Items[0].TaskRevisionSHA256 = latestTask.RevisionSHA256
			record.CurrentTaskRevision = latestTask.Revision
			record.CurrentTaskRevisionSHA256 = latestTask.RevisionSHA256
			item.TaskRevision = latestTask.Revision
			item.TaskRevisionSHA256 = latestTask.RevisionSHA256
		}
		latest.Status = model.TrainV2Running
		latest.Items[0].Status = model.TrainV2ItemRunning
		latest.Items[0].Attempts = []model.TrainV2Attempt{attempt}
		latest.Items[0].ActiveAttemptNumber = 1
		if err := model.ValidateTrainV2(latest); err != nil {
			return nil, err
		}
		updatedTrain = latest
		startPath := projectRoot(in.ProjectID) + "/train-v2-starts/" + in.TrainID + ".json"
		if err := hub.WriteJSON(worktree, startPath, record); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, trainPath(in.ProjectID, in.TrainID), latest); err != nil {
			return nil, err
		}
		return []string{startPath, trainPath(in.ProjectID, in.TrainID)}, nil
	})
	if err != nil {
		return StartResult{}, err
	}
	createdWorktree = false
	if err := fsutil.WriteJSONAtomic(runtimePath(deps.StateDir, in.ProjectID, in.TrainID), runtime, 0o600); err != nil {
		return StartResult{}, err
	}
	item = updatedTrain.Items[0]
	if err := dispatchAttempt(ctx, deps, updatedTrain, item, attempt, runtime, tx.After); err != nil {
		return StartResult{}, err
	}
	updated, err := readTrain(ctx, deps.Hub, in.ProjectID, in.TrainID)
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

func readTrain(ctx context.Context, store hub.Store, projectID, trainID string) (model.TrainV2, error) {
	var train model.TrainV2
	if err := store.ReadJSON(ctx, trainPath(projectID, trainID), &train); err != nil {
		return train, err
	}
	if err := model.ValidateTrainV2(train); err != nil {
		return train, err
	}
	return train, nil
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
