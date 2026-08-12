package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/gates"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestTrainV2WatcherDisabledStartObserveAndFinalize(t *testing.T) {
	s, hubRevision, _ := testService(t)
	s.Config.Projects["example"] = func() config.ProjectConfig {
		project := s.Config.Projects["example"]
		project.Watcher.Mode = "disabled"
		return project
	}()
	script := filepath.Join(t.TempDir(), "airelay")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ncase \"$1\" in\nsession-status) printf 'Controller: reachable\\nState: idle\\n' ;;\ntail) printf 'working\\n' ;;\nprompt) exit 0 ;;\nesac\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	s.Config.AirelayCommand = script
	hubRevision = enableTrainV2ForTest(t, s, hubRevision)
	task, hubRevision := readyTrainTaskForTest(t, s, hubRevision, "Watcher-disabled Train execution")
	train, operation, err := s.TrainV2Create(context.Background(), TrainV2CreateInput{ProjectID: "example", TaskIDs: []string{task.ID}, CreatedBy: "planner", WriteOptions: WriteOptions{ExpectedHubRevision: hubRevision}})
	if err != nil {
		t.Fatal(err)
	}
	started, err := s.TrainV2Start(context.Background(), TrainV2StartInput{ProjectID: "example", TrainID: train.ID, StartedBy: "planner", WriteOptions: WriteOptions{ExpectedHubRevision: operation.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.WatcherObserve(context.Background(), WatcherObserveInput{ProjectID: "example"}); err != nil {
		t.Fatalf("watcher observation blocked Train execution: %v", err)
	}
	publishServerOwnedChange(t, started.Runtime.WorktreePath, started.Run.Branch, "watcher-disabled.txt", "watcher-disabled finalization")
	s.gateExecutorWithScope = func(_ context.Context, _ string, names []string, _ gates.TestScope) ([]model.CompletionGateResult, error) {
		return fakeReceiptResults(names), nil
	}
	if _, result, err := s.RunFinalize(context.Background(), FinalizeInput{RunID: started.Run.ID, Summary: "Completed without watcher supervision."}); err != nil || result.Status != "TASK_FINALIZED" {
		t.Fatalf("watcher-disabled Train finalization failed: result=%#v err=%v", result, err)
	}
}

func TestWatcherDisabledAdmissionFailsClosed(t *testing.T) {
	s, _, _ := testService(t)
	if _, err := s.WatcherStart(context.Background(), "example"); err == nil {
		t.Fatal("disabled watcher admission unexpectedly succeeded")
	}
}
