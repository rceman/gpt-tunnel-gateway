package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

func TestStateCheckWithoutDurabilityUsesLocalConfigurationWithoutHub(t *testing.T) {
	s, revision, _ := testService(t)
	train, _ := reviewBackfillFixture(t)
	operation := trainv2.IntegrationOperation{
		SchemaVersion: 1,
		OperationID:   "GTW-INTEGRATE-000000000000000000000000",
		ProjectID:     train.ProjectID,
		TrainID:       train.ID,
		RequestSHA256: strings.Repeat("c", 64),
		SourceHead:    strings.Repeat("a", 40),
		TargetBranch:  "main",
		TargetBefore:  strings.Repeat("b", 40),
		Phase:         trainv2.IntegrationPhasePrePending,
		UpdatedAt:     time.Now().UTC(),
	}
	if _, err := s.Hub.Transact(context.Background(), revision, "test: seed Train integration graph", func(worktree string) ([]string, error) {
		trainPath := s.trainV2Path(train.ProjectID, train.ID)
		integrationPath := trainV2IntegrationOperationPath(train.ProjectID, train.ID)
		if err := hub.WriteJSON(worktree, trainPath, train); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, integrationPath, operation); err != nil {
			return nil, err
		}
		return []string{trainPath, integrationPath}, nil
	}); err != nil {
		t.Fatal(err)
	}
	binDir := t.TempDir()
	logPath := filepath.Join(binDir, "git.log")
	gitWrapper := filepath.Join(binDir, "git")
	const wrapper = `#!/bin/sh
if [ "$1" = "fetch" ]; then
  printf '%s\n' "$*" >> "__STATE_CHECK_GIT_LOG__"
fi
exec /usr/bin/git "$@"
`
	if err := os.WriteFile(gitWrapper, []byte(strings.ReplaceAll(wrapper, "__STATE_CHECK_GIT_LOG__", logPath)), 0o700); err != nil {
		t.Fatal(err)
	}
	oldPath := os.Getenv("PATH")
	if err := os.Setenv("PATH", binDir+string(os.PathListSeparator)+oldPath); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Setenv("PATH", oldPath)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := s.StateCheck(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Valid {
		t.Fatalf("StateCheck invalid: %#v", result.Issues)
	}
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatalf("StateCheck touched Hub git fetch path: stat=%v", err)
	}
}

func TestStateCheckUsesLocalSQLiteWhenHubUnavailableAndLocked(t *testing.T) {
	s, _, _ := testService(t)
	db, err := sqlitestore.Open(s.Config.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s.Durability = db
	configuration := model.DefaultProjectConfiguration("example", time.Now().UTC())
	payload, err := json.Marshal(configuration)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.PutSharedProjection(context.Background(), "project_configuration", sqlitestore.SharedEntity{
		ID: configuration.ProjectID, Revision: int64(configuration.Revision), Payload: payload, UpdatedAt: configuration.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
	s.Hub.Config.Hub.RepositoryURL = filepath.Join(t.TempDir(), "unavailable-hub.git")
	hubLock, err := lockfile.Acquire(filepath.Join(s.Config.StateDir, "locks"), "hub-repository")
	if err != nil {
		t.Fatal(err)
	}
	defer hubLock.Release()

	result, err := s.StateCheck(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !result.Valid || len(result.Issues) != 0 {
		t.Fatalf("local StateCheck failed with Hub unavailable/locked: %#v", result)
	}
}

func TestStateCheckAllowsTwoIndependentActiveTrains(t *testing.T) {
	s, revision, _ := testService(t)
	now := time.Now().UTC()
	first := staleTrainV2ForRetirementTest(now)
	second := staleTrainV2ForRetirementTest(now.Add(time.Second))
	first.ID, second.ID = "EXM-TRN2", "EXM-TRN3"
	second.Items[0].TaskID = "EXM-TSK2"
	first.Status, second.Status = model.TrainV2Running, model.TrainV2Running
	first.Items[0].Status, second.Items[0].Status = model.TrainV2ItemRunning, model.TrainV2ItemRunning
	first.Items[0].Attempts[0].Status, second.Items[0].Attempts[0].Status = model.TrainV2AttemptRunning, model.TrainV2AttemptRunning
	first.Items[0].Attempts[0].FinishedAt, second.Items[0].Attempts[0].FinishedAt = nil, nil
	if _, err := s.Hub.Transact(context.Background(), revision, "test: seed independent active Trains", func(worktree string) ([]string, error) {
		firstPath, secondPath := s.trainV2Path("example", first.ID), s.trainV2Path("example", second.ID)
		if err := hub.WriteJSON(worktree, firstPath, first); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, secondPath, second); err != nil {
			return nil, err
		}
		return []string{firstPath, secondPath}, nil
	}); err != nil {
		t.Fatal(err)
	}
	result, err := s.StateCheck(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !result.Valid {
		t.Fatalf("independent active Trains were rejected: %#v", result.Issues)
	}
	for _, issue := range result.Issues {
		if issue.Code == "MULTIPLE_ACTIVE_TRAINS" {
			t.Fatal("StateCheck retained project-wide active Train conflict")
		}
	}
}

func TestStateCheckReportsDuplicateTrainTaskOwnership(t *testing.T) {
	s, _, _ := testService(t)
	db, err := sqlitestore.Open(s.Config.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s.Durability = db
	configuration := model.DefaultProjectConfiguration("example", time.Now().UTC())
	payload, err := json.Marshal(configuration)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.PutSharedProjection(context.Background(), "project_configuration", sqlitestore.SharedEntity{ID: "example", Revision: int64(configuration.Revision), Payload: payload, UpdatedAt: configuration.UpdatedAt.UTC().Format(time.RFC3339Nano)}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	first := staleTrainV2ForRetirementTest(now)
	second := staleTrainV2ForRetirementTest(now.Add(time.Second))
	first.ID, second.ID = "EXM-TRN4", "EXM-TRN5"
	first.Status, second.Status = model.TrainV2Planned, model.TrainV2Planned
	first.Items[0].Status, second.Items[0].Status = model.TrainV2ItemQueued, model.TrainV2ItemQueued
	first.Items[0].Attempts, second.Items[0].Attempts = nil, nil
	firstPayload, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	secondPayload, err := json.Marshal(second)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []struct {
		train   model.TrainV2
		payload []byte
	}{
		{train: first, payload: firstPayload},
		{train: second, payload: secondPayload},
	} {
		if err := db.PutSharedProjection(context.Background(), "train", sqlitestore.SharedEntity{ID: value.train.ID, Revision: int64(value.train.Revision), Payload: value.payload, UpdatedAt: value.train.UpdatedAt.UTC().Format(time.RFC3339Nano)}); err != nil {
			t.Fatal(err)
		}
	}
	result, err := s.StateCheck(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Valid {
		t.Fatal("StateCheck accepted duplicate Task ownership")
	}
	for _, issue := range result.Issues {
		if issue.Code == "DUPLICATE_TRAIN_TASK_MEMBERSHIP" {
			return
		}
	}
	t.Fatalf("duplicate Task ownership issue missing: %#v", result.Issues)
}
