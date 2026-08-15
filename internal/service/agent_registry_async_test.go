package service

import (
	"context"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestAgentRegistryMutationsUseBoundedDurableReceipts(t *testing.T) {
	s, revision, _ := testServiceWithoutIdentifiers(t)
	ctx := authority.WithPlanner(context.Background())
	now := time.Now().UTC()
	register := AgentRegisterInput{
		Agent: model.Agent{
			SchemaVersion:        model.AgentSchemaVersion,
			ProjectID:            "example",
			AgentID:              "async-coder",
			Role:                 model.AgentRoleCoding,
			Enabled:              true,
			RecommendedReasoning: model.ReasoningHigh,
			CreatedAt:            now,
			UpdatedAt:            now,
		},
		WriteOptions: WriteOptions{
			ExpectedHubRevision: revision,
		},
	}
	started := time.Now()
	first, err := s.AgentRegisterAsync(ctx, register)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("Agent register initiation exceeded one second: %s", elapsed)
	} else {
		t.Logf("Agent register initiation latency: %s", elapsed)
	}
	second, err := s.AgentRegisterAsync(ctx, register)
	if err != nil {
		t.Fatal(err)
	}
	if first.OperationID == "" || first.OperationID != second.OperationID {
		t.Fatalf("Agent register initiation was not idempotent: first=%#v second=%#v", first, second)
	}
	completed := waitAgentMutation(t, s, ctx, first.OperationID)
	if completed.Agent == nil || completed.Agent.AgentID != "async-coder" {
		t.Fatalf("Agent register did not return the durable Agent: %#v", completed)
	}

	updated := true
	updateReceipt, err := s.AgentUpdateAsync(ctx, AgentUpdateInput{
		ProjectID: "example",
		AgentID:   "async-coder",
		Enabled:   &updated,
		UpdatedBy: "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: completed.Operation.Hub.After,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	updatedResult := waitAgentMutation(t, s, ctx, updateReceipt.OperationID)
	if updatedResult.Agent == nil {
		t.Fatalf("Agent update did not return the durable Agent: %#v", updatedResult)
	}

	disabled := false
	disableReceipt, err := s.AgentDisableAsync(ctx, AgentDisableInput{
		ProjectID: "example",
		AgentID:   "async-coder",
		UpdatedBy: "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: updatedResult.Operation.Hub.After,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	disabledResult := waitAgentMutation(t, s, ctx, disableReceipt.OperationID)
	if disabledResult.Agent == nil || disabledResult.Agent.Enabled != disabled {
		t.Fatalf("Agent disable did not persist the disabled state: %#v", disabledResult)
	}
}

func waitAgentMutation(t *testing.T, s *Service, ctx context.Context, operationID string) AgentMutationReceipt {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var receipt AgentMutationReceipt
	for time.Now().Before(deadline) {
		var err error
		receipt, err = s.AgentMutationOperationStatus(ctx, operationID)
		if err != nil {
			t.Fatal(err)
		}
		if receipt.Status == "completed" || receipt.Status == "failed" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if receipt.Status != "completed" {
		t.Fatalf("Agent mutation did not complete: %#v", receipt)
	}
	return receipt
}
