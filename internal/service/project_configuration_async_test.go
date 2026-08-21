package service

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
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

func TestProjectConfigurationReadUsesSharedWhenHubUnavailable(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	ctx := trustedWorkflowPolicyContext(context.Background(), "planner")
	configuration, err := s.ProjectConfigurationRead(ctx, "example")
	if err != nil {
		t.Fatal(err)
	}
	db, err := sqlitestore.Open(s.Config.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s.Durability = db
	markSharedBootstrapCompleteForTest(t, db)
	payload, err := json.Marshal(configuration)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.PutSharedProjection(ctx, "project_configuration", sqlitestore.SharedEntity{
		ID: configuration.ProjectID, Revision: int64(configuration.Revision), Payload: payload, UpdatedAt: configuration.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
	s.Hub.Config.Hub.RepositoryURL = filepath.Join(t.TempDir(), "unavailable-hub.git")
	read, err := s.ProjectConfigurationRead(ctx, "example")
	if err != nil || read.Revision != configuration.Revision {
		t.Fatalf("Shared project configuration read failed without Hub: %#v %v", read, err)
	}
	policy, err := s.ProjectWorkflowPolicyRead(ctx, "example")
	if err != nil || policy.ProjectID != "example" || policy.Revision != configuration.Revision {
		t.Fatalf("Shared workflow policy read failed without Hub: %#v %v", policy, err)
	}
}

func TestProjectConfigurationUpdateUsesSharedCASAndOutbox(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	ctx := trustedWorkflowPolicyContext(context.Background(), "planner")
	configuration, err := s.ProjectConfigurationRead(ctx, "example")
	if err != nil {
		t.Fatal(err)
	}
	db, err := sqlitestore.Open(s.Config.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	s.Durability = db
	markSharedBootstrapCompleteForTest(t, db)
	payload, err := json.Marshal(configuration)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.PutSharedProjection(ctx, "project_configuration", sqlitestore.SharedEntity{
		ID: configuration.ProjectID, Revision: int64(configuration.Revision), Payload: payload, UpdatedAt: configuration.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
	s.Hub.Config.Hub.RepositoryURL = filepath.Join(t.TempDir(), "unavailable-hub.git")
	routing := configuration.AgentRouting
	routing.SingletonRecommendedReasoning = model.ReasoningMedium
	updated, operation, err := s.ProjectConfigurationUpdate(ctx, ProjectConfigurationUpdateInput{
		ProjectID: "example", ExpectedRevision: configuration.Revision,
		Patch: ProjectConfigurationPatch{AgentRouting: &routing}, UpdatedBy: "planner",
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != configuration.Revision+1 || operation.OperationID == "" {
		t.Fatalf("unexpected Shared project update: updated=%#v operation=%#v", updated, operation)
	}
	entries, err := db.PendingOutbox(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].EntityType != "project_configuration" || entries[0].EntityID != "example" {
		t.Fatalf("project configuration outbox=%#v", entries)
	}
	if _, _, err := s.ProjectConfigurationUpdate(ctx, ProjectConfigurationUpdateInput{
		ProjectID: "example", ExpectedRevision: configuration.Revision,
		Patch: ProjectConfigurationPatch{AgentRouting: &routing}, UpdatedBy: "other",
	}); err == nil {
		t.Fatal("stale project configuration update unexpectedly passed Shared CAS")
	}
}

func TestProjectConfigurationUpdateUsesSharedActiveTrainGuard(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	ctx := trustedWorkflowPolicyContext(context.Background(), "planner")
	configuration, err := s.ProjectConfigurationRead(ctx, "example")
	if err != nil {
		t.Fatal(err)
	}
	db, err := sqlitestore.Open(s.Config.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s.Durability = db
	markSharedBootstrapCompleteForTest(t, db)
	payload, err := json.Marshal(configuration)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.PutSharedProjection(ctx, "project_configuration", sqlitestore.SharedEntity{
		ID: configuration.ProjectID, Revision: int64(configuration.Revision), Payload: payload, UpdatedAt: configuration.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
	active := staleTrainV2ForRetirementTest(time.Now().UTC())
	active.Status = model.TrainV2Running
	active.Items[0].Status = model.TrainV2ItemRunning
	active.Items[0].Attempts[0].Status = model.TrainV2AttemptRunning
	active.Items[0].Attempts[0].FinishedAt = nil
	trainPayload, err := json.Marshal(active)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.PutSharedProjection(ctx, "train", sqlitestore.SharedEntity{
		ID: active.ID, Revision: int64(active.Revision), Payload: trainPayload, UpdatedAt: active.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
	workflow := configuration.Workflow
	if _, _, err := s.ProjectConfigurationUpdate(ctx, ProjectConfigurationUpdateInput{
		ProjectID: "example", ExpectedRevision: configuration.Revision,
		Patch: ProjectConfigurationPatch{Workflow: &workflow}, UpdatedBy: "planner",
	}); err == nil {
		t.Fatal("execution-sensitive project update passed with active Shared Train Attempt")
	}
}

func TestProjectConfigurationUpdateSameOperationRetryReusesCommittedResult(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	ctx := trustedWorkflowPolicyContext(context.Background(), "planner")
	configuration, err := s.ProjectConfigurationRead(ctx, "example")
	if err != nil {
		t.Fatal(err)
	}
	db, err := sqlitestore.Open(s.Config.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	s.Durability = db
	markSharedBootstrapCompleteForTest(t, db)
	payload, err := json.Marshal(configuration)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.PutSharedProjection(ctx, "project_configuration", sqlitestore.SharedEntity{
		ID: configuration.ProjectID, Revision: int64(configuration.Revision), Payload: payload, UpdatedAt: configuration.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
	operationCtx := withDurableMutationOperationID(ctx, "mutation-project-configuration-retry")
	routing := configuration.AgentRouting
	routing.SingletonRecommendedReasoning = model.ReasoningMedium
	input := ProjectConfigurationUpdateInput{
		ProjectID: "example", ExpectedRevision: configuration.Revision,
		Patch: ProjectConfigurationPatch{AgentRouting: &routing}, UpdatedBy: "planner",
	}
	first, firstOperation, err := s.ProjectConfigurationUpdate(operationCtx, input)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = sqlitestore.Open(s.Config.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	restarted := New(s.Config)
	restarted.Durability = db
	second, secondOperation, err := restarted.ProjectConfigurationUpdate(operationCtx, input)
	if err != nil {
		t.Fatal(err)
	}
	if first.Revision != configuration.Revision+1 || second.Revision != first.Revision || !first.UpdatedAt.Equal(second.UpdatedAt) {
		t.Fatalf("same operation retry rebuilt result: first=%#v second=%#v", first, second)
	}
	if firstOperation.OperationID != secondOperation.OperationID || !reflect.DeepEqual(firstOperation.Hub, secondOperation.Hub) {
		t.Fatalf("same operation retry changed receipt: first=%#v second=%#v", firstOperation, secondOperation)
	}
	entries, err := db.PendingOutbox(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Revision != int64(first.Revision) {
		t.Fatalf("same operation retry duplicated or changed outbox: %#v", entries)
	}
}
