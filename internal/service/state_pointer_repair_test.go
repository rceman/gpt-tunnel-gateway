package service

import (
	"context"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func installStaleActivePointer(t *testing.T, runStatus, taskStatus string, mismatch bool) (*Service, model.Task, model.Run) {
	t.Helper()
	s, _, _, _ := dispatchedRun(t, "feature/stale-pointer")
	ctx := context.Background()
	plan, err := s.PlanRead(ctx, "example")
	if err != nil {
		t.Fatal(err)
	}
	activeTask, err := s.TaskReadRecord(ctx, plan.ActiveTaskID)
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.RunRead(ctx, plan.ActiveRunID)
	if err != nil {
		t.Fatal(err)
	}
	run.Status = runStatus
	if mismatch {
		other, _, err := s.TaskCreate(ctx, TaskCreateInput{
			ProjectID:          "example",
			Title:              "Mismatched pointer target",
			Objective:          "Provide a distinct task identity for the stale pointer test.",
			Slug:               "mismatched-pointer-target",
			AcceptanceCriteria: []string{"identity remains strict"},
			OperationClass:     "implementation",
			CreatedBy:          "test",
			WriteOptions: WriteOptions{
				ExpectedHubRevision: mustHubRevision(t, s),
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		run.TaskID = other.ID
	}
	state, err := s.taskState(ctx, activeTask.Task)
	if err != nil {
		t.Fatal(err)
	}
	state.Status = taskStatus
	state.UpdatedAt = time.Now().UTC()
	hubRevision, err := s.Hub.RemoteRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Hub.Transact(ctx, hubRevision, "test: install stale active pointer", func(worktree string) ([]string, error) {
		if err := hub.WriteJSON(worktree, s.runPath("example", run.ID), run); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, s.taskStatePath("example", activeTask.Task.ID), state); err != nil {
			return nil, err
		}
		return []string{s.runPath("example", run.ID), s.taskStatePath("example", activeTask.Task.ID)}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return s, activeTask.Task, run
}

func TestStateRepairNormalizesTerminalLifecyclePointerAndIsIdempotent(t *testing.T) {
	for _, tc := range []struct {
		name, runStatus, taskStatus string
	}{
		{name: "succeeded", runStatus: "succeeded", taskStatus: "completed"},
		{name: "needs_gpt_revision", runStatus: "needs_gpt_revision", taskStatus: "ready"},
		{name: "cancelled", runStatus: "failed", taskStatus: "cancelled"},
		{name: "superseded", runStatus: "failed", taskStatus: "superseded"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _, _ := installStaleActivePointer(t, tc.runStatus, tc.taskStatus, false)
			check, err := s.StateCheck(context.Background())
			if err != nil || check.Valid {
				t.Fatalf("stale pointer was not detected: %#v %v", check, err)
			}
			dryRun, err := s.StateRepair(context.Background(), false)
			if err != nil || len(dryRun.Actions) != 1 || dryRun.Actions[0].Kind != "plan_pointer" {
				t.Fatalf("unexpected dry-run: %#v %v", dryRun, err)
			}
			result, err := s.StateRepair(context.Background(), true)
			if err != nil || !result.Applied || len(result.ChangedPaths) != 1 {
				t.Fatalf("repair failed: %#v %v", result, err)
			}
			plan, err := s.PlanRead(context.Background(), "example")
			if err != nil || plan.ActiveTaskID != "" || plan.ActiveRunID != "" {
				t.Fatalf("pointers remain after repair: %#v %v", plan, err)
			}
			check, err = s.StateCheck(context.Background())
			if err != nil || !check.Valid {
				t.Fatalf("repaired graph is invalid: %#v %v", check, err)
			}
			replay, err := s.StateRepair(context.Background(), false)
			if err != nil || len(replay.Actions) != 0 {
				t.Fatalf("repair was not idempotent: %#v %v", replay, err)
			}
		})
	}
}

func TestStateRepairPreservesValidActivePairAndRejectsMismatchedPair(t *testing.T) {
	s, _, _, _ := dispatchedRun(t, "feature/valid-active-pair")
	dryRun, err := s.StateRepair(context.Background(), false)
	if err != nil || len(dryRun.Actions) != 0 {
		t.Fatalf("valid active pair was changed: %#v %v", dryRun, err)
	}
	s, _, _ = installStaleActivePointer(t, "awaiting_result", "ready", true)
	check, err := s.StateCheck(context.Background())
	if err != nil || check.Valid {
		t.Fatalf("mismatched pair was accepted: %#v %v", check, err)
	}
	dryRun, err = s.StateRepair(context.Background(), false)
	if err != nil || len(dryRun.Actions) != 1 || dryRun.Actions[0].Kind != "plan_pointer" {
		t.Fatalf("mismatched pair was not repairable: %#v %v", dryRun, err)
	}
}

func TestStateCheckPreservesAuthorizedPendingTaskTrainPointer(t *testing.T) {
	s, hubRevision, _ := testService(t)
	task, created, err := s.TaskCreate(context.Background(), TaskCreateInput{
		ProjectID:          "example",
		Title:              "Pending train task",
		Objective:          "Remain pending until the train dispatches it.",
		Slug:               "pending-train-task",
		AcceptanceCriteria: []string{"pending pointer is authorized"},
		OperationClass:     "implementation",
		CreatedBy:          "test",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: hubRevision,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.TaskTrainCreate(context.Background(), TaskTrainCreateInput{
		ProjectID: "example",
		TaskIDs:   []string{task.ID},
		CreatedBy: "test",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: created.Hub.After,
		},
	}); err != nil {
		t.Fatal(err)
	}
	check, err := s.StateCheck(context.Background())
	if err != nil || !check.Valid {
		t.Fatalf("authorized pending train pointer was rejected: %#v %v", check, err)
	}
	dryRun, err := s.StateRepair(context.Background(), false)
	if err != nil || len(dryRun.Actions) != 0 {
		t.Fatalf("authorized pending train pointer was repaired: %#v %v", dryRun, err)
	}
}
