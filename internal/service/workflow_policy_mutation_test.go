package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestTaskCreateRequiresDurableWorkflowPolicy(t *testing.T) {
	s, revision, _ := testService(t)
	ctx := context.Background()
	policyPath := s.workflowPolicyPath("example")
	removed, err := s.Hub.Transact(ctx, revision, "test: remove workflow policy", func(worktree string) ([]string, error) {
		if err := os.Remove(filepath.Join(worktree, filepath.FromSlash(policyPath))); err != nil {
			return nil, err
		}
		return []string{policyPath}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = s.TaskCreate(ctx, TaskCreateInput{
		ProjectID:          "example",
		Slug:               "missing-policy",
		Title:              "Missing policy",
		Objective:          "Reject missing workflow policy.",
		AcceptanceCriteria: []string{"rejected"},
		OperationClass:     "implementation",
		CreatedBy:          "test",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: removed.After,
		},
	})
	if err == nil || !strings.Contains(err.Error(), "project workflow policy is required") {
		t.Fatalf("missing policy was not enforced: %v", err)
	}
}

func TestProjectStatusUsesPersistedActiveTaskPolicyAcrossRevisionDrift(t *testing.T) {
	s, revision, _ := testService(t)
	ctx := context.Background()
	task, created, err := s.TaskCreate(ctx, TaskCreateInput{
		ProjectID:          "example",
		Slug:               "policy-drift",
		Title:              "Policy drift",
		Objective:          "Preserve the task policy snapshot.",
		AcceptanceCriteria: []string{"status"},
		OperationClass:     "implementation",
		CreatedBy:          "test",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: revision,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	title, summary, objective := "Policy drift", "Policy drift", "Policy drift"
	plan, err := s.PlanUpdate(ctx, PlanUpdateInput{
		ProjectID:        task.ProjectID,
		Title:            &title,
		Summary:          &summary,
		CurrentObjective: &objective,
		ActiveTaskID:     &task.ID,
		UpdatedBy:        "test",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: created.Hub.After,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	current, err := s.ProjectWorkflowPolicyRead(ctx, task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	current.Revision = 2
	current.Agent.WaitForCI = true
	current.CI.Task = model.WorkflowCIModeRequire
	current.UpdatedBy = "planner"
	current.UpdatedAt = time.Now().UTC()
	if _, _, err := s.ProjectWorkflowPolicyUpdate(trustedWorkflowPolicyContext(ctx, "planner"), ProjectWorkflowPolicyInput{
		Policy: current,
		WriteOptions: WriteOptions{
			ExpectedHubRevision: plan.Hub.After,
		},
	}); err != nil {
		t.Fatal(err)
	}
	status, err := s.ProjectStatus(ctx, task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if status.WorkflowPolicy.Revision != 2 || status.WorkflowPolicy.AgentWaitForCI != true || status.WorkflowPolicy.CI.Task != model.WorkflowCIModeRequire {
		t.Fatalf("current policy projection was not retained: %#v", status.WorkflowPolicy)
	}
	if status.WorkflowPolicy.ActiveOperationClass != task.OperationClass || status.WorkflowPolicy.ActiveCIMode != task.EffectiveCIMode || status.WorkflowPolicy.CIBlocking != task.CIBlocking {
		t.Fatalf("active task policy was recomputed instead of persisted: task=%#v status=%#v", task, status.WorkflowPolicy)
	}
}

func TestWorkflowPolicyMutationRejectsActiveRunWithoutHubMutation(t *testing.T) {
	s, _, run, _ := dispatchedRun(t, "policy-active-run")
	ctx := context.Background()
	policy, err := s.ProjectWorkflowPolicyRead(ctx, run.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.Hub.RemoteRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	policy.Revision++
	policy.UpdatedBy = "planner"
	policy.UpdatedAt = time.Now().UTC()
	_, _, err = s.ProjectWorkflowPolicyUpdate(trustedWorkflowPolicyContext(ctx, "planner"), ProjectWorkflowPolicyInput{
		Policy: policy,
		WriteOptions: WriteOptions{
			ExpectedHubRevision: before,
		},
	})
	if err == nil || !strings.Contains(err.Error(), "active run "+run.ID) {
		t.Fatalf("active run did not block policy mutation: %v", err)
	}
	after, err := s.Hub.RemoteRevision(ctx)
	if err != nil || after != before {
		t.Fatalf("active-run rejection changed Hub revision: before=%s after=%s err=%v", before, after, err)
	}
	unchanged, err := s.ProjectWorkflowPolicyRead(ctx, run.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Revision != 1 {
		t.Fatalf("active-run rejection changed policy: %#v", unchanged)
	}
}

func TestActivationTaskUsesExplicitNonHostedCIPolicy(t *testing.T) {
	s, revision, _ := testService(t)
	task, _, err := s.TaskCreate(context.Background(), TaskCreateInput{
		ProjectID:          "example",
		Slug:               "activation",
		Title:              "Activation task",
		Objective:          "Verify explicit activation policy.",
		AcceptanceCriteria: []string{"activation"},
		OperationClass:     "activation",
		CreatedBy:          "test",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: revision,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if task.EffectiveCIField != "activation" || task.EffectiveCIMode != model.WorkflowCIModeDisabled || task.WaitForCI || task.CIBlocking || task.AgentMayWait {
		t.Fatalf("activation task inherited task-merge policy: %#v", task)
	}
}
