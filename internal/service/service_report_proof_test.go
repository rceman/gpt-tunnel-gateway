package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestRunFinalizeRequiresPublishedBranchAtomically(t *testing.T) {
	s, hubRevision, _ := testService(t)
	ctx := context.Background()
	task, created, err := s.TaskCreate(ctx, TaskCreateInput{
		ProjectID:          "example",
		Title:              "Push first",
		Objective:          "Require durable finalization.",
		Slug:               "push-first",
		AcceptanceCriteria: []string{"durable"},
		OperationClass:     "implementation",
		CreatedBy:          "gpt",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: hubRevision,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := s.PlanUpdate(ctx, PlanUpdateInput{
		ProjectID:        "example",
		Title:            planString("Push first"),
		Summary:          planString("Push first"),
		CurrentObjective: planString("Push before finalize."),
		ActiveTaskID:     planString(task.ID),
		UpdatedBy:        "gpt",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: created.Hub.After,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	run, dispatch, err := s.TaskDispatch(ctx, DispatchInput{
		TaskID: task.ID,
		WriteOptions: WriteOptions{
			ExpectedHubRevision: plan.Hub.After,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	project := s.Config.Projects["example"]
	if err := os.WriteFile(filepath.Join(project.Root, "push-first.txt"), []byte("first\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, project.Root, "add", "push-first.txt")
	testutil.Git(t, project.Root, "commit", "-m", "push first")
	completion := model.Completion{SchemaVersion: 1, RunID: run.ID, TaskSHA256: task.SHA256, Status: "succeeded", Summary: "done", GateResults: []model.CompletionGateResult{}, AcceptanceCoverage: []string{"AC1"}, Deviations: []string{}, RemainingRisks: []string{}}
	if err := fsutil.WriteJSONAtomic(run.CompletionPath, completion, 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := s.Hub.RemoteRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.RunFinalize(ctx, FinalizeInput{
		RunID: run.ID,
		WriteOptions: WriteOptions{
			ExpectedHubRevision: dispatch.Hub.After,
		},
	}); err == nil || !strings.Contains(err.Error(), "pushed") {
		t.Fatalf("pre-push finalization was accepted: %v", err)
	}
	active, err := s.RunRead(ctx, run.ID)
	if err != nil || active.Status != "awaiting_result" {
		t.Fatalf("pre-push finalization changed run state: %#v %v", active, err)
	}
	if _, err := s.RunReport(ctx, run.ID); err == nil {
		t.Fatal("pre-push finalization created a report")
	}
	state, err := s.taskState(ctx, task)
	if err != nil || state.Status != "dispatched" {
		t.Fatalf("pre-push finalization changed task state: %#v %v", state, err)
	}
	currentPlan, err := s.PlanRead(ctx, task.ProjectID)
	if err != nil || currentPlan.ActiveRunID != run.ID {
		t.Fatalf("pre-push finalization changed plan state: %#v %v", currentPlan, err)
	}
	after, err := s.Hub.RemoteRevision(ctx)
	if err != nil || after != before {
		t.Fatalf("pre-push finalization changed hub revision: before=%s after=%s err=%v", before, after, err)
	}

	testutil.Git(t, project.Root, "push", "origin", task.Branch)
	remote := strings.TrimSpace(testutil.Git(t, project.Root, "remote", "get-url", "origin"))
	moverParent := t.TempDir()
	mover := filepath.Join(moverParent, "mover")
	testutil.Git(t, moverParent, "clone", "--no-local", "--branch", task.Branch, remote, mover)
	testutil.Git(t, mover, "config", "user.name", "Test User")
	testutil.Git(t, mover, "config", "user.email", "test@example.invalid")
	if err := os.WriteFile(filepath.Join(mover, "moved.txt"), []byte("moved\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, mover, "add", "moved.txt")
	testutil.Git(t, mover, "commit", "-m", "move published branch")
	testutil.Git(t, mover, "push", "origin", task.Branch)
	if _, _, err := s.RunFinalize(ctx, FinalizeInput{
		RunID: run.ID,
		WriteOptions: WriteOptions{
			ExpectedHubRevision: dispatch.Hub.After,
		},
	}); err == nil || !strings.Contains(err.Error(), "pushed") {
		t.Fatalf("finalization accepted a branch pointing elsewhere: %v", err)
	}
}
