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
	if status.CI.Task != model.WorkflowCIModeDisabled || status.CI.TaskMerge != model.WorkflowCIModeObserve || status.CI.Release != model.WorkflowCIModeObserve {
		t.Fatalf("workflow policy status lost its canonical Hub projection: %#v", status)
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
	expected := map[string]string{
		"task":       string(model.WorkflowCIModeDisabled),
		"task_merge": string(model.WorkflowCIModeObserve),
		"release":    string(model.WorkflowCIModeObserve),
	}
	for field, mode := range expected {
		if ci[field] != mode {
			t.Fatalf("serialized %s CI mode=%v, want %s: %s", field, ci[field], mode, encoded)
		}
	}
}

func TestProjectStatusWorkflowPolicyStateMatrixUsesDeterministicCIProjection(t *testing.T) {
	ctx := context.Background()
	for _, state := range []string{"missing", "invalid"} {
		t.Run(state, func(t *testing.T) {
			s, revision, _ := testServiceWithoutIdentifiers(t)
			path := s.projectConfigurationPath("example")
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
			if status.HubRevision != txRevision || status.ProjectConfiguration.State != state || status.WorkflowPolicy.State != "adopted" {
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
	case "agent":
		return authority.WithAgent(ctx)
	default:
		return ctx
	}
}

func TestWorkflowPolicyReadDoesNotUseRetiredHubAuthority(t *testing.T) {
	s, _ := tsk384ConfiguredFixture(t)
	ctx := context.Background()
	policy, err := s.ProjectWorkflowPolicyRead(ctx, "example")
	if err != nil {
		t.Fatal(err)
	}
	divergent := policy
	if divergent.CI.Release == model.WorkflowCIModeDisabled {
		divergent.CI.Release = model.WorkflowCIModeObserve
	} else {
		divergent.CI.Release = model.WorkflowCIModeDisabled
	}
	revision, err := s.Hub.RemoteRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	legacyPath := s.workflowPolicyPath("example")
	if _, err := s.Hub.Transact(ctx, revision, "test: write a retired workflow policy projection", func(worktree string) ([]string, error) {
		if err := hub.WriteJSON(worktree, legacyPath, divergent); err != nil {
			return nil, err
		}
		return []string{legacyPath}, nil
	}); err != nil {
		t.Fatal(err)
	}
	current, err := s.ProjectWorkflowPolicyRead(ctx, "example")
	if err != nil || !workflowPoliciesEquivalent(current, policy) {
		t.Fatalf("retired Hub workflow policy altered the Shared Rule projection: current=%#v err=%v", current, err)
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
	if _, _, err := s.ProjectWorkflowPolicyUpdate(ctx, ProjectWorkflowPolicyInput{
		Policy: policy,
		WriteOptions: WriteOptions{
			ExpectedHubRevision: revision,
		},
	}); err != nil && err.Error() == "AUTHORITY_UNAVAILABLE" {
		t.Fatalf("permissive workflow-policy action retained a role gate: %v", err)
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
	readBack, err := s.ProjectWorkflowPolicyRead(ctx, "example")
	if err != nil || readBack.Revision != 2 || readBack.IntegrationBranch != "develop" {
		t.Fatalf("policy update was not durable: policy=%#v err=%v", readBack, err)
	}
	if updated.Status != "updated" {
		t.Fatalf("unexpected policy update status: %#v", updated)
	}
}
