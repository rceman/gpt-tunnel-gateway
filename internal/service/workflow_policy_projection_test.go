package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

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
		_, _, authErr := s.ProjectWorkflowPolicyUpdate(unauthorized, ProjectWorkflowPolicyInput{
			Policy: policy,
			WriteOptions: WriteOptions{
				ExpectedHubRevision: beforeAuthorizationCheck,
			},
		})
		if authErr == nil || authErr.Error() != "AUTHORITY_UNAVAILABLE" {
			t.Fatalf("unauthorized policy write was accepted: %v", authErr)
		}
	}
	afterAuthorizationCheck, err := s.Hub.RemoteRevision(ctx)
	if err != nil || afterAuthorizationCheck != beforeAuthorizationCheck {
		t.Fatalf("unauthorized policy write changed Hub revision: before=%s after=%s err=%v", beforeAuthorizationCheck, afterAuthorizationCheck, err)
	}
	task, created, err := s.TaskCreate(ctx, TaskCreateInput{
		ProjectID:          "example",
		Slug:               "policy-projection",
		Title:              "Policy projection",
		Objective:          "Verify durable policy projection.",
		AcceptanceCriteria: []string{"projection"},
		RequiredGates:      []string{"go test ./..."},
		OperationClass:     "correction",
		CreatedBy:          "test",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: revision,
		},
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
		ProjectID:          "example",
		Slug:               "hosted-gate",
		Title:              "Hosted gate",
		Objective:          "Reject an unauthorized hosted wait.",
		AcceptanceCriteria: []string{"rejected"},
		RequiredGates:      []string{"check-github-ci.py --wait"},
		OperationClass:     "implementation",
		CreatedBy:          "test",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: created.Hub.After,
		},
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
	_, updated, err := s.ProjectWorkflowPolicyUpdate(trustedWorkflowPolicyContext(ctx, "planner"), ProjectWorkflowPolicyInput{
		Policy: policy,
		WriteOptions: WriteOptions{
			ExpectedHubRevision: created.Hub.After,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.BootstrapSharedFromHub(ctx); err != nil {
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

func TestWorkflowPolicyReadDoesNotFallBackToLegacyHub(t *testing.T) {
	s, revision, _ := testService(t)
	ctx := context.Background()
	legacy := model.ProjectWorkflowPolicy{
		SchemaVersion:     model.SchemaVersion,
		ProjectID:         "orphan",
		Revision:          1,
		WorkflowStage:     model.WorkflowStageTransitionalMain,
		IntegrationBranch: "main",
		Agent:             model.WorkflowPolicyAgent{WaitForCI: false},
		CI:                model.WorkflowPolicyCI{Task: model.WorkflowCIModeDisabled, TaskMerge: model.WorkflowCIModeObserve, Release: model.WorkflowCIModeObserve},
		UpdatedBy:         "test",
		UpdatedAt:         time.Now().UTC(),
	}
	if _, err := s.Hub.Transact(ctx, revision, "test: seed legacy-only policy", func(worktree string) ([]string, error) {
		path := s.workflowPolicyPath("orphan")
		if err := hub.WriteJSON(worktree, path, legacy); err != nil {
			return nil, err
		}
		return []string{path}, nil
	}); err != nil {
		t.Fatal(err)
	}
	legacyReads := 0
	s.legacyWorkflowPolicyRead = func(context.Context, string, *model.ProjectWorkflowPolicy) error {
		legacyReads++
		return nil
	}
	if _, err := s.ProjectWorkflowPolicyRead(ctx, "orphan"); err == nil {
		t.Fatal("workflow policy read accepted legacy Hub fallback without Shared configuration")
	}
	if legacyReads != 0 {
		t.Fatalf("workflow policy read invoked legacy fallback %d time(s)", legacyReads)
	}
}
