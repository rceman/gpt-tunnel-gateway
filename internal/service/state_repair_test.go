package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func installHistoricalOnlyDispatchedTask(t *testing.T, s *Service, hubRevision, projectHead, runID, branch string) (model.Task, string, []byte, []byte) {
	t.Helper()
	ctx := context.Background()
	task, created, err := s.TaskCreate(ctx, TaskCreateInput{
		ProjectID:          "example",
		Title:              "Historical-only task",
		Objective:          "Close stale mutable state after protocol cutover.",
		Slug:               strings.TrimPrefix(branch, "feature/"),
		AcceptanceCriteria: []string{"immutable history remains unchanged"},
		OperationClass:     "implementation",
		CreatedBy:          "test",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: hubRevision,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "historical-run-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	fixtureText := strings.Replace(string(fixture), `"id": "11111111-1111-4111-8111-111111111111"`, fmt.Sprintf(`"id": "%s"`, runID), 1)
	fixtureText = strings.Replace(fixtureText, `"task_id": "historical-task"`, fmt.Sprintf(`"task_id": "%s"`, task.ID), 1)
	fixtureText = strings.Replace(fixtureText, `"task_sha256": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"`, fmt.Sprintf(`"task_sha256": "%s"`, task.SHA256), 1)
	state, err := s.taskState(ctx, task)
	if err != nil {
		t.Fatal(err)
	}
	state.Status = "dispatched"
	state.UpdatedAt = time.Now().UTC()
	taskPath := s.taskPath(task.ProjectID, task.ID)
	statePath := s.taskStatePath(task.ProjectID, task.ID)
	runPath := s.runPath(task.ProjectID, runID)
	tx, err := s.Hub.Transact(ctx, created.Hub.After, "test: install history-only dispatched task", func(worktree string) ([]string, error) {
		if err := hub.WriteJSON(worktree, statePath, state); err != nil {
			return nil, err
		}
		if err := hub.WriteText(worktree, runPath, fixtureText); err != nil {
			return nil, err
		}
		return []string{statePath, runPath}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	taskBytes, err := s.Hub.ReadFile(ctx, taskPath)
	if err != nil {
		t.Fatal(err)
	}
	runBytes, err := s.Hub.ReadFile(ctx, runPath)
	if err != nil {
		t.Fatal(err)
	}
	return task, tx.After, taskBytes, runBytes
}

func TestStateRepairTerminalizesOnlyHistoricalDispatchedTasks(t *testing.T) {
	s, hubRevision, projectHead := testService(t)
	first, revision, firstTaskBytes, firstRunBytes := installHistoricalOnlyDispatchedTask(t, s, hubRevision, projectHead, "11111111-1111-4111-8111-111111111111", "feature/history-one")
	second, revision, secondTaskBytes, secondRunBytes := installHistoricalOnlyDispatchedTask(t, s, revision, projectHead, "22222222-2222-4222-8222-222222222222", "feature/history-two")

	dryRun, err := s.StateRepair(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if dryRun.Applied || len(dryRun.Actions) != 2 {
		t.Fatalf("unexpected dry-run: %#v", dryRun)
	}
	for _, action := range dryRun.Actions {
		if action.Kind != "task_state" || action.OldStatus != "dispatched" || action.NewStatus != "cancelled" || action.Reason != historyOnlyTaskRepairReason {
			t.Fatalf("unexpected repair action: %#v", action)
		}
	}

	result, err := s.StateRepair(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Applied || result.OldHubSHA != revision || result.NewHubSHA == "" || len(result.ChangedPaths) != 2 {
		t.Fatalf("unexpected applied repair: %#v", result)
	}
	wantPaths := []string{s.taskStatePath("example", first.ID), s.taskStatePath("example", second.ID)}
	sort.Strings(wantPaths)
	gotPaths := append([]string{}, result.ChangedPaths...)
	sort.Strings(gotPaths)
	if !reflect.DeepEqual(gotPaths, wantPaths) {
		t.Fatalf("changed unrelated paths: got=%v want=%v", gotPaths, wantPaths)
	}
	for _, task := range []model.Task{first, second} {
		record, err := s.TaskReadRecord(context.Background(), task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if record.State.Status != "cancelled" {
			t.Fatalf("task %s status=%s", task.ID, record.State.Status)
		}
	}
	if got, err := s.Hub.ReadFile(context.Background(), s.taskPath("example", first.ID)); err != nil || string(got) != string(firstTaskBytes) {
		t.Fatal("first immutable task record changed")
	}
	if got, err := s.Hub.ReadFile(context.Background(), s.taskPath("example", second.ID)); err != nil || string(got) != string(secondTaskBytes) {
		t.Fatal("second immutable task record changed")
	}
	if got, err := s.Hub.ReadFile(context.Background(), s.runPath("example", "11111111-1111-4111-8111-111111111111")); err != nil || string(got) != string(firstRunBytes) {
		t.Fatal("first immutable history record changed")
	}
	if got, err := s.Hub.ReadFile(context.Background(), s.runPath("example", "22222222-2222-4222-8222-222222222222")); err != nil || string(got) != string(secondRunBytes) {
		t.Fatal("second immutable history record changed")
	}
	if _, err := s.Hub.ReadFile(context.Background(), s.reportPath("example", "11111111-1111-4111-8111-111111111111")); err == nil {
		t.Fatal("repair fabricated a report")
	}
	check, err := s.StateCheck(context.Background())
	if err != nil || !check.Valid {
		t.Fatalf("state remained invalid after repair: %#v %v", check, err)
	}
}

func TestStateRepairDoesNotTerminalizeWithoutHistoricalOnlyEvidence(t *testing.T) {
	s, hubRevision, _ := testService(t)
	task, created, err := s.TaskCreate(context.Background(), TaskCreateInput{
		ProjectID:          "example",
		Title:              "Unlinked dispatched task",
		Objective:          "Remain invalid without historical evidence.",
		Slug:               "unlinked",
		AcceptanceCriteria: []string{"no automatic repair"},
		OperationClass:     "implementation",
		CreatedBy:          "test",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: hubRevision,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	state, err := s.taskState(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}
	state.Status = "dispatched"
	state.UpdatedAt = time.Now().UTC()
	_, err = s.Hub.Transact(context.Background(), created.Hub.After, "test: install unlinked dispatched task", func(worktree string) ([]string, error) {
		path := s.taskStatePath("example", task.ID)
		return []string{path}, hub.WriteJSON(worktree, path, state)
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.StateRepair(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Actions) != 0 {
		t.Fatalf("unlinked task was automatically repaired: %#v", result.Actions)
	}
}
