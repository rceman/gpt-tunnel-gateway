package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestTaskMarkMergeReadyUsesLatestSuccessfulReportAndDeferReusesHead(t *testing.T) {
	s, revision, _ := testService(t)
	ctx := context.Background()
	task, created, err := s.TaskCreate(ctx, TaskCreateInput{
		ProjectID:          "example",
		Title:              "Lifecycle report",
		Objective:          "Use a canonical successful report.",
		Slug:               "lifecycle-report",
		AcceptanceCriteria: []string{"report"},
		RequiredGates:      []string{"gate"},
		OperationClass:     "implementation",
		CreatedBy:          "test",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: revision,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := s.PlanUpdate(ctx, PlanUpdateInput{
		ProjectID:        "example",
		Title:            planString("Lifecycle"),
		Summary:          planString("Lifecycle"),
		CurrentObjective: planString("Lifecycle"),
		ActiveTaskID:     planString(task.ID),
		UpdatedBy:        "test",
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
	_, finalized, err := s.RunFinalize(ctx, FinalizeInput{
		RunID: run.ID,
		WriteOptions: WriteOptions{
			ExpectedHubRevision: dispatch.Hub.After,
		},
	})
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
	installTaskLifecycleState(t, s, task, completedState, invalidRevision)
	delivery := finalizeAcceptedDeliveryReview(t, s, task, run)
	ready, err := s.TaskMarkMergeReady(ctx, TaskMarkMergeReadyInput{
		TaskID: task.ID,
		WriteOptions: WriteOptions{
			ExpectedHubRevision: delivery.HubCommit,
		},
	})
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
	policy, err := s.ProjectWorkflowPolicyRead(ctx, task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	policy.Revision = 2
	policy.WorkflowStage = model.WorkflowStageDevelopActive
	policy.IntegrationBranch = "develop"
	policy.UpdatedAt = time.Now().UTC()
	_, policyUpdate, err := s.ProjectWorkflowPolicyUpdate(trustedWorkflowPolicyContext(ctx, "delivery"), ProjectWorkflowPolicyInput{
		Policy: policy,
		WriteOptions: WriteOptions{
			ExpectedHubRevision: ready.Hub.After,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, project.Root, "branch", "develop")
	testutil.Git(t, project.Root, "push", "-u", "origin", "develop")
	testutil.Git(t, project.Mirror, "config", "--unset-all", "remote.origin.fetch")
	testutil.Git(t, project.Mirror, "config", "--add", "remote.origin.fetch", "+refs/heads/*:refs/remotes/origin/*")
	testutil.Git(t, project.Mirror, "config", "--unset-all", "remote.origin.mirror")
	testutil.Git(t, project.Mirror, "fetch", "origin")
	deferred, err := s.TaskDefer(ctx, TaskDeferInput{
		TaskID: task.ID,
		Reason: "later integration",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: policyUpdate.Hub.After,
		},
	})
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
	merged, err := s.TaskMarkMerged(ctx, TaskMarkMergedInput{
		TaskID:          task.ID,
		IntegrationHead: record.State.ReviewedHead,
		WriteOptions: WriteOptions{
			ExpectedHubRevision: reset,
		},
	})
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
