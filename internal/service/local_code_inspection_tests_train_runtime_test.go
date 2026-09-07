package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

func seedCodeTrainWithLegacyRuntime(t *testing.T, f localCodeFixture, status string) string {
	t.Helper()
	project := f.service.Config.Projects["example"]
	project.ProjectCode = "EXM"
	f.service.Config.Projects["example"] = project
	trainID := "EXM-TRN2"
	trainPath, err := trainv2.CompactWorktreePath(f.service.Config.StateDir, project.ProjectCode, trainID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(trainPath), 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, f.root, "branch", "train/"+trainID, f.current)
	testutil.Git(t, f.root, "worktree", "add", trainPath, "train/"+trainID)
	if err := os.WriteFile(filepath.Join(trainPath, "train-only.txt"), []byte("train lane\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, trainPath, "add", "train-only.txt")
	testutil.Git(t, trainPath, "commit", "-m", "train lane fixture")
	t.Cleanup(func() {
		testutil.Git(t, f.root, "worktree", "remove", "--force", trainPath)
		testutil.Git(t, f.root, "branch", "-D", "train/"+trainID)
	})
	now := time.Now().UTC()
	train := model.TrainV2{
		SchemaVersion: model.TrainV2SchemaVersion,
		ID:            trainID,
		ProjectID:     "example",
		Revision:      1,
		Status:        status,
		CreatedBy:     "runtime-regression",
		CreatedAt:     now,
		UpdatedAt:     now,
		Items: []model.TrainV2Item{{
			Position:           0,
			TaskID:             "EXM-TSK1",
			TaskRevision:       1,
			TaskRevisionSHA256: strings.Repeat("a", 64),
			Status:             model.TrainV2ItemQueued,
			AddedAt:            now,
		}},
	}
	payload, err := json.Marshal(train)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Durability.CommitSharedMutation(context.Background(), sqlitestore.SharedMutation{
		OperationID: "OPR-LEGACY-RUNTIME-" + strings.ToLower(status),
		EntityType:  "train",
		EntityID:    trainID,
		Revision:    1,
		Kind:        "runtime-regression",
		Payload:     payload,
		Create:      true,
	}); err != nil {
		t.Fatal(err)
	}
	runtimePath := trainv2.RuntimePath(f.service.Config.StateDir, "example", trainID)
	if err := os.MkdirAll(filepath.Dir(runtimePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(runtimePath, []byte(`{"schema_version":1,"project_id":"example","train_id":"EXM-TRN2","run_id":"legacy-run"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return trainID
}

func TestCodeWorktreeSkipsInactiveTrainBeforeRuntimeDecode(t *testing.T) {
	f := newLocalCodeFixture(t)
	trainID := seedCodeTrainWithLegacyRuntime(t, f, model.TrainV2Planned)
	result, err := f.service.CodeWorktree(context.Background(), CodeWorktreeInput{ProjectID: "example"})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range result.Items {
		if item.TrainID == trainID {
			t.Fatalf("inactive Train with obsolete runtime was exposed: %#v", result)
		}
	}
	if len(result.Items) != 1 || result.Items[0].Kind != "main" {
		t.Fatalf("inactive Train changed the code/worktree result: %#v", result)
	}
}

func TestCodeWorktreeFailsClosedForRunningTrainWithInvalidRuntime(t *testing.T) {
	f := newLocalCodeFixture(t)
	trainID := seedCodeTrainWithLegacyRuntime(t, f, model.TrainV2Running)
	if _, err := f.service.CodeWorktree(context.Background(), CodeWorktreeInput{ProjectID: "example"}); err == nil || !strings.Contains(err.Error(), trainID) {
		t.Fatalf("running Train with obsolete runtime was not rejected: %v", err)
	}
}
