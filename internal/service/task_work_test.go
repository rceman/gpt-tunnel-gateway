package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/persistence"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
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
	seedSharedTrainForTaskWorkTest(t, s, train)
	seedLocalCodingAgentSessionForTaskWorkTest(t, s)
	ensureTaskWorkMirrorForTest(t, s)
	s.Hub.Config.Hub.RepositoryURL = filepath.Join(t.TempDir(), "unavailable-hub.git")
	if train.ID == "" || operation.Status != model.TrainV2Planned {
		t.Fatalf("invalid Train creation: %#v %#v", train, operation)
	}

	first, err := s.TaskWork(context.Background(), TaskWorkInput{TaskID: task.ID, AgentID: "coder-example"})
	if err != nil {
		t.Fatal(err)
	}
	if first.TaskID != task.ID || first.TrainID != train.ID || first.ItemPosition != 0 || first.AttemptNumber != 1 || first.AttemptStatus != model.TrainV2AttemptRunning || first.Text == "" {
		t.Fatalf("unexpected Task work result: %#v", first)
	}
	afterStart, err := s.Durability.ReadSharedEntity(context.Background(), "train", train.ID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.TaskWork(context.Background(), TaskWorkInput{TaskID: task.ID, AgentID: "coder-example"})
	if err != nil {
		t.Fatal(err)
	}
	afterResume, err := s.Durability.ReadSharedEntity(context.Background(), "train", train.ID)
	if err != nil {
		t.Fatal(err)
	}
	if second.TrainID != first.TrainID || second.AttemptNumber != first.AttemptNumber || afterResume.Revision != afterStart.Revision {
		t.Fatalf("Task work was not an idempotent resume: first=%#v second=%#v revisions=%d/%d", first, second, afterStart.Revision, afterResume.Revision)
	}
	if _, err := os.Stat(filepath.Join(s.Config.StateDir, "runs")); !os.IsNotExist(err) {
		t.Fatalf("Task work created legacy runs storage: %v", err)
	}
}

func TestTaskFinalizeOwnsCheckpointByTaskIdentity(t *testing.T) {
	s, revision, _ := testService(t)
	revision = enableTrainV2ForTest(t, s, revision)
	task, revision := readyTrainTaskForTest(t, s, revision, "Task identity finalize")
	train, _, err := s.TrainV2Create(context.Background(), TrainV2CreateInput{
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
	seedSharedTrainForTaskWorkTest(t, s, train)
	seedLocalCodingAgentSessionForTaskWorkTest(t, s)
	ensureTaskWorkMirrorForTest(t, s)
	s.Hub.Config.Hub.RepositoryURL = filepath.Join(t.TempDir(), "unavailable-hub.git")
	work, err := s.TaskWork(context.Background(), TaskWorkInput{TaskID: task.ID, AgentID: "coder-example"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work.WorktreePath, "task-change.txt"), []byte("server-owned checkpoint\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	project, err := s.projectConfig("example")
	if err != nil {
		t.Fatal(err)
	}
	project.Root = work.WorktreePath
	if _, err := s.Git.CommitCandidate(context.Background(), project, "server-owned checkpoint"); err != nil {
		t.Fatal(err)
	}
	result, err := s.TaskFinalize(context.Background(), TaskFinalizeInput{TaskID: task.ID})
	if err != nil {
		t.Fatal(err)
	}
	if result.Report.TaskID != task.ID || result.Report.TrainID != train.ID || result.Report.AttemptNumber != work.AttemptNumber || result.Report.Status != "succeeded" {
		t.Fatalf("unexpected Task finalize result: %#v", result)
	}
	if result.Report.Repository.Head == "" || !result.Report.Repository.WorktreeClean {
		t.Fatalf("Task finalize did not create a clean server-owned checkpoint: %#v", result.Report.Repository)
	}
	if completionPath, pathErr := s.trainV2AttemptCompletionPath(context.Background(), "example", train.ID, task.ID, work.ItemPosition, work.AttemptNumber); pathErr != nil {
		t.Fatal(pathErr)
	} else if _, statErr := os.Stat(completionPath); !os.IsNotExist(statErr) {
		t.Fatalf("Agent completion file unexpectedly exists at %s: %v", completionPath, statErr)
	}
}

func seedSharedTrainForTaskWorkTest(t *testing.T, s *Service, train model.TrainV2) {
	t.Helper()
	if s.TrainEvidence == nil {
		s.TrainEvidence = persistence.NewLocalEvidenceStore(s.Config.StateDir)
	}
	payload, err := json.Marshal(train)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Durability.PutSharedProjection(context.Background(), "train", sqlitestore.SharedEntity{
		ID: train.ID, Revision: int64(train.Revision), Payload: payload, UpdatedAt: train.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
}

func seedLocalCodingAgentSessionForTaskWorkTest(t *testing.T, s *Service) {
	t.Helper()
	ref := "example_master"
	if _, err := durableSession.NewStore(s.Config.StateDir).Create(durableSession.CreateInput{
		ProjectID: "example", ProjectCode: "EXM", Role: durableSession.RoleAgent,
		SessionType: durableSession.SessionTypeChatGPT, SessionRef: &ref,
	}); err != nil {
		t.Fatal(err)
	}
}

func ensureTaskWorkMirrorForTest(t *testing.T, s *Service) {
	t.Helper()
	if err := s.Git.EnsureMirror(context.Background(), s.Config.Projects["example"]); err != nil {
		t.Fatal(err)
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
	if train.ID == "" || operation.Status != model.TrainV2Planned {
		t.Fatal("test Train was not persisted")
	}
}
