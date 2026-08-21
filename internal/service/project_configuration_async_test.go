package service

import (
	"context"
	"encoding/json"
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
		Patch: ProjectConfigurationPatch{
			AgentRouting: &routing,
		},
		UpdatedBy: "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: revision,
		},
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

func TestProjectConfigurationCheckpointUpdateAdvancesRevisionAndUsesSchemaValidPaths(t *testing.T) {
	s, revision, _ := testServiceWithoutIdentifiers(t)
	ctx := trustedWorkflowPolicyContext(context.Background(), "planner")
	current, err := s.ProjectConfigurationRead(ctx, "example")
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := current.Checkpoint
	checkpoint.Adapter = "go"
	input := ProjectConfigurationUpdateInput{
		ProjectID:        "example",
		ExpectedRevision: current.Revision,
		Patch: ProjectConfigurationPatch{
			Checkpoint: &checkpoint,
		},
		UpdatedBy: "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: revision,
		},
	}
	receipt, err := s.ProjectConfigurationUpdateAsync(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Operation.Hub.Paths == nil {
		t.Fatal("initial project/update receipt has null operation.hub.paths")
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		receipt, err = s.ProjectConfigurationUpdateOperationStatus(ctx, receipt.OperationID)
		if err != nil {
			t.Fatal(err)
		}
		if receipt.Status == "completed" || receipt.Status == "failed" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if receipt.Status != "completed" {
		t.Fatalf("checkpoint project/update did not complete: %#v", receipt)
	}
	if receipt.Configuration == nil || receipt.Configuration.Revision != current.Revision+1 {
		t.Fatalf("checkpoint update did not advance revision: current=%d receipt=%#v", current.Revision, receipt)
	}
	if receipt.Configuration.Checkpoint.Adapter != "go" {
		t.Fatalf("checkpoint patch was not persisted: %#v", receipt.Configuration.Checkpoint)
	}
	if receipt.Operation.Hub.Paths == nil {
		t.Fatal("completed project/update receipt has null operation.hub.paths")
	}

	wire, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Operation struct {
			Hub struct {
				Paths []string `json:"paths"`
			} `json:"hub"`
		} `json:"operation"`
	}
	if err := json.Unmarshal(wire, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Operation.Hub.Paths == nil {
		t.Fatalf("project/update receipt did not encode paths as an array: %s", wire)
	}
}

func TestProjectConfigurationMutationReceiptPreservesErrorAndSchemaValidPaths(t *testing.T) {
	receipt := projectConfigurationMutationReceipt(durableMutationOperation{
		OperationID: "mutation-project-update",
		Status:      "failed",
		Error:       "hub unavailable",
		CreatedAt:   time.Unix(1, 0).UTC(),
		UpdatedAt:   time.Unix(2, 0).UTC(),
	})
	if receipt.Status != "failed" || receipt.Error != "hub unavailable" {
		t.Fatalf("receipt error/status was not preserved: %#v", receipt)
	}
	if receipt.Operation.Hub.Paths == nil {
		t.Fatal("failed project/update receipt has null operation.hub.paths")
	}
}
