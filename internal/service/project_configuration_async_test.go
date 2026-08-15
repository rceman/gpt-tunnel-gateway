package service

import (
	"context"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestProjectConfigurationUpdateAsyncIsBoundedAndIdempotent(t *testing.T) {
	s, revision, _ := testServiceWithoutIdentifiers(t)
	ctx := trustedWorkflowPolicyContext(context.Background(), "planner")
	current, err := s.ProjectConfigurationRead(ctx, "example")
	if err != nil {
		t.Fatal(err)
	}
	routing := current.AgentRouting
	routing.SingletonRecommendedReasoning = model.ReasoningMedium
	in := ProjectConfigurationUpdateInput{
		ProjectID:        "example",
		ExpectedRevision: current.Revision,
		Patch:            ProjectConfigurationPatch{AgentRouting: &routing},
		UpdatedBy:        "planner",
		WriteOptions:     WriteOptions{ExpectedHubRevision: revision},
	}
	started := time.Now()
	first, err := s.ProjectConfigurationUpdateAsync(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("project/update initiation exceeded one second: %s", elapsed)
	} else {
		t.Logf("project/update initiation latency: %s", elapsed)
	}
	second, err := s.ProjectConfigurationUpdateAsync(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if first.OperationID == "" || first.OperationID != second.OperationID {
		t.Fatalf("project/update initiation was not idempotent: first=%#v second=%#v", first, second)
	}

	deadline := time.Now().Add(10 * time.Second)
	var completed ProjectConfigurationMutationReceipt
	for time.Now().Before(deadline) {
		completed, err = s.ProjectConfigurationUpdateOperationStatus(ctx, first.OperationID)
		if err != nil {
			t.Fatal(err)
		}
		if completed.Status == "completed" || completed.Status == "failed" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if completed.Status != "completed" || completed.Configuration == nil || completed.Configuration.Revision != current.Revision+1 {
		t.Fatalf("project/update worker did not complete: %#v", completed)
	}
}
