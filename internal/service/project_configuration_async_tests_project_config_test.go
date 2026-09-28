package service

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestProjectConfigurationReadUsesSharedWhenHubUnavailable(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	configuration, err := s.ProjectConfigurationRead(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	testServiceWithDurability(t, s)
	s.Hub.Config.Hub.RepositoryURL = filepath.Join(t.TempDir(), "unavailable-hub.git")
	read, err := s.ProjectConfigurationRead(context.Background(), "example")
	if err != nil || read.Revision != configuration.Revision {
		t.Fatalf("Shared project configuration read failed without Hub: %#v %v", read, err)
	}
}

func TestProjectConfigurationMutationUsesSharedCASAndOutbox(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	db := testServiceWithDurability(t, s)
	defer db.Close()
	ctx := WithAgentSessionID(trustedWorkflowPolicyContext(context.Background(), "planner"), "planner-test")
	first, err := s.ConfigProcedureCreate(ctx, ConfigProcedureCreateInput{
		ProjectID:  "example",
		Name:       "notify_work_finished",
		Definition: workFinishedProcedureDefinition(),
		Reason:     "Add the project work-finished action.",
	})
	if err != nil || first.Revision != 2 {
		t.Fatalf("Procedure create=%#v err=%v", first, err)
	}
	entries, err := db.PendingOutbox(ctx, 20)
	if err != nil {
		t.Fatal(err)
	}
	configEntries := 0
	for _, entry := range entries {
		if entry.EntityType == "project_configuration" {
			configEntries++
			if entry.EntityID != "example" || entry.Revision != int64(first.Revision) {
				t.Fatalf("project configuration outbox=%#v", entries)
			}
		}
	}
	if configEntries != 1 {
		t.Fatalf("project configuration outbox=%#v", entries)
	}
	second, err := s.ConfigProcedureUpdate(ctx, ConfigProcedureUpdateInput{
		ProjectID:  "example",
		Name:       "notify_work_finished",
		Definition: workFinishedProcedureDefinition(),
		Reason:     "Refresh the project work-finished action.",
	})
	if err != nil || second.Revision != 3 {
		t.Fatalf("Procedure update=%#v err=%v", second, err)
	}
}

func TestProjectConfigurationMutationRequiresPlannerAndReason(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	ctx := WithAgentSessionID(context.Background(), "worker-session")
	input := ConfigProcedureCreateInput{
		ProjectID:  "example",
		Name:       "notify_work_finished",
		Definition: workFinishedProcedureDefinition(),
		Reason:     "Add the project work-finished action.",
	}
	if _, err := s.ConfigProcedureCreate(ctx, input); err == nil {
		t.Fatal("non-Planner Procedure mutation succeeded")
	}
	ctx = WithAgentSessionID(trustedWorkflowPolicyContext(context.Background(), "planner"), "planner-session")
	input.Reason = ""
	if _, err := s.ConfigProcedureCreate(ctx, input); err == nil {
		t.Fatal("Procedure mutation without a reason succeeded")
	}
}

func TestProjectConfigurationSharedPayloadRemainsCanonicalJSON(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	db := testServiceWithDurability(t, s)
	defer db.Close()
	entities, err := db.ListSharedEntities(context.Background(), "project_configuration", 20)
	if err != nil || len(entities) != 1 {
		t.Fatalf("configuration projection=%#v err=%v", entities, err)
	}
	var configuration struct {
		SchemaVersion int               `json:"schema_version"`
		Hooks         map[string]string `json:"hooks"`
		Procedures    map[string]any    `json:"procedures"`
	}
	if err := json.Unmarshal(entities[0].Payload, &configuration); err != nil || configuration.SchemaVersion != 3 || configuration.Hooks == nil || configuration.Procedures == nil {
		t.Fatalf("noncanonical v3 payload: %#v err=%v", configuration, err)
	}
	if entities[0].UpdatedAt == "" {
		t.Fatal("project configuration projection omitted UpdatedAt")
	}
}
