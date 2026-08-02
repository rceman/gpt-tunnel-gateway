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

func TestProjectRegisterCreatesCanonicalIdlePlan(t *testing.T) {
	s, _, _ := testService(t)
	plan, err := s.PlanRead(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	if plan.SchemaVersion != model.PlanSchemaVersion || plan.Revision != 1 {
		t.Fatalf("unexpected plan identity: %#v", plan)
	}
	if plan.ActiveTaskID != "" || plan.ActiveRunID != "" || len(plan.Queue) != 0 || len(plan.Sections) != 0 {
		t.Fatalf("registration created non-idle plan: %#v", plan)
	}
}

func TestStateCheckReportsLegacyPlanAndGraphIssuesTogether(t *testing.T) {
	s, hubRevision, _ := testService(t)
	legacy := `{"schema_version":1,"project_id":"example","revision":1,"summary":"legacy","body":"# legacy","updated_by":"test","updated_at":"2026-08-01T00:00:00Z"}`
	_, err := s.Hub.Transact(context.Background(), hubRevision, "test: install invalid plan", func(worktree string) ([]string, error) {
		path := s.planPath("example")
		return []string{path}, hub.WriteText(worktree, path, legacy)
	})
	if err != nil {
		t.Fatal(err)
	}
	s.Config.Projects["missing"] = s.Config.Projects["example"]
	result, err := s.StateCheck(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Valid {
		t.Fatal("invalid graph was accepted")
	}
	var codes []string
	for _, issue := range result.Issues {
		codes = append(codes, issue.Code)
	}
	joined := strings.Join(codes, ",")
	if !strings.Contains(joined, "LEGACY_PLAN_BODY") || !strings.Contains(joined, "CONFIGURED_PROJECT_MISSING") {
		t.Fatalf("preflight did not report all independent blockers: %s", joined)
	}
}

func TestStateRepairClearsOnlyObsoletePointerAndPreservesHistory(t *testing.T) {
	s, hubRevision, _ := testService(t)
	fixture, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "historical-run-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	task := model.Task{SchemaVersion: model.SchemaVersion, ID: "11111111-1111-4111-8111-111111111111", ProjectID: "example", Title: "Historical task", Objective: "Preserve immutable history during cutover.", Branch: "feature/historical", BaseRevision: strings.Repeat("b", 40), AcceptanceCriteria: []string{"history remains unchanged"}, CreatedBy: "test", CreatedAt: time.Now().UTC()}
	task.SHA256, err = model.HashTask(task)
	if err != nil {
		t.Fatal(err)
	}
	fixtureText := strings.Replace(string(fixture), `"task_id": "historical-task"`, fmt.Sprintf(`"task_id": "%s"`, task.ID), 1)
	fixtureText = strings.Replace(fixtureText, `"task_sha256": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"`, fmt.Sprintf(`"task_sha256": "%s"`, task.SHA256), 1)
	plan, err := s.PlanRead(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	plan.ActiveTaskID = "historical-task"
	plan.ActiveRunID = "11111111-1111-4111-8111-111111111111"
	plan.Revision++
	plan.UpdatedBy = "test"
	tx, err := s.Hub.Transact(context.Background(), hubRevision, "test: install obsolete pointer", func(worktree string) ([]string, error) {
		planPath := s.planPath("example")
		runPath := s.runPath("example", "11111111-1111-4111-8111-111111111111")
		taskPath := s.taskPath("example", task.ID)
		if err := hub.WriteJSON(worktree, taskPath, task); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, planPath, plan); err != nil {
			return nil, err
		}
		if err := hub.WriteText(worktree, runPath, fixtureText); err != nil {
			return nil, err
		}
		return []string{taskPath, planPath, runPath}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	beforeRun, err := s.Hub.ReadFile(context.Background(), s.runPath("example", "11111111-1111-4111-8111-111111111111"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.StateRepair(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Applied || result.OldHubSHA != tx.After || len(result.Actions) != 1 {
		t.Fatalf("unexpected repair result: %#v", result)
	}
	var repaired model.Plan
	if err := s.Hub.ReadJSON(context.Background(), s.planPath("example"), &repaired); err != nil {
		t.Fatal(err)
	}
	if repaired.ActiveTaskID != "" || repaired.ActiveRunID != "" {
		t.Fatalf("obsolete pointers remain: %#v", repaired)
	}
	afterRun, err := s.Hub.ReadFile(context.Background(), s.runPath("example", "11111111-1111-4111-8111-111111111111"))
	if err != nil {
		t.Fatal(err)
	}
	if string(afterRun) != string(beforeRun) {
		t.Fatal("immutable history record was changed by repair")
	}
}

func installHistoricalOnlyDispatchedTask(t *testing.T, s *Service, hubRevision, projectHead, runID, branch string) (model.Task, string, []byte, []byte) {
	t.Helper()
	ctx := context.Background()
	task, created, err := s.TaskCreate(ctx, TaskCreateInput{
		ProjectID: "example", Title: "Historical-only task", Objective: "Close stale mutable state after protocol cutover.",
		Branch: branch, BaseRevision: projectHead, AcceptanceCriteria: []string{"immutable history remains unchanged"}, CreatedBy: "test",
		WriteOptions: WriteOptions{ExpectedHubRevision: hubRevision},
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
	s, hubRevision, projectHead := testService(t)
	task, created, err := s.TaskCreate(context.Background(), TaskCreateInput{
		ProjectID: "example", Title: "Unlinked dispatched task", Objective: "Remain invalid without historical evidence.",
		Branch: "feature/unlinked", BaseRevision: projectHead, AcceptanceCriteria: []string{"no automatic repair"}, CreatedBy: "test",
		WriteOptions: WriteOptions{ExpectedHubRevision: hubRevision},
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

func TestTaskCancelIgnoresHistoricalAwaitingResult(t *testing.T) {
	s, revision, projectHead := testService(t)
	task, revision, _, _ := installHistoricalOnlyDispatchedTask(t, s, revision, projectHead, "33333333-3333-4333-8333-333333333333", "feature/history-cancel")
	result, err := s.TaskCancel(context.Background(), task.ID, revision)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "cancelled" {
		t.Fatalf("status=%s", result.Status)
	}
}

func TestStateRepairRefusesWhenAnOperationalRunExists(t *testing.T) {
	s, _, _, _ := dispatchedRun(t, "feature/operational-run")
	result, err := s.StateRepair(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Actions) != 0 {
		t.Fatalf("operational task was eligible for historical repair: %#v", result.Actions)
	}
}

func TestTaskReadDoesNotSelectHistoricalAwaitingResult(t *testing.T) {
	s, revision, projectHead := testService(t)
	task, _, _, _ := installHistoricalOnlyDispatchedTask(t, s, revision, projectHead, "44444444-4444-4444-8444-444444444444", "feature/history-read")
	if _, err := s.TaskRead(context.Background(), task.ID); err == nil || !strings.Contains(err.Error(), "found 0") {
		t.Fatalf("historical run became an execution packet: %v", err)
	}
}
