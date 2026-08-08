package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func assertWorkflowPolicyStatusCI(t *testing.T, status ProjectWorkflowPolicyStatus) {
	t.Helper()
	if status.CI.Task != model.WorkflowCIModeDisabled || status.CI.TaskMerge != model.WorkflowCIModeDisabled || status.CI.Release != model.WorkflowCIModeDisabled {
		t.Fatalf("workflow policy status exposed invalid CI modes: %#v", status)
	}
	encoded, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	ci := wire["ci"].(map[string]any)
	for _, field := range []string{"task", "task_merge", "release"} {
		if ci[field] != string(model.WorkflowCIModeDisabled) {
			t.Fatalf("serialized %s CI mode=%v: %s", field, ci[field], encoded)
		}
	}
}

func TestProjectStatusWorkflowPolicyStateMatrixUsesDeterministicCIProjection(t *testing.T) {
	ctx := context.Background()
	for _, state := range []string{"missing", "invalid"} {
		t.Run(state, func(t *testing.T) {
			s, revision, _ := testServiceWithoutIdentifiers(t)
			path := s.workflowPolicyPath("example")
			var txRevision string
			if state == "missing" {
				result, err := s.Hub.Transact(ctx, revision, "test: remove workflow policy", func(worktree string) ([]string, error) {
					if err := os.Remove(filepath.Join(worktree, filepath.FromSlash(path))); err != nil {
						return nil, err
					}
					return []string{path}, nil
				})
				if err != nil {
					t.Fatal(err)
				}
				txRevision = result.After
			} else {
				result, err := s.Hub.Transact(ctx, revision, "test: corrupt workflow policy", func(worktree string) ([]string, error) {
					if err := hub.WriteText(worktree, path, `{"schema_version":1,"project_id":"example"}`); err != nil {
						return nil, err
					}
					return []string{path}, nil
				})
				if err != nil {
					t.Fatal(err)
				}
				txRevision = result.After
			}
			status, err := s.ProjectStatus(ctx, "example")
			if err != nil {
				t.Fatal(err)
			}
			if status.HubRevision != txRevision || status.WorkflowPolicy.State != state {
				t.Fatalf("unexpected %s status: %#v", state, status)
			}
			assertWorkflowPolicyStatusCI(t, status.WorkflowPolicy)
		})
	}
}

func trustedWorkflowPolicyContext(ctx context.Context, role string) context.Context {
	switch role {
	case "planner":
		return authority.WithPlanner(ctx)
	case "delivery":
		return authority.WithDelivery(ctx)
	default:
		return ctx
	}
}

