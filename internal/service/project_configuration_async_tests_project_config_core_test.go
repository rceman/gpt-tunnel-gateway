package service

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

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
		ProjectID:        "example",
		ExpectedRevision: configuration.Revision,
		Patch: ProjectConfigurationPatch{
			AgentRouting: &routing,
		},
		UpdatedBy: "planner",
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
