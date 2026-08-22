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

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

func projectRoot(projectID string) string { return hub.ProtocolRoot + "/projects/" + projectID }

func trainPath(projectID, trainID string) string {
	return projectRoot(projectID) + "/trains-v2/" + trainID + ".json"
}

func readStartRecord(ctx context.Context, store hub.Store, projectID, trainID string) (model.TrainV2StartRecord, error) {
	var record model.TrainV2StartRecord
	if err := store.ReadJSON(ctx, projectRoot(projectID)+"/train-v2-starts/"+trainID+".json", &record); err != nil {
		return record, err
	}
	if err := model.ValidateTrainV2StartRecord(record); err != nil {
		return record, err
	}
	return record, nil
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

// DeriveStartRecord preserves the Train start authority from the canonical
// Attempt snapshot when the legacy Hub start file is not available locally.
func DeriveStartRecord(train model.TrainV2, item model.TrainV2Item, attempt model.TrainV2Attempt, policy model.ProjectWorkflowPolicy, project model.Project, now time.Time) model.TrainV2StartRecord {
	branch := policy.IntegrationBranch
	if branch == "" {
		branch = project.DefaultBranch
	}
	return model.TrainV2StartRecord{SchemaVersion: model.TrainV2StartSchemaVersion, ProjectID: train.ProjectID, TrainID: train.ID, Status: model.TrainV2StartActive, IntegrationBranch: branch, BaseRevision: attempt.StartHead, LaneBranch: "train/" + train.ID, CurrentItemPosition: item.Position, CurrentAttemptNumber: attempt.Number, CurrentTaskID: item.TaskID, CurrentTaskRevision: item.TaskRevision, CurrentTaskRevisionSHA256: item.TaskRevisionSHA256, StartedAt: now}
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
	_, err = deps.Shared.CommitSharedMutation(ctx, sqlitestore.SharedMutation{OperationID: operationID, EntityType: "train", EntityID: train.ID, ExpectedRevision: int64(train.Revision - 1), Revision: int64(train.Revision), Kind: kind, Payload: payload, CreatedAt: now})
	return err
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