func TestWorkflowPolicyRevisionAndTaskProjection(t *testing.T) {
	s, revision, _ := testService(t)
	ctx := context.Background()
	policy, err := s.ProjectWorkflowPolicyRead(ctx, "example")
	if err != nil {
		t.Fatal(err)
	}
	if policy.WorkflowStage != model.WorkflowStageTransitionalMain || policy.IntegrationBranch != "main" || policy.CI.Task != model.WorkflowCIModeDisabled {
		t.Fatalf("unexpected initial workflow policy: %#v", policy)
	}
	beforeAuthorizationCheck, err := s.Hub.RemoteRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, unauthorized := range []context.Context{ctx, trustedWorkflowPolicyContext(ctx, "agent")} {
		_, _, authErr := s.ProjectWorkflowPolicyUpdate(unauthorized, ProjectWorkflowPolicyInput{Policy: policy, WriteOptions: WriteOptions{ExpectedHubRevision: beforeAuthorizationCheck}})
		if authErr == nil || authErr.Error() != "AUTHORITY_UNAVAILABLE" {
			t.Fatalf("unauthorized policy write was accepted: %v", authErr)
		}
	}
	afterAuthorizationCheck, err := s.Hub.RemoteRevision(ctx)
	if err != nil || afterAuthorizationCheck != beforeAuthorizationCheck {
		t.Fatalf("unauthorized policy write changed Hub revision: before=%s after=%s err=%v", beforeAuthorizationCheck, afterAuthorizationCheck, err)
	}
	task, created, err := s.TaskCreate(ctx, TaskCreateInput{
		ProjectID: "example", Slug: "policy-projection", Title: "Policy projection", Objective: "Verify durable policy projection.",
		AcceptanceCriteria: []string{"projection"}, RequiredGates: []string{"go test ./..."}, OperationClass: "correction", CreatedBy: "test",
		WriteOptions: WriteOptions{ExpectedHubRevision: revision},
	})
	if err != nil {
		t.Fatal(err)
	}
	if task.WorkflowPolicyRevision != policy.Revision || task.OperationClass != "correction" || task.EffectiveCIField != "task" || task.EffectiveCIMode != model.WorkflowCIModeDisabled || task.WaitForCI || task.CIBlocking || task.AgentMayWait {
		t.Fatalf("unexpected task policy projection: %#v", task)
	}
	record, err := s.TaskReadRecord(ctx, task.ID)
	if err != nil || record.WorkflowPolicy == nil || record.WorkflowPolicy.Revision != policy.Revision {
		t.Fatalf("task record did not expose policy: record=%#v err=%v", record, err)
	}
	if _, _, err := s.TaskCreate(ctx, TaskCreateInput{
		ProjectID: "example", Slug: "hosted-gate", Title: "Hosted gate", Objective: "Reject an unauthorized hosted wait.",
		AcceptanceCriteria: []string{"rejected"}, RequiredGates: []string{"check-github-ci.py --wait"}, OperationClass: "implementation", CreatedBy: "test",
		WriteOptions: WriteOptions{ExpectedHubRevision: created.Hub.After},
	}); err == nil || !strings.Contains(err.Error(), "hosted-CI") {
		t.Fatalf("hosted CI wait gate was not rejected: %v", err)
	}
	current, err := s.Hub.RemoteRevision(ctx)
	if err != nil || current != created.Hub.After {
		t.Fatalf("rejected task changed Hub revision: got=%s err=%v want=%s", current, err, created.Hub.After)
	}

	policy.Revision = 2
	policy.WorkflowStage = model.WorkflowStageDevelopActive
	policy.IntegrationBranch = "develop"
	policy.UpdatedBy = "owner"
	policy.UpdatedAt = time.Now().UTC()
	_, updated, err := s.ProjectWorkflowPolicyUpdate(trustedWorkflowPolicyContext(ctx, "planner"), ProjectWorkflowPolicyInput{Policy: policy, WriteOptions: WriteOptions{ExpectedHubRevision: created.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	readBack, err := s.ProjectWorkflowPolicyRead(ctx, "example")
	if err != nil || readBack.Revision != 2 || readBack.IntegrationBranch != "develop" {
		t.Fatalf("policy update was not durable: policy=%#v err=%v", readBack, err)
	}
	if updated.Status != "updated" {
		t.Fatalf("unexpected policy update status: %#v", updated)
	}
}

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
		ProjectID: "example", Slug: "missing-policy", Title: "Missing policy", Objective: "Reject missing workflow policy.",
		AcceptanceCriteria: []string{"rejected"}, OperationClass: "implementation", CreatedBy: "test",
		WriteOptions: WriteOptions{ExpectedHubRevision: removed.After},
	})
	if err == nil || !strings.Contains(err.Error(), "project workflow policy is required") {
		t.Fatalf("missing policy was not enforced: %v", err)
	}
}

func TestProjectStatusUsesPersistedActiveTaskPolicyAcrossRevisionDrift(t *testing.T) {
	s, revision, _ := testService(t)
	ctx := context.Background()
	task, created, err := s.TaskCreate(ctx, TaskCreateInput{
		ProjectID: "example", Slug: "policy-drift", Title: "Policy drift", Objective: "Preserve the task policy snapshot.",
		AcceptanceCriteria: []string{"status"}, OperationClass: "implementation", CreatedBy: "test",
		WriteOptions: WriteOptions{ExpectedHubRevision: revision},
	})
	if err != nil {
		t.Fatal(err)
	}
	title, summary, objective := "Policy drift", "Policy drift", "Policy drift"
	plan, err := s.PlanUpdate(ctx, PlanUpdateInput{ProjectID: task.ProjectID, Title: &title, Summary: &summary, CurrentObjective: &objective, ActiveTaskID: &task.ID, UpdatedBy: "test", WriteOptions: WriteOptions{ExpectedHubRevision: created.Hub.After}})
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
	if _, _, err := s.ProjectWorkflowPolicyUpdate(trustedWorkflowPolicyContext(ctx, "planner"), ProjectWorkflowPolicyInput{Policy: current, WriteOptions: WriteOptions{ExpectedHubRevision: plan.Hub.After}}); err != nil {
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
	_, _, err = s.ProjectWorkflowPolicyUpdate(trustedWorkflowPolicyContext(ctx, "planner"), ProjectWorkflowPolicyInput{Policy: policy, WriteOptions: WriteOptions{ExpectedHubRevision: before}})
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
		ProjectID: "example", Slug: "activation", Title: "Activation task", Objective: "Verify explicit activation policy.",
		AcceptanceCriteria: []string{"activation"}, OperationClass: "activation", CreatedBy: "test",
		WriteOptions: WriteOptions{ExpectedHubRevision: revision},
	})
	if err != nil {
		t.Fatal(err)
	}
	if task.EffectiveCIField != "activation" || task.EffectiveCIMode != model.WorkflowCIModeDisabled || task.WaitForCI || task.CIBlocking || task.AgentMayWait {
		t.Fatalf("activation task inherited task-merge policy: %#v", task)
	}
}
