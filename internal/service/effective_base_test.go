package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestTaskDispatchBindsImplementationRunToRefreshedCanonicalHead(t *testing.T) {
	s, hubRevision, oldHead := testService(t)
	ctx := context.Background()
	task, created, err := s.TaskCreate(ctx, TaskCreateInput{
		ProjectID: "example", Title: "Stale implementation", Objective: "Dispatch from refreshed main.",
		Slug: "stale-implementation", AcceptanceCriteria: []string{"run from current main"},
		OperationClass: "implementation", CreatedBy: "test", WriteOptions: WriteOptions{ExpectedHubRevision: hubRevision},
	})
	if err != nil {
		t.Fatal(err)
	}
	if task.BaseRevision != oldHead {
		t.Fatalf("task was not created at the original head: got=%s want=%s", task.BaseRevision, oldHead)
	}
	originalTaskHash := task.SHA256

	project := s.Config.Projects[task.ProjectID]
	if err := os.WriteFile(filepath.Join(project.Root, "effective-base.txt"), []byte("canonical head\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, project.Root, "add", "effective-base.txt")
	testutil.Git(t, project.Root, "commit", "-m", "advance canonical main")
	testutil.Git(t, project.Root, "push", "origin", "main")
	newHead := strings.TrimSpace(testutil.Git(t, project.Root, "rev-parse", "HEAD"))
	if newHead == oldHead {
		t.Fatal("canonical main did not advance")
	}

	plan, err := s.PlanUpdate(ctx, PlanUpdateInput{
		ProjectID: task.ProjectID, Title: planString("Stale implementation"), Summary: planString("Stale implementation"),
		CurrentObjective: planString("Dispatch from current main."), ActiveTaskID: planString(task.ID), UpdatedBy: "test",
		WriteOptions: WriteOptions{ExpectedHubRevision: created.Hub.After},
	})
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := s.TaskDispatch(ctx, DispatchInput{TaskID: task.ID, WriteOptions: WriteOptions{ExpectedHubRevision: plan.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	if run.BaseRevision != newHead {
		t.Fatalf("run was not pinned to refreshed canonical head: got=%s want=%s", run.BaseRevision, newHead)
	}
	if run.BaseRevision == task.BaseRevision {
		t.Fatal("run retained stale task base")
	}
	status, err := s.Git.WorktreeStatus(ctx, project)
	if err != nil {
		t.Fatal(err)
	}
	if status.Branch != task.Branch || status.Head != newHead || !status.Clean {
		t.Fatalf("branch was not prepared from effective base: %#v", status)
	}
	readBack, err := s.TaskReadRecord(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if readBack.Task.BaseRevision != oldHead || readBack.Task.SHA256 != originalTaskHash {
		t.Fatalf("immutable task changed during dispatch: base=%s hash=%s", readBack.Task.BaseRevision, readBack.Task.SHA256)
	}
	if _, err := s.failRun(ctx, run, task, "failed", "bounded compatibility proof", mustHubRevision(t, s)); err != nil {
		t.Fatal(err)
	}
	report, err := s.RunReport(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if report.Repository.DiffScope != run.BaseRevision+".."+report.Repository.Head {
		t.Fatalf("durable report did not use run execution base: %#v", report.Repository)
	}
}

func TestDispatchExecutionBasePreservesPinnedOperationLineage(t *testing.T) {
	s, _, oldHead := testService(t)
	ctx := context.Background()
	project := s.Config.Projects["example"]
	task := model.Task{ID: "EXM-TSK1", ProjectID: "example"}
	for _, operationClass := range []string{"correction", "release", "activation"} {
		got, err := s.dispatchExecutionBase(ctx, task, model.TaskRevision{OperationClass: operationClass, BaseRevision: oldHead}, project)
		if err != nil {
			t.Fatalf("%s: %v", operationClass, err)
		}
		if got != oldHead {
			t.Fatalf("%s lineage changed: got=%s want=%s", operationClass, got, oldHead)
		}
	}
}

func TestDispatchExecutionBaseTreatsEmptyOperationAsImplementationCompatibility(t *testing.T) {
	s, _, oldHead := testService(t)
	ctx := context.Background()
	project := s.Config.Projects["example"]
	if err := os.WriteFile(filepath.Join(project.Root, "legacy-effective-base.txt"), []byte("canonical head\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, project.Root, "add", "legacy-effective-base.txt")
	testutil.Git(t, project.Root, "commit", "-m", "advance legacy canonical main")
	testutil.Git(t, project.Root, "push", "origin", "main")
	newHead := strings.TrimSpace(testutil.Git(t, project.Root, "rev-parse", "HEAD"))
	got, err := s.dispatchExecutionBase(ctx, model.Task{ID: "EXM-TSK1", ProjectID: "example"}, model.TaskRevision{OperationClass: "", BaseRevision: oldHead}, project)
	if err != nil {
		t.Fatal(err)
	}
	if got != newHead {
		t.Fatalf("legacy task was not rebased to canonical head: got=%s want=%s", got, newHead)
	}
}
