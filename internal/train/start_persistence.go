package train

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

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

// DeriveStartRecord preserves the Train start authority from the canonical
// Attempt snapshot when the legacy Hub start file is not available locally.
func DeriveStartRecord(train model.TrainV2, item model.TrainV2Item, attempt model.TrainV2Attempt, policy model.ProjectWorkflowPolicy, project model.Project, now time.Time) model.TrainV2StartRecord {
	branch := policy.IntegrationBranch
	if branch == "" {
		branch = project.DefaultBranch
	}
	return model.TrainV2StartRecord{SchemaVersion: model.TrainV2StartSchemaVersion, ProjectID: train.ProjectID, TrainID: train.ID, Status: model.TrainV2StartActive, IntegrationBranch: branch, BaseRevision: attempt.StartHead, LaneBranch: "train/" + train.ID, CurrentItemPosition: item.Position, CurrentAttemptNumber: attempt.Number, CurrentTaskID: item.TaskID, CurrentTaskRevision: item.TaskRevision, CurrentTaskRevisionSHA256: item.TaskRevisionSHA256, StartedAt: now}
}

// ActiveAttemptIdentity derives watcher/runtime binding from the canonical
// Train item snapshot; it never consults a legacy start record.
func ActiveAttemptIdentity(train model.TrainV2) (int, uint64, string, bool) {
	for i, item := range train.Items {
		if item.ActiveAttemptNumber == 0 || item.ActiveAttemptNumber > uint64(len(item.Attempts)) {
			continue
		}
		attempt := item.Attempts[item.ActiveAttemptNumber-1]
		if attempt.Number == item.ActiveAttemptNumber && attempt.Status == model.TrainV2AttemptRunning {
			return i, attempt.Number, item.TaskID, true
		}
	}
	return 0, 0, "", false
}

// CommitSharedTrain persists a Train transition through the existing Shared
// CAS/outbox path. It is shared by Start and Advance; neither path owns a
// second Train state machine.
func CommitSharedTrain(ctx context.Context, deps StartDependencies, train model.TrainV2, kind string, now time.Time) error {
	payload, err := json.Marshal(train)
	if err != nil {
		return err
	}
	operationID := deps.OperationID
	if operationID == "" {
		operationID = "train-v2-" + kind + "-" + train.ID + fmt.Sprintf("-%d", train.Revision-1)
	}
	_, err = deps.Shared.CommitSharedMutation(ctx, sqlitestore.SharedMutation{OperationID: operationID, EntityType: "train", EntityID: train.ID, ExpectedRevision: int64(train.Revision - 1), Revision: int64(train.Revision), Kind: kind, Payload: payload, CreatedAt: now, Create: train.Revision == 1})
	return err
}

// CommitCorrectionStart performs the correction transition's local runtime
// projection and Shared CAS as one typed train operation. Service adapters
// supply validated domain values; path, lock, and atomic-file mechanics stay
// inside the train execution boundary.
func CommitCorrectionStart(ctx context.Context, deps StartDependencies, current, updated model.TrainV2, previousRuntime, nextRuntime RuntimeBinding, now time.Time) error {
	if deps.Shared == nil {
		return fmt.Errorf("Shared Train authority is unavailable")
	}
	if current.ID == "" || current.ID != updated.ID || current.ProjectID != updated.ProjectID || updated.Revision != current.Revision+1 {
		return fmt.Errorf("invalid correction Train revision transition")
	}
	if err := model.ValidateTrainV2(updated); err != nil {
		return err
	}
	if err := ValidateRuntimeBinding(nextRuntime, deps.StateDir); err != nil {
		return err
	}
	lock, err := lockfile.Acquire(filepath.Join(deps.StateDir, "locks"), "train-"+updated.ID)
	if err != nil {
		return err
	}
	defer lock.Release()

	path := runtimePath(deps.StateDir, updated.ProjectID, updated.ID)
	if err := fsutil.WriteJSONAtomic(path, nextRuntime, 0o600); err != nil {
		return err
	}
	keepRuntime := false
	defer func() {
		if !keepRuntime {
			_ = fsutil.WriteJSONAtomic(path, previousRuntime, 0o600)
		}
	}()
	if err := CommitSharedTrain(ctx, deps, updated, "train-v2-correction-start", now); err != nil {
		return err
	}
	keepRuntime = true
	return nil
}

func readWorktreeJSON(worktree, path string, out any) error {
	data, err := os.ReadFile(filepath.Join(worktree, filepath.FromSlash(path)))
	if err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return fmt.Errorf("worktree JSON has trailing content")
	}
	return nil
}
