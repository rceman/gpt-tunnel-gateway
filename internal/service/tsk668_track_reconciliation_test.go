package service

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

func TestTSK668LegacyTrackAcceptOutboxReconcilesFromSharedHistoryAndDrains(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiersSetup(t)
	db, err := sqlitestore.Open(s.Config.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s.Durability = db
	ctx := context.Background()
	canonical, historical := tsk668TrackFixtures()
	entry := seedTSK668TrackAcceptance(t, db, canonical, historical)
	seedTSK668HubTrack(t, s, historical)
	legacyDigest, err := trackSemanticDigest(historical)
	if err != nil {
		t.Fatal(err)
	}
	canonicalDigest, err := trackSemanticDigest(canonical)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.publishTSK668TrackAcceptOutboxWithFingerprints(ctx, entry, trackReconciliationFingerprints{
		legacy:    legacyDigest,
		canonical: canonicalDigest,
	}); err != nil {
		t.Fatalf("reconcile exact pre-publisher Track state: %v", err)
	}
	var published model.Track
	if err := s.Hub.ReadJSON(ctx, s.trackPath(tsk668TrackProjectID, tsk668TrackID), &published); err != nil {
		t.Fatal(err)
	}
	if !tracksSemanticallyEqual(published, canonical) {
		t.Fatalf("Hub Track after reconciliation=%#v, want canonical %#v", published, canonical)
	}
	var evidence trackCurrentReconciliationEvidence
	if err := s.Hub.ReadJSON(ctx, s.tsk668TrackReconciliationEvidencePath(), &evidence); err != nil {
		t.Fatal(err)
	}
	if evidence.MigrationID != tsk668TrackMigrationID || evidence.OutboxID != tsk668TrackAcceptOutboxID || evidence.LegacySemanticSHA256 != legacyDigest || evidence.CanonicalSemanticSHA256 != canonicalDigest || !reflect.DeepEqual(evidence.ReplacedFields, []string{"status", "updated_at", "updated_by"}) || !tracksSemanticallyEqual(evidence.LegacyTrack, historical) {
		t.Fatalf("reconciliation evidence=%#v", evidence)
	}
	if err := s.deliverSharedOutboxEntry(ctx, entry); err != nil {
		t.Fatalf("deliver reconciled Track acceptance: %v", err)
	}
	stored, found, err := db.ReadSharedOutboxEntry(ctx, entry.ID)
	if err != nil || !found || stored.PublishedAt == "" {
		t.Fatalf("outbox after reconciliation=%#v found=%v err=%v", stored, found, err)
	}
	health, err := db.SharedSyncHealth(ctx)
	if err != nil || health.Pending != 0 || health.Retrying != 0 {
		t.Fatalf("shared sync health after reconciliation=%#v err=%v", health, err)
	}
}

func TestTSK668LegacyTrackAcceptReconciliationRejectsNearMisses(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*model.Track)
	}{
		{name: "title", mutate: func(track *model.Track) { track.Title = "Different title" }},
		{name: "status", mutate: func(track *model.Track) { track.Status = model.TrackStale }},
		{name: "updated_at", mutate: func(track *model.Track) { track.UpdatedAt = track.UpdatedAt.Add(time.Nanosecond) }},
		{name: "updated_by", mutate: func(track *model.Track) { track.UpdatedBy = "another-lead" }},
		{name: "review_digest", mutate: func(track *model.Track) { track.Review.Digest = strings.Repeat("e", 64) }},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			s, _, _ := testServiceWithoutIdentifiersSetup(t)
			db, err := sqlitestore.Open(s.Config.StateDir)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			s.Durability = db
			ctx := context.Background()
			canonical, historical := tsk668TrackFixtures()
			entry := seedTSK668TrackAcceptance(t, db, canonical, historical)
			nearMiss := historical
			tc.mutate(&nearMiss)
			seedTSK668HubTrack(t, s, nearMiss)
			if err := s.deliverSharedOutboxEntry(ctx, entry); err == nil || !strings.Contains(err.Error(), "Hub Track \"GTW-TRK1\" conflicts at revision 8") {
				t.Fatalf("near-miss delivery error=%v", err)
			}
			var unchanged model.Track
			if err := s.Hub.ReadJSON(ctx, s.trackPath(tsk668TrackProjectID, tsk668TrackID), &unchanged); err != nil {
				t.Fatal(err)
			}
			if !tracksSemanticallyEqual(unchanged, nearMiss) {
				t.Fatalf("near-miss Hub Track changed: got %#v want %#v", unchanged, nearMiss)
			}
			var evidence trackCurrentReconciliationEvidence
			if err := s.Hub.ReadJSON(ctx, s.tsk668TrackReconciliationEvidencePath(), &evidence); err == nil || !IsNotFound(err) {
				t.Fatalf("near-miss wrote reconciliation evidence: evidence=%#v err=%v", evidence, err)
			}
			stored, found, err := db.ReadSharedOutboxEntry(ctx, entry.ID)
			if err != nil || !found || stored.PublishedAt != "" || stored.Attempts != 1 || stored.LastError == "" {
				t.Fatalf("near-miss outbox state=%#v found=%v err=%v", stored, found, err)
			}
		})
	}
}

