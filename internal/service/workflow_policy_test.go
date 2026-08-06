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
	for _, authorization := range []string{"", "agent", "arbitrary"} {
		_, _, authErr := s.ProjectWorkflowPolicyUpdate(ctx, ProjectWorkflowPolicyInput{Policy: policy, AuthorizationContext: authorization, WriteOptions: WriteOptions{ExpectedHubRevision: beforeAuthorizationCheck}})
		if authErr == nil || !strings.Contains(authErr.Error(), "authorization_context") {
			t.Fatalf("unauthorized policy write %q was accepted: %v", authorization, authErr)
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
	_, updated, err := s.ProjectWorkflowPolicyUpdate(ctx, ProjectWorkflowPolicyInput{Policy: policy, AuthorizationContext: WorkflowPolicyAuthorizationPlanner, WriteOptions: WriteOptions{ExpectedHubRevision: created.Hub.After}})
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
