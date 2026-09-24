package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
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

func TestTSK666Gate20TrackAcceptedOutboxConverges(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiersSetup(t)
	db, err := sqlitestore.Open(s.Config.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s.Durability = db
	ctx := context.Background()
	track := tsk666AcceptedTrack()
	payload, err := json.Marshal(track)
	if err != nil {
		t.Fatal(err)
	}
	path := s.trackPath(track.ProjectID, track.ID)
	if _, err := s.Hub.Transact(ctx, "", "test: seed already-applied Track acceptance", func(worktree string) ([]string, error) {
		if err := hub.WriteJSON(worktree, path, tsk666TrackWithOffsetTimes(track)); err != nil {
			return nil, err
		}
		return []string{path}, nil
	}); err != nil {
		t.Fatal(err)
	}
	createdAt := track.UpdatedAt.Format(time.RFC3339Nano)
	if _, err := db.Shared.Exec(ctx, `INSERT INTO shared_tracks(id,revision,payload,updated_at) VALUES(?,?,?,?)`, track.ID, track.Revision, payload, createdAt); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Shared.Exec(ctx, `INSERT INTO hub_outbox(id,entity_type,entity_id,project_id,revision,kind,payload,created_at) VALUES(?,?,?,?,?,?,?,?)`, "track-accept-GTW-TRK1-r8", "track", track.ID, track.ProjectID, track.Revision, "track-accept", payload, createdAt); err != nil {
		t.Fatal(err)
	}
	pending, err := db.PendingOutbox(ctx, 10)
	var delivery *sqlitestore.OutboxEntry
	for index := range pending {
		if pending[index].ID == "track-accept-GTW-TRK1-r8" {
			delivery = &pending[index]
			break
		}
	}
	if err != nil || delivery == nil {
		var ids []string
		for _, item := range pending {
			ids = append(ids, item.ID)
		}
		t.Fatalf("pending stale Track delivery IDs=%v err=%v", ids, err)
	}
	if err := s.deliverSharedOutboxEntry(ctx, *delivery); err != nil {
		t.Fatalf("deliver already-applied Track acceptance: %v", err)
	}
	stored, found, err := db.ReadSharedOutboxEntry(ctx, delivery.ID)
	if err != nil || !found || stored.PublishedAt == "" {
		t.Fatalf("stale Track delivery was not marked published: entry=%#v found=%v err=%v", stored, found, err)
	}
	health, err := db.SharedSyncHealth(ctx)
	if err != nil || health.Pending != 0 || health.Retrying != 0 {
		t.Fatalf("shared sync health after convergence=%#v err=%v", health, err)
	}
}

func TestTSK666Gate20TrackSameRevisionDivergenceEvidence(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiersSetup(t)
	ctx := context.Background()
	track := tsk666AcceptedTrack()
	payload, err := json.Marshal(track)
	if err != nil {
		t.Fatal(err)
	}
	divergent := track
	divergent.Title = "Divergent Track title"
	path := s.trackPath(track.ProjectID, track.ID)
	if _, err := s.Hub.Transact(ctx, "", "test: seed divergent same-revision Track", func(worktree string) ([]string, error) {
		if err := hub.WriteJSON(worktree, path, divergent); err != nil {
			return nil, err
		}
		return []string{path}, nil
	}); err != nil {
		t.Fatal(err)
	}
	entry := sqlitestore.OutboxEntry{ID: "track-accept-GTW-TRK1-r8", EntityType: "track", EntityID: track.ID, Revision: int64(track.Revision), Payload: payload}
	publicationErr := s.publishSharedTrackOutbox(ctx, entry)
	if publicationErr == nil || errors.Is(publicationErr, errSharedOutboxNoop) || !strings.Contains(publicationErr.Error(), "Hub Track \"GTW-TRK1\" conflicts at revision 8") || !strings.Contains(publicationErr.Error(), "Hub semantic sha256=") || !strings.Contains(publicationErr.Error(), "Shared outbox semantic sha256=") {
		t.Fatalf("same-revision divergence error=%v", publicationErr)
	}
}

func tsk666AcceptedTrack() model.Track {
	createdAt := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	updatedAt := createdAt.Add(time.Hour)
	return model.Track{
		SchemaVersion: model.TrackSchemaVersion,
		ID:            "GTW-TRK1",
		ProjectID:     "gpt-tunnel-gateway",
		Revision:      8,
		Milestone:     "GTW-MIL1",
		Title:         "Track acceptance fixture",
		Summary:       "Converges an already-applied acceptance delivery.",
		Tasks:         []string{"GTW-TSK1"},
		Status:        model.TrackAccepted,
		Review: &model.TrackReview{
			Head:          strings.Repeat("a", 40),
			Tree:          strings.Repeat("b", 40),
			Digest:        strings.Repeat("c", 64),
			TrackRevision: 8,
			Tasks:         []model.TrackTaskSnapshot{{Key: "GTW-TSK1", Revision: 1, RevisionSHA256: strings.Repeat("d", 64)}},
			SubmittedAt:   updatedAt,
			SubmittedBy:   "planner",
		},
		CreatedBy: "planner",
		CreatedAt: createdAt,
		UpdatedBy: "planner",
		UpdatedAt: updatedAt,
	}
}

func tsk666TrackWithOffsetTimes(track model.Track) model.Track {
	offset := time.FixedZone("UTC+02", 2*60*60)
	track.CreatedAt = track.CreatedAt.In(offset)
	track.UpdatedAt = track.UpdatedAt.In(offset)
	if track.Review != nil {
		review := *track.Review
		review.SubmittedAt = review.SubmittedAt.In(offset)
		track.Review = &review
	}
	return track
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
