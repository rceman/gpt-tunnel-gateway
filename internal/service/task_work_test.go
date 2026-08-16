package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestTaskWorkStartsAndResumesByTaskIdentity(t *testing.T) {
	s, revision, _ := testService(t)
	revision = enableTrainV2ForTest(t, s, revision)
	task, revision := readyTrainTaskForTest(t, s, revision, "Task identity work")
	train, operation, err := s.TrainV2Create(context.Background(), TrainV2CreateInput{
		ProjectID: "example",
		TaskIDs:   []string{task.ID},
		CreatedBy: "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: revision,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if train.ID == "" || operation.Hub.After == "" {
		t.Fatalf("invalid Train creation: %#v %#v", train, operation)
	}

	first, err := s.TaskWork(context.Background(), TaskWorkInput{TaskID: task.ID})
	if err != nil {
		t.Fatal(err)
	}
	if first.TaskID != task.ID || first.TrainID != train.ID || first.ItemPosition != 0 || first.AttemptNumber != 1 || first.AttemptStatus != model.TrainV2AttemptRunning || first.Text == "" {
		t.Fatalf("unexpected Task work result: %#v", first)
	}
	afterStart, err := s.Hub.RemoteRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.TaskWork(context.Background(), TaskWorkInput{TaskID: task.ID})
	if err != nil {
		t.Fatal(err)
	}
	afterResume, err := s.Hub.RemoteRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if second.TrainID != first.TrainID || second.AttemptNumber != first.AttemptNumber || afterResume != afterStart {
		t.Fatalf("Task work was not an idempotent resume: first=%#v second=%#v revisions=%s/%s", first, second, afterStart, afterResume)
	}
	if _, err := os.Stat(filepath.Join(s.Config.StateDir, "runs")); !os.IsNotExist(err) {
		t.Fatalf("Task work created legacy runs storage: %v", err)
	}
}

func TestTaskFinalizeOwnsCheckpointByTaskIdentity(t *testing.T) {
	s, revision, _ := testService(t)
	revision = enableTrainV2ForTest(t, s, revision)
	task, revision := readyTrainTaskForTest(t, s, revision, "Task identity finalize")
	train, operation, err := s.TrainV2Create(context.Background(), TrainV2CreateInput{
		ProjectID: "example",
		TaskIDs:   []string{task.ID},
		CreatedBy: "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: revision,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	work, err := s.TaskWork(context.Background(), TaskWorkInput{TaskID: task.ID})
	if err != nil {
		t.Fatal(err)
	}
	project, err := s.projectConfig("example")
	if err != nil {
		t.Fatal(err)
	}
	project.Root = work.WorktreePath
	startHead, _, _, err := s.Git.CurrentHead(context.Background(), project)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work.WorktreePath, "task-change.txt"), []byte("server-owned checkpoint\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := s.TaskFinalize(context.Background(), TaskFinalizeInput{TaskID: task.ID})
	if err != nil {
		t.Fatal(err)
	}
	if result.Report.TaskID != task.ID || result.Report.TrainID != train.ID || result.Report.AttemptNumber != work.AttemptNumber || result.Report.Status != "succeeded" || operation.Hub.After == result.Hub.Before {
		t.Fatalf("unexpected Task finalize result: %#v", result)
	}
	if result.Report.Repository.Head == startHead || !result.Report.Repository.WorktreeClean {
		t.Fatalf("Task finalize did not create a clean server-owned checkpoint: %#v", result.Report.Repository)
	}
	if completionPath, pathErr := s.trainV2AttemptCompletionPath(context.Background(), "example", train.ID, task.ID, work.ItemPosition, work.AttemptNumber); pathErr != nil {
		t.Fatal(pathErr)
	} else if _, statErr := os.Stat(completionPath); !os.IsNotExist(statErr) {
		t.Fatalf("Agent completion file unexpectedly exists at %s: %v", completionPath, statErr)
	}
}

func TestTaskWorkRejectsTaskOutsideCurrentTrain(t *testing.T) {
	s, revision, _ := testService(t)
	revision = enableTrainV2ForTest(t, s, revision)
	first, revision := readyTrainTaskForTest(t, s, revision, "Current Task")
	other, revision := readyTrainTaskForTest(t, s, revision, "Non-current Task")
	train, operation, err := s.TrainV2Create(context.Background(), TrainV2CreateInput{
		ProjectID: "example",
		TaskIDs:   []string{first.ID},
		CreatedBy: "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: revision,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.TaskWork(context.Background(), TaskWorkInput{TaskID: other.ID}); err == nil {
		t.Fatal("non-current Task was accepted")
	}
	if train.ID == "" || operation.Hub.After == "" {
		t.Fatal("test Train was not persisted")
	}
}
