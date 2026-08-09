package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func installTaskLifecycleState(t *testing.T, s *Service, task model.Task, state model.TaskState, expected string) string {
	t.Helper()
	tx, err := s.Hub.Transact(context.Background(), expected, "test: install lifecycle state", func(worktree string) ([]string, error) {
		path := s.taskStatePath(task.ProjectID, task.ID)
		return []string{path}, hub.WriteJSON(worktree, path, state)
	})
	if err != nil {
		t.Fatal(err)
	}
	return tx.After
}

func TestTaskDeferPreservesReviewedHeadAndNormalizesReason(t *testing.T) {
	s, revision, _ := testService(t)
	ctx := context.Background()
	task, created, err := s.TaskCreate(ctx, TaskCreateInput{
		ProjectID:          "example",
		Title:              "Defer task",
		Objective:          "Exercise task deferral.",
		Slug:               "defer",
		AcceptanceCriteria: []string{"state"},
		OperationClass:     "implementation",
		CreatedBy:          "test",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: revision,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	reviewed := strings.Repeat("c", 40)
	state := model.TaskState{SchemaVersion: model.SchemaVersion, TaskID: task.ID, TaskSHA256: task.SHA256, Status: "merge_ready", ReviewedHead: reviewed, UpdatedAt: time.Now().UTC()}
	revision = installTaskLifecycleState(t, s, task, state, created.Hub.After)
	result, err := s.TaskDefer(ctx, TaskDeferInput{
		TaskID: task.ID,
		Reason: "  outside integration scope  ",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: revision,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "deferred" || len(result.Hub.Paths) != 1 || result.Hub.Paths[0] != s.taskStatePath(task.ProjectID, task.ID) {
		t.Fatalf("unexpected defer result: %#v", result)
	}
	record, err := s.TaskReadRecord(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if record.State.Status != "deferred" || record.State.ReviewedHead != reviewed || record.State.DeferredReason != "outside integration scope" {
		t.Fatalf("unexpected deferred state: %#v", record.State)
	}
	if record.State.IntegrationBranch != "" || record.State.IntegrationHead != "" {
		t.Fatalf("defer unexpectedly recorded integration receipt: %#v", record.State)
	}
}

func TestTaskMarkMergeReadyRequiresCanonicalSuccessfulReport(t *testing.T) {
	s, revision, _ := testService(t)
	ctx := context.Background()
	task, created, err := s.TaskCreate(ctx, TaskCreateInput{
		ProjectID:          "example",
		Title:              "Merge review",
		Objective:          "Require a canonical report.",
		Slug:               "merge-review",
		AcceptanceCriteria: []string{"report"},
		OperationClass:     "implementation",
		CreatedBy:          "test",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: revision,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	state := model.TaskState{SchemaVersion: model.SchemaVersion, TaskID: task.ID, TaskSHA256: task.SHA256, Status: "completed", UpdatedAt: time.Now().UTC()}
	revision = installTaskLifecycleState(t, s, task, state, created.Hub.After)
	if _, err := s.TaskMarkMergeReady(ctx, TaskMarkMergeReadyInput{
		TaskID: task.ID,
		WriteOptions: WriteOptions{
			ExpectedHubRevision: revision,
		},
	}); err == nil || !strings.Contains(err.Error(), "no canonical successful report") {
		t.Fatalf("expected missing canonical report error, got %v", err)
	}
	current, err := s.Hub.RemoteRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if current != revision {
		t.Fatalf("rejected transition changed hub revision: got %s want %s", current, revision)
	}
}
