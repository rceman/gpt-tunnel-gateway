package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

func TestSharedOutboxRetryDelayIsBounded(t *testing.T) {
	if got := sharedOutboxRetryDelay(1); got != time.Second {
		t.Fatalf("first retry delay=%s", got)
	}
	if got := sharedOutboxRetryDelay(5); got != 16*time.Second {
		t.Fatalf("fifth retry delay=%s", got)
	}
	if got := sharedOutboxRetryDelay(50); got != 16*time.Second {
		t.Fatalf("retry delay was unbounded=%s", got)
	}
}

func TestSharedOutboxEqualTaskADRAndConfigurationAreTerminalNoOps(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiersSetup(t)
	ctx := context.Background()
	now := time.Now().UTC()

	task, err := model.NewTask("example", "EXM-TSK565", model.AuthoringDraft{
		Title: "No-op task", Summary: "Prove equal publication is terminal.", Objective: "Prove equal publication is terminal.", ADRRelation: model.TaskADRNoRequired,
	}, "planner", now)
	if err != nil {
		t.Fatal(err)
	}
	taskPayload, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	taskEntry := sqlitestore.OutboxEntry{ID: "task-noop", EntityType: "task", EntityID: task.ID, Revision: int64(task.Revision), Payload: taskPayload}
	if err := s.publishSharedTaskOutbox(ctx, taskEntry); err != nil {
		t.Fatal(err)
	}
	if err := s.publishSharedTaskOutbox(ctx, taskEntry); !errors.Is(err, errSharedOutboxNoop) {
		t.Fatalf("equal Task publication error=%v, want terminal no-op", err)
	}

	adr := model.ADR{SchemaVersion: model.SchemaVersion, ID: "EXM-ADR565", ProjectID: "example", Revision: 1, RevisionCount: 1, Title: "No-op ADR", Summary: "Bounded no-op ADR summary", Status: model.ADRStatusAccepted, Context: "context", Decision: "decision", Consequences: "consequences", CreatedAt: now, UpdatedAt: now}
	adrPayload, err := json.Marshal(adr)
	if err != nil {
		t.Fatal(err)
	}
	adrEntry := sqlitestore.OutboxEntry{ID: "adr-noop", EntityType: "adr", EntityID: adr.ID, Revision: 1, Payload: adrPayload}
	if err := s.publishSharedADROutbox(ctx, adrEntry); err != nil {
		t.Fatal(err)
	}
	if err := s.publishSharedADROutbox(ctx, adrEntry); !errors.Is(err, errSharedOutboxNoop) {
		t.Fatalf("equal ADR publication error=%v, want terminal no-op", err)
	}

	configuration, err := s.ProjectConfigurationRead(ctx, "example")
	if err != nil {
		t.Fatal(err)
	}
	configurationPayload, err := json.Marshal(configuration)
	if err != nil {
		t.Fatal(err)
	}
	configurationEntry := sqlitestore.OutboxEntry{ID: "configuration-noop", EntityType: "project_configuration", EntityID: configuration.ProjectID, Revision: int64(configuration.Revision), Payload: configurationPayload}
	if err := s.publishSharedProjectConfigurationOutbox(ctx, configurationEntry); err != nil && !errors.Is(err, errSharedOutboxNoop) {
		t.Fatal(err)
	}
	if err := s.publishSharedProjectConfigurationOutbox(ctx, configurationEntry); !errors.Is(err, errSharedOutboxNoop) {
		t.Fatalf("equal project configuration publication error=%v, want terminal no-op", err)
	}
}

