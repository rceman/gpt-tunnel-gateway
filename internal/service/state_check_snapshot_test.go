package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

func TestStateCheckUsesOneHubSnapshotForCompleteValidation(t *testing.T) {
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
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(data), "\n"); got != 1 {
		t.Fatalf("Hub fetch count = %d, want one pinned snapshot fetch; log=%q", got, data)
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
	s, revision, _ := testService(t)
	now := time.Now().UTC()
	first := staleTrainV2ForRetirementTest(now)
	second := staleTrainV2ForRetirementTest(now.Add(time.Second))
	first.ID, second.ID = "EXM-TRN4", "EXM-TRN5"
	first.Status, second.Status = model.TrainV2Planned, model.TrainV2Planned
	first.Items[0].Status, second.Items[0].Status = model.TrainV2ItemQueued, model.TrainV2ItemQueued
	first.Items[0].Attempts, second.Items[0].Attempts = nil, nil
	if _, err := s.Hub.Transact(context.Background(), revision, "test: seed duplicate Train Task ownership", func(worktree string) ([]string, error) {
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
