package train

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

// startShared is the SQLite-first admission path. Git is used only for local
// worktree inspection/creation; no refresh, Hub read, or Hub transaction is
// part of admitting the first Attempt.
func startShared(ctx context.Context, in StartInput, deps StartDependencies) (StartResult, error) {
	now := time.Now().UTC()
	if deps.Now != nil {
		now = deps.Now().UTC()
	}
	binding, runtimeErr := ReadRuntime(deps.StateDir, in.ProjectID, in.TrainID)
	if runtimeErr == nil {
		if binding.AgentID != in.ResolvedAgentID || binding.SessionKey != in.SessionKey {
			return StartResult{}, fmt.Errorf("Train already has a different Agent/session binding")
		}
		if binding.ItemPosition < 0 || binding.ItemPosition >= len(deps.Train.Items) {
			return StartResult{}, fmt.Errorf("Train Attempt item binding is invalid")
		}
		item := deps.Train.Items[binding.ItemPosition]
		attempt, err := itemAttempt(item, binding.AttemptNumber)
		if err != nil {
			return StartResult{}, err
		}
		if item.TaskID != binding.TaskID || attempt.AirelaySessionKey != binding.SessionKey {
			return StartResult{}, fmt.Errorf("Train Attempt runtime ownership mismatch")
		}
		if (attempt.Status == model.TrainV2AttemptRunning || attempt.Status == model.TrainV2AttemptRecovered) && attempt.DispatchedAt == nil {
			if err := dispatchAttempt(ctx, deps, deps.Train, item, attempt, binding, ""); err != nil {
				return StartResult{}, err
			}
			deps.Train, err = readSharedTrain(ctx, deps.Shared, in.ProjectID, in.TrainID)
			if err != nil {
				return StartResult{}, err
			}
			item = deps.Train.Items[binding.ItemPosition]
			attempt, err = itemAttempt(item, binding.AttemptNumber)
			if err != nil {
				return StartResult{}, err
			}
		}
		return StartResult{Record: startRecordFromAttempt(deps.Train, item, attempt, deps.Policy, deps.Project, now), ItemPosition: binding.ItemPosition, Attempt: attempt, Runtime: binding}, nil
	}
	if !os.IsNotExist(runtimeErr) {
		return StartResult{}, runtimeErr
	}
	if deps.Train.Status != model.TrainV2Planned || in.StartedBy == "" || strings.ContainsAny(in.StartedBy, "\x00\r\n") || in.SessionKey == "" || in.ResolvedAgentID == "" {
		return StartResult{}, fmt.Errorf("Train has no startable local Attempt")
	}
	item := deps.Train.Items[0]
	if item.Status != model.TrainV2ItemQueued || len(item.Attempts) != 0 || item.ActiveAttemptNumber != 0 {
		return StartResult{}, fmt.Errorf("Train first item is not available for Attempt admission")
	}
	if deps.ReadTask == nil {
		return StartResult{}, fmt.Errorf("local Task authority is unavailable")
	}
	task, err := deps.ReadTask(ctx, in.ProjectID, item.TaskID)
	if err != nil {
		return StartResult{}, err
	}
	if err := ValidateExecutionTask(task); err != nil {
		return StartResult{}, err
	}
	if task.ID != item.TaskID || task.ProjectID != in.ProjectID {
		return StartResult{}, fmt.Errorf("Train item Task identity does not match the current Task")
	}
	item.TaskRevision = task.Revision
	item.TaskRevisionSHA256 = task.RevisionSHA256
	status, err := deps.Git.WorktreeStatus(ctx, deps.ProjectConfig)
	if err != nil {
		return StartResult{}, err
	}
	if !status.Clean {
		return StartResult{}, fmt.Errorf("project worktree is dirty")
	}
	integrationBranch := deps.Policy.IntegrationBranch
	if integrationBranch == "" {
		integrationBranch = deps.Project.DefaultBranch
	}
	base, exists, err := deps.Git.MirrorBranchHead(ctx, deps.ProjectConfig, integrationBranch)
	if err != nil || !exists || model.ValidateCommitSHA(base) != nil {
		return StartResult{}, fmt.Errorf("local integration branch %q does not resolve to an exact commit", integrationBranch)
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
	runtime := RuntimeBinding{SchemaVersion: runtimeSchemaVersion, ProjectID: in.ProjectID, ProjectCode: deps.ProjectCode, TrainID: in.TrainID, WorktreePath: worktreePath, AgentID: in.ResolvedAgentID, SessionKey: in.SessionKey, ItemPosition: item.Position, TaskID: item.TaskID, AttemptNumber: 1, StartedAt: now}
	if err := ValidateRuntimeBinding(runtime, deps.StateDir); err != nil {
		return StartResult{}, err
	}
	updated := deps.Train
	updated.Revision++
	updated.UpdatedAt = now
	updated.Status = model.TrainV2Running
	updated.Items[0] = item
	updated.Items[0].Status = model.TrainV2ItemRunning
	updated.Items[0].Attempts = []model.TrainV2Attempt{attempt}
	updated.Items[0].ActiveAttemptNumber = 1
	if err := model.ValidateTrainV2(updated); err != nil {
		return StartResult{}, err
	}
	payload, err := json.Marshal(updated)
	if err != nil {
		return StartResult{}, err
	}
	operationID := deps.OperationID
	if operationID == "" {
		operationID = "train-v2-start-" + in.TrainID
	}
	if err := fsutil.WriteJSONAtomic(runtimePath(deps.StateDir, in.ProjectID, in.TrainID), runtime, 0o600); err != nil {
		return StartResult{}, err
	}
	runtimeWritten := true
	defer func() {
		if runtimeWritten && createdWorktree {
			_ = os.Remove(runtimePath(deps.StateDir, in.ProjectID, in.TrainID))
		}
	}()
	if _, err := deps.Shared.CommitSharedMutation(ctx, sqlitestore.SharedMutation{OperationID: operationID, EntityType: "train", EntityID: updated.ID, ExpectedRevision: int64(deps.Train.Revision), Revision: int64(updated.Revision), Kind: "train-v2-start", Payload: payload, CreatedAt: now}); err != nil {
		return StartResult{}, err
	}
	createdWorktree = false
	runtimeWritten = false
	item = updated.Items[0]
	if err := dispatchAttempt(ctx, deps, updated, item, attempt, runtime, ""); err != nil {
		return StartResult{}, err
	}
	finalTrain, err := readSharedTrain(ctx, deps.Shared, in.ProjectID, in.TrainID)
	if err != nil {
		return StartResult{}, err
	}
	attempt, err = itemAttempt(finalTrain.Items[0], 1)
	if err != nil {
		return StartResult{}, err
	}
	return StartResult{Record: startRecordFromAttempt(finalTrain, finalTrain.Items[0], attempt, deps.Policy, deps.Project, now), ItemPosition: 0, Attempt: attempt, Runtime: runtime}, nil
}

func readSharedTrain(ctx context.Context, db *sqlitestore.Databases, projectID, trainID string) (model.TrainV2, error) {
	entity, err := db.ReadSharedEntity(ctx, "train", trainID)
	if err != nil {
		return model.TrainV2{}, err
	}
	var train model.TrainV2
	if err := json.Unmarshal(entity.Payload, &train); err != nil {
		return model.TrainV2{}, err
	}
	if train.ID != trainID || train.ProjectID != projectID {
		return model.TrainV2{}, fmt.Errorf("shared Train identity mismatch")
	}
	if err := model.ValidateTrainV2(train); err != nil {
		return model.TrainV2{}, err
	}
	return train, nil
}

func startRecordFromAttempt(train model.TrainV2, item model.TrainV2Item, attempt model.TrainV2Attempt, policy model.ProjectWorkflowPolicy, project model.Project, now time.Time) model.TrainV2StartRecord {
	branch := policy.IntegrationBranch
	if branch == "" {
		branch = project.DefaultBranch
	}
	return model.TrainV2StartRecord{SchemaVersion: model.TrainV2StartSchemaVersion, ProjectID: train.ProjectID, TrainID: train.ID, Status: model.TrainV2StartActive, IntegrationBranch: branch, BaseRevision: attempt.StartHead, LaneBranch: "train/" + train.ID, CurrentItemPosition: item.Position, CurrentAttemptNumber: attempt.Number, CurrentTaskID: item.TaskID, CurrentTaskRevision: item.TaskRevision, CurrentTaskRevisionSHA256: item.TaskRevisionSHA256, StartedAt: now}
}

func dispatchAttemptShared(ctx context.Context, deps StartDependencies, train model.TrainV2, item model.TrainV2Item, attempt model.TrainV2Attempt, receipt dispatchReceipt) error {
	if attempt.DispatchedAt != nil {
		return nil
	}
	if item.Position < 0 || item.Position >= len(train.Items) || item.TaskID != train.Items[item.Position].TaskID {
		return fmt.Errorf("Train Attempt item changed before local dispatch")
	}
	current := train.Items[item.Position]
	if attempt.Number == 0 || attempt.Number > uint64(len(current.Attempts)) {
		return fmt.Errorf("Train Attempt changed before local dispatch")
	}
	currentAttempt := current.Attempts[attempt.Number-1]
	if currentAttempt.AgentID != attempt.AgentID || currentAttempt.AirelaySessionKey != attempt.AirelaySessionKey || currentAttempt.StartHead != attempt.StartHead {
		return fmt.Errorf("Train Attempt execution snapshot changed before local dispatch")
	}
	dispatchedAt := receipt.FinishedAt
	if dispatchedAt.IsZero() {
		dispatchedAt = time.Now().UTC()
	}
	currentAttempt.Status = model.TrainV2AttemptRunning
	currentAttempt.DispatchedAt = &dispatchedAt
	current.Attempts[attempt.Number-1] = currentAttempt
	train.Items[item.Position] = current
	train.Revision++
	train.UpdatedAt = dispatchedAt
	if err := model.ValidateTrainV2(train); err != nil {
		return err
	}
	payload, err := json.Marshal(train)
	if err != nil {
		return err
	}
	operationID := deps.OperationID
	if operationID == "" {
		operationID = "train-v2-start-" + train.ID
	}
	operationID += "-dispatch"
	if _, err := deps.Shared.CommitSharedMutation(ctx, sqlitestore.SharedMutation{OperationID: operationID, EntityType: "train", EntityID: train.ID, ExpectedRevision: int64(train.Revision - 1), Revision: int64(train.Revision), Kind: "train-v2-dispatch", Payload: payload, CreatedAt: dispatchedAt}); err != nil {
		return err
	}
	if err := os.Remove(dispatchReceiptPath(deps.StateDir, train.ProjectID, train.ID)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