func TestSharedOutboxNewADRRevisionPublishesAndFailuresRemainRetryable(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiersSetup(t)
	ctx := context.Background()
	now := time.Now().UTC()
	adr := model.ADR{SchemaVersion: model.SchemaVersion, ID: "EXM-ADR566", ProjectID: "example", Revision: 1, RevisionCount: 1, Title: "Revision one", Summary: "Bounded revision one summary", Status: model.ADRStatusAccepted, Context: "context", Decision: "old decision", Consequences: "consequences", CreatedAt: now, UpdatedAt: now}
	firstPayload, err := json.Marshal(adr)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.publishSharedADROutbox(ctx, sqlitestore.OutboxEntry{ID: "adr-revision-1", EntityType: "adr", EntityID: adr.ID, Revision: 1, Payload: firstPayload}); err != nil {
		t.Fatal(err)
	}
	adr.Revision = 2
	adr.RevisionCount = 2
	adr.Decision = "new decision"
	adr.LastReason = "accepted by user"
	adr.UpdatedAt = now.Add(time.Second)
	secondPayload, err := json.Marshal(adr)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.publishSharedADROutbox(ctx, sqlitestore.OutboxEntry{ID: "adr-revision-2", EntityType: "adr", EntityID: adr.ID, Revision: 2, Payload: secondPayload}); err != nil {
		t.Fatalf("new ADR revision was treated as no-op: %v", err)
	}
	var published model.ADR
	if err := s.Hub.ReadJSON(ctx, s.adrPath("example", adr.ID), &published); err != nil {
		t.Fatal(err)
	}
	if published.Revision != 2 || published.Decision != "new decision" {
		t.Fatalf("published ADR=%#v, want revision 2 with new content", published)
	}

	db, err := sqlitestore.Open(s.Config.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s.Durability = db
	createdAt := now.Format(time.RFC3339Nano)
	if _, err := db.Shared.Exec(ctx, `INSERT INTO hub_outbox(id,entity_type,entity_id,project_id,revision,kind,payload,created_at) VALUES(?,?,?,?,?,?,?,?)`, "adr-failure", "adr", "EXM-ADR566", "example", 3, "adr-update", []byte(`{"invalid":true}`), createdAt); err != nil {
		t.Fatal(err)
	}
	pending, err := db.PendingOutbox(ctx, 10)
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending=%#v err=%v", pending, err)
	}
	publicationErr := s.publishSharedOutboxEntry(ctx, pending[0])
	if publicationErr == nil || errors.Is(publicationErr, errSharedOutboxNoop) {
		t.Fatalf("genuine publication failure=%v", publicationErr)
	}
	if err := db.MarkOutboxRetry(ctx, pending[0].ID, now.Add(time.Second), publicationErr); err != nil {
		t.Fatal(err)
	}
	retried, found, err := db.ReadSharedOutboxEntry(ctx, pending[0].ID)
	if err != nil || !found || retried.Attempts != 1 || retried.LastError == "" {
		t.Fatalf("retry state=%#v found=%v err=%v", retried, found, err)
	}
	if !strings.Contains(retried.LastError, "invalid") {
		t.Fatalf("retry error=%q", retried.LastError)
	}
}

func TestSharedRelationOutboxPublishesCanonicalHubRecord(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiersSetup(t)
	ctx := context.Background()
	relation := model.Relation{
		SchemaVersion: model.RelationSchemaVersion,
		ProjectID:     "example",
		Kind:          model.RelationKindCorrects,
		Source:        "EXM-TSK11",
		Target:        "EXM-TSK12",
		CreatedAt:     time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC),
		CreatedBy:     "planner",
	}
	payload, err := json.Marshal(relation)
	if err != nil {
		t.Fatal(err)
	}
	entry := sqlitestore.OutboxEntry{ID: "relation-" + relation.Identity(), EntityType: "relation", EntityID: relation.Identity(), Revision: 1, Kind: "relation-create", Payload: payload}
	if _, ok := sharedOutboxPublishers["relation"]; !ok {
		t.Fatal("relation has no Shared outbox publisher")
	}
	if err := s.publishSharedOutboxEntry(ctx, entry); err != nil {
		t.Fatal(err)
	}
	var published model.Relation
	if err := s.Hub.ReadJSON(ctx, s.relationPath(relation), &published); err != nil {
		t.Fatal(err)
	}
	if published != relation {
		t.Fatalf("published relation=%#v want %#v", published, relation)
	}
	if err := s.publishSharedOutboxEntry(ctx, entry); !errors.Is(err, errSharedOutboxNoop) {
		t.Fatalf("duplicate relation publication error=%v, want terminal no-op", err)
	}
}
