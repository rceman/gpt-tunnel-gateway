package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
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
	s, revision, projectHead := testService(t)
	ctx := context.Background()
	task, created, err := s.TaskCreate(ctx, TaskCreateInput{ProjectID: "example", Title: "Defer task", Objective: "Exercise task deferral.", Branch: "feature/defer", BaseRevision: projectHead, AcceptanceCriteria: []string{"state"}, CreatedBy: "test", WriteOptions: WriteOptions{ExpectedHubRevision: revision}})
	if err != nil {
		t.Fatal(err)
	}
	reviewed := strings.Repeat("c", 40)
	state := model.TaskState{SchemaVersion: model.SchemaVersion, TaskID: task.ID, TaskSHA256: task.SHA256, Status: "merge_ready", ReviewedHead: reviewed, UpdatedAt: time.Now().UTC()}
	revision = installTaskLifecycleState(t, s, task, state, created.Hub.After)
	result, err := s.TaskDefer(ctx, TaskDeferInput{TaskID: task.ID, Reason: "  outside integration scope  ", WriteOptions: WriteOptions{ExpectedHubRevision: revision}})
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
	s, revision, projectHead := testService(t)
	ctx := context.Background()
	task, created, err := s.TaskCreate(ctx, TaskCreateInput{ProjectID: "example", Title: "Merge review", Objective: "Require a canonical report.", Branch: "feature/merge-review", BaseRevision: projectHead, AcceptanceCriteria: []string{"report"}, CreatedBy: "test", WriteOptions: WriteOptions{ExpectedHubRevision: revision}})
	if err != nil {
		t.Fatal(err)
	}
	state := model.TaskState{SchemaVersion: model.SchemaVersion, TaskID: task.ID, TaskSHA256: task.SHA256, Status: "completed", UpdatedAt: time.Now().UTC()}
	revision = installTaskLifecycleState(t, s, task, state, created.Hub.After)
	if _, err := s.TaskMarkMergeReady(ctx, TaskMarkMergeReadyInput{TaskID: task.ID, WriteOptions: WriteOptions{ExpectedHubRevision: revision}}); err == nil || !strings.Contains(err.Error(), "no canonical successful report") {
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

func TestTaskMarkMergeReadyUsesLatestSuccessfulReportAndDeferReusesHead(t *testing.T) {
	s, revision, projectHead := testService(t)
	ctx := context.Background()
	task, created, err := s.TaskCreate(ctx, TaskCreateInput{ProjectID: "example", Title: "Lifecycle report", Objective: "Use a canonical successful report.", Branch: "feature/lifecycle-report", BaseRevision: projectHead, AcceptanceCriteria: []string{"report"}, RequiredGates: []string{"gate"}, CreatedBy: "test", WriteOptions: WriteOptions{ExpectedHubRevision: revision}})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := s.PlanUpdate(ctx, PlanUpdateInput{ProjectID: "example", Title: planString("Lifecycle"), Summary: planString("Lifecycle"), CurrentObjective: planString("Lifecycle"), ActiveTaskID: planString(task.ID), UpdatedBy: "test", WriteOptions: WriteOptions{ExpectedHubRevision: created.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	run, dispatch, err := s.TaskDispatch(ctx, DispatchInput{TaskID: task.ID, WriteOptions: WriteOptions{ExpectedHubRevision: plan.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	project := s.Config.Projects[task.ProjectID]
	if err := os.WriteFile(filepath.Join(project.Root, "lifecycle.txt"), []byte("done\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, project.Root, "add", "lifecycle.txt")
	testutil.Git(t, project.Root, "commit", "-m", "lifecycle report")
	testutil.Git(t, project.Root, "push", "-u", "origin", task.Branch)
	completion := model.Completion{SchemaVersion: model.SchemaVersion, RunID: run.ID, TaskSHA256: task.SHA256, Status: "succeeded", Summary: "completed", GateResults: []model.CompletionGateResult{{ID: "G1", ExitCode: 0}}, AcceptanceCoverage: []string{"AC1"}, Deviations: []string{}, RemainingRisks: []string{}}
	if err := fsutil.WriteJSONAtomic(run.CompletionPath, completion, 0o600); err != nil {
		t.Fatal(err)
	}
	_, finalized, err := s.RunFinalize(ctx, FinalizeInput{RunID: run.ID, WriteOptions: WriteOptions{ExpectedHubRevision: dispatch.Hub.After}})
	if err != nil || finalized.Status != "TASK_FINALIZED" {
		t.Fatalf("finalize failed: %#v %v", finalized, err)
	}
	if _, err := s.RunReport(ctx, run.ID); err != nil {
		t.Fatalf("completed report was not readable: %v", err)
	}
	invalidState := model.TaskState{SchemaVersion: model.SchemaVersion, TaskID: task.ID, TaskSHA256: task.SHA256, Status: "ready", UpdatedAt: time.Now().UTC()}
	invalidRevision := installTaskLifecycleState(t, s, task, invalidState, finalized.Hub.After)
	if _, err := s.RunReport(ctx, run.ID); err == nil {
		t.Fatal("succeeded report was accepted for unrelated ready task state")
	}
	completedState := model.TaskState{SchemaVersion: model.SchemaVersion, TaskID: task.ID, TaskSHA256: task.SHA256, Status: "completed", UpdatedAt: time.Now().UTC()}
	completedRevision := installTaskLifecycleState(t, s, task, completedState, invalidRevision)
	ready, err := s.TaskMarkMergeReady(ctx, TaskMarkMergeReadyInput{TaskID: task.ID, WriteOptions: WriteOptions{ExpectedHubRevision: completedRevision}})
	if err != nil {
		t.Fatal(err)
	}
	record, err := s.TaskReadRecord(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if record.State.Status != "merge_ready" || record.State.ReviewedHead == "" || ready.Status != "merge_ready" {
		t.Fatalf("unexpected merge-ready state: %#v", record.State)
	}
	if _, err := s.RunReport(ctx, run.ID); err != nil {
		t.Fatalf("merge-ready report was not readable: %v", err)
	}
	testutil.Git(t, project.Root, "branch", "develop")
	testutil.Git(t, project.Root, "push", "-u", "origin", "develop")
	testutil.Git(t, project.Mirror, "config", "--unset-all", "remote.origin.fetch")
	testutil.Git(t, project.Mirror, "config", "--add", "remote.origin.fetch", "+refs/heads/*:refs/remotes/origin/*")
	testutil.Git(t, project.Mirror, "config", "--unset-all", "remote.origin.mirror")
	testutil.Git(t, project.Mirror, "fetch", "origin")
	deferred, err := s.TaskDefer(ctx, TaskDeferInput{TaskID: task.ID, Reason: "later integration", WriteOptions: WriteOptions{ExpectedHubRevision: ready.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RunReport(ctx, run.ID); err != nil {
		t.Fatalf("deferred report was not readable: %v", err)
	}
	record, err = s.TaskReadRecord(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if record.State.Status != "deferred" || deferred.Status != "deferred" {
		t.Fatalf("unexpected deferred state: %#v", record.State)
	}
	mergeReadyAgain := model.TaskState{SchemaVersion: model.SchemaVersion, TaskID: task.ID, TaskSHA256: task.SHA256, Status: "merge_ready", ReviewedHead: record.State.ReviewedHead, UpdatedAt: time.Now().UTC()}
	reset := installTaskLifecycleState(t, s, task, mergeReadyAgain, deferred.Hub.After)
	merged, err := s.TaskMarkMerged(ctx, TaskMarkMergedInput{TaskID: task.ID, IntegrationHead: record.State.ReviewedHead, WriteOptions: WriteOptions{ExpectedHubRevision: reset}})
	if err != nil {
		t.Fatal(err)
	}
	record, err = s.TaskReadRecord(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if merged.Status != "merged" || record.State.Status != "merged" || record.State.ReviewedHead == "" || record.State.IntegrationBranch != "develop" || record.State.IntegrationHead != record.State.ReviewedHead {
		t.Fatalf("unexpected merged result/state: %#v %#v", merged, record.State)
	}
	if _, err := s.RunReport(ctx, run.ID); err != nil {
		t.Fatalf("merged report was not readable: %v", err)
	}
}

func TestTaskMarkMergedDoesNotMutateWhenRemoteReceiptIsUnavailable(t *testing.T) {
	s, revision, projectHead := testService(t)
	ctx := context.Background()
	task, created, err := s.TaskCreate(ctx, TaskCreateInput{ProjectID: "example", Title: "Merge receipt", Objective: "Require remote receipt.", Branch: "feature/merge-receipt", BaseRevision: projectHead, AcceptanceCriteria: []string{"remote"}, CreatedBy: "test", WriteOptions: WriteOptions{ExpectedHubRevision: revision}})
	if err != nil {
		t.Fatal(err)
	}
	reviewed := projectHead
	state := model.TaskState{SchemaVersion: model.SchemaVersion, TaskID: task.ID, TaskSHA256: task.SHA256, Status: "merge_ready", ReviewedHead: reviewed, UpdatedAt: time.Now().UTC()}
	revision = installTaskLifecycleState(t, s, task, state, created.Hub.After)
	project := s.Config.Projects[task.ProjectID]
	if err := s.Git.Refresh(ctx, project); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, project.Mirror, "update-ref", "refs/heads/"+task.Branch, reviewed)
	testutil.Git(t, project.Mirror, "update-ref", "refs/heads/develop", reviewed)
	if _, err := s.TaskMarkMerged(ctx, TaskMarkMergedInput{TaskID: task.ID, IntegrationHead: reviewed, WriteOptions: WriteOptions{ExpectedHubRevision: revision}}); err == nil {
		t.Fatal("accepted merged receipt without exact remote branches")
	}
	current, err := s.Hub.RemoteRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if current != revision {
		t.Fatalf("rejected merged receipt changed hub revision: got %s want %s", current, revision)
	}
}
