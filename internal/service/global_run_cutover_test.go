package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestGlobalRunCutoverRejectsLegacyDispatchWithoutHubMutation(t *testing.T) {
	s, revision, _ := testService(t)
	_, _, err := s.TaskDispatch(context.Background(), DispatchInput{
		TaskID: "EXM-TSK1",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: revision,
		},
	})
	if !errors.Is(err, errRunAuthorityRetired) {
		t.Fatalf("TaskDispatch error = %v, want retired authority", err)
	}
	current, err := s.hubRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if current != revision {
		t.Fatalf("legacy dispatch changed Hub revision from %s to %s", revision, current)
	}
}

func TestGlobalRunCutoverStateCheckHasNoTaskRunGraph(t *testing.T) {
	s, _, _ := testService(t)
	check, err := s.StateCheck(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !check.Valid || check.OperationalTaskRunGraph {
		t.Fatalf("unexpected post-cutover state graph: %#v", check)
	}
	if len(check.Issues) != 0 {
		t.Fatalf("post-cutover state issues = %#v", check.Issues)
	}
}

func TestLegacyActiveRunStatusCannotBeRetiredWithoutAttemptMapping(t *testing.T) {
	for _, status := range []string{"created", "dispatching", "dispatched", "awaiting_result", "running"} {
		got, err := migrationAttemptStatus(status, nil)
		if err != nil || got != "running" {
			t.Fatalf("migrationAttemptStatus(%q) = %q, %v; want running, nil", status, got, err)
		}
	}
	if got, err := migrationAttemptStatus("succeeded", nil); err == nil || got != "" {
		t.Fatal("terminal legacy status without finished_at was accepted")
	}
	finished := time.Now().UTC()
	if got, err := migrationAttemptStatus("succeeded", &finished); err != nil || got != model.TrainV2AttemptSucceeded {
		t.Fatalf("terminal legacy status = %q, %v", got, err)
	}
}

func TestActiveTrainAttemptPreservesConfigurationMutationSafety(t *testing.T) {
	s, revision, _ := testService(t)
	revision = enableTrainV2ForTest(t, s, revision)
	task, revision := readyTrainTaskForTest(t, s, revision, "active Attempt safety")
	train, operation, err := s.TrainV2Create(context.Background(), TrainV2CreateInput{
		ProjectID: "example",
		TaskIDs:   []string{task.ID},
		CreatedBy: "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: revision,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	seedTrainExecutionSession(t, s, train.ID)
	if _, err := s.TrainV2Start(context.Background(), TrainV2StartInput{
		ProjectID: "example",
		TrainID:   train.ID,
		StartedBy: "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: operation.Hub.After,
		},
	}); err != nil {
		t.Fatal(err)
	}
	policy, err := s.ProjectWorkflowPolicyRead(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	policy.Revision++
	policy.WorkflowStage = model.WorkflowStageDevelopActive
	policy.IntegrationBranch = "develop"
	policy.UpdatedAt = time.Now().UTC()
	before, err := s.hubRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ProjectWorkflowPolicyUpdate(trustedWorkflowPolicyContext(context.Background(), "planner"), ProjectWorkflowPolicyInput{
		Policy: policy,
		WriteOptions: WriteOptions{
			ExpectedHubRevision: before,
		},
	}); err == nil || !strings.Contains(err.Error(), "active Train Attempt") {
		t.Fatalf("active Attempt policy mutation was not rejected: %v", err)
	}
	after, err := s.hubRevision(context.Background())
	if err != nil || after != before {
		t.Fatalf("active Attempt rejection changed Hub: before=%s after=%s err=%v", before, after, err)
	}
}