func TestTSK668LegacyTrackAcceptDoesNotRegressNewerHubRevision(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiersSetup(t)
	db, err := sqlitestore.Open(s.Config.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s.Durability = db
	ctx := context.Background()
	canonical, historical := tsk668TrackFixtures()
	entry := seedTSK668TrackAcceptance(t, db, canonical, historical)
	newer := canonical
	newer.Revision++
	newer.Review.TrackRevision++
	newer.UpdatedAt = newer.UpdatedAt.Add(time.Second)
	seedTSK668HubTrack(t, s, newer)
	if err := s.deliverSharedOutboxEntry(ctx, entry); err != nil {
		t.Fatalf("deliver older Track outbox against newer Hub state: %v", err)
	}
	var unchanged model.Track
	if err := s.Hub.ReadJSON(ctx, s.trackPath(tsk668TrackProjectID, tsk668TrackID), &unchanged); err != nil {
		t.Fatal(err)
	}
	if !tracksSemanticallyEqual(unchanged, newer) {
		t.Fatalf("newer Hub Track regressed: got %#v want %#v", unchanged, newer)
	}
	stored, found, err := db.ReadSharedOutboxEntry(ctx, entry.ID)
	if err != nil || !found || stored.PublishedAt == "" {
		t.Fatalf("outbox after newer Hub no-op=%#v found=%v err=%v", stored, found, err)
	}
}

func TestTSK668RecordedTrackDigestsMatchLeadEvidence(t *testing.T) {
	if tsk668LegacyTrackSHA256 != "fe86a4e8e3da83a51202a2cc60b70f5869e4bc627c6d603e36b769eb857f5033" || tsk668CanonicalTrackSHA256 != "43ecf4af400252617a23cd3032e133ff5ef5cd01bf26bfb77b37cdb4ee527372" {
		t.Fatalf("TSK668 semantic fingerprints no longer match GTW-JRN11")
	}
}

func tsk668TrackFixtures() (model.Track, model.Track) {
	legacyAt, _ := time.Parse(time.RFC3339Nano, tsk668LegacyTrackUpdatedAt)
	canonicalAt, _ := time.Parse(time.RFC3339Nano, tsk668CanonicalTrackUpdatedAt)
	createdAt := time.Date(2026, 9, 23, 9, 56, 34, 0, time.UTC)
	tasks := []string{"GTW-TSK595", "GTW-TSK597", "GTW-TSK598", "GTW-TSK599", "GTW-TSK663"}
	snapshots := []model.TrackTaskSnapshot{
		{Key: "GTW-TSK595", Revision: 10, RevisionSHA256: strings.Repeat("d", 64)},
		{Key: "GTW-TSK597", Revision: 5, RevisionSHA256: strings.Repeat("e", 64)},
		{Key: "GTW-TSK598", Revision: 6, RevisionSHA256: strings.Repeat("f", 64)},
		{Key: "GTW-TSK599", Revision: 2, RevisionSHA256: strings.Repeat("1", 64)},
		{Key: "GTW-TSK663", Revision: 1, RevisionSHA256: strings.Repeat("2", 64)},
	}
	canonical := model.Track{
		SchemaVersion:   model.TrackSchemaVersion,
		ID:              tsk668TrackID,
		ProjectID:       tsk668TrackProjectID,
		Revision:        int(tsk668TrackRevision),
		Milestone:       "GTW-MIL1",
		Title:           "Action Contract Hard Cut",
		Summary:         "Freeze the surviving public action inventory, build the YAML contract compiler, migrate the surface, and remove legacy schema authority.",
		Tasks:           tasks,
		DispatchedTasks: append([]string(nil), tasks...),
		Status:          model.TrackAccepted,
		Review: &model.TrackReview{
			Head:          "3835b898" + strings.Repeat("a", 32),
			Tree:          "e9a685f8" + strings.Repeat("b", 32),
			Digest:        strings.Repeat("c", 64),
			TrackRevision: int(tsk668TrackRevision),
			Tasks:         snapshots,
			SubmittedAt:   legacyAt,
			SubmittedBy:   tsk668LegacyTrackUpdatedBy,
		},
		CreatedBy: tsk668CanonicalTrackUpdatedBy,
		CreatedAt: createdAt,
		UpdatedBy: tsk668CanonicalTrackUpdatedBy,
		UpdatedAt: canonicalAt,
	}
	legacy := canonical
	legacy.Status = model.TrackReviewPending
	legacy.UpdatedBy = tsk668LegacyTrackUpdatedBy
	legacy.UpdatedAt = legacyAt
	return canonical, legacy
}

func seedTSK668TrackAcceptance(t *testing.T, db *sqlitestore.Databases, canonical, historical model.Track) sqlitestore.OutboxEntry {
	t.Helper()
	ctx := context.Background()
	canonicalPayload, err := json.Marshal(canonical)
	if err != nil {
		t.Fatal(err)
	}
	historicalPayload, err := json.Marshal(historical)
	if err != nil {
		t.Fatal(err)
	}
	legacyAt := historical.UpdatedAt.UTC().Format(time.RFC3339Nano)
	canonicalAt := canonical.UpdatedAt.UTC().Format(time.RFC3339Nano)
	if _, err := db.Shared.Exec(ctx, `INSERT INTO shared_tracks(id,revision,payload,updated_at) VALUES(?,?,?,?)`, canonical.ID, canonical.Revision, canonicalPayload, canonicalAt); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Shared.Exec(ctx, `INSERT INTO shared_entity_revisions(entity_type,entity_id,project_id,revision,mutation_kind,actor,reason,changed_fields,payload,recorded_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, "track", historical.ID, historical.ProjectID, historical.Revision, "update", tsk668LegacyTrackUpdatedBy, "submit Track for Planner acceptance", []byte(`["status","review"]`), historicalPayload, legacyAt); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Shared.Exec(ctx, `INSERT INTO shared_lifecycle_events(operation_id,entity_type,project_id,entity_id,revision,event_kind,from_status,to_status,actor,reason,contract,recorded_at,mutation_kind,changed_fields) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, tsk668TrackAcceptOutboxID, "track", canonical.ProjectID, canonical.ID, canonical.Revision, "status", model.TrackReviewPending, model.TrackAccepted, tsk668CanonicalTrackUpdatedBy, "accept fresh Track review", []byte(`{"entity":"track"}`), canonicalAt, "status", []byte(`["status"]`)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Shared.Exec(ctx, `INSERT INTO hub_outbox(id,entity_type,entity_id,project_id,revision,kind,payload,created_at) VALUES(?,?,?,?,?,?,?,?)`, tsk668TrackAcceptOutboxID, "track", canonical.ID, canonical.ProjectID, canonical.Revision, "track-accept", canonicalPayload, canonicalAt); err != nil {
		t.Fatal(err)
	}
	pending, err := db.PendingOutbox(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range pending {
		if entry.ID == tsk668TrackAcceptOutboxID {
			return entry
		}
	}
	t.Fatal("TSK668 Track acceptance was not pending")
	return sqlitestore.OutboxEntry{}
}

func seedTSK668HubTrack(t *testing.T, s *Service, track model.Track) {
	t.Helper()
	path := s.trackPath(track.ProjectID, track.ID)
	if _, err := s.Hub.Transact(context.Background(), "", "test: seed TSK668 Hub Track", func(worktree string) ([]string, error) {
		if err := hub.WriteJSON(worktree, path, track); err != nil {
			return nil, err
		}
		return []string{path}, nil
	}); err != nil {
		t.Fatal(err)
	}
}
