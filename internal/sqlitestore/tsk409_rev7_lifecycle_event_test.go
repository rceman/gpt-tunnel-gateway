package sqlitestore

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/rceman/go-sqlite-store/migrate"
	upstream "github.com/rceman/go-sqlite-store/store"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func adrRev7Fixture(id, status string, revision int, now time.Time) model.ADR {
	return model.ADR{SchemaVersion: model.SchemaVersion, ID: id, ProjectID: "example", Revision: revision, RevisionCount: revision,
		Title: "Rev7 ADR", Summary: "Bounded rev7 ADR summary", Status: status, Context: "context", Decision: "decision",
		Consequences: "consequences", CreatedBy: "planner", CreatedAt: now, UpdatedBy: "planner", UpdatedAt: now, LastReason: "create"}
}

func openRev7RawBaseline(t *testing.T) *upstream.Store {
	t.Helper()
	ctx := context.Background()
	db, err := upstream.Open(upstream.Config{Path: t.TempDir() + "/shared.db"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := migrate.Apply(ctx, db, []migrate.Migration{sharedBaselineMigration(), sharedLifecycleEventMigration()}, migrate.Options{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func openRev7Databases(t *testing.T) *Databases {
	t.Helper()
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func insertRev7ADR(t *testing.T, db *Databases, adr model.ADR, now time.Time) []byte {
	t.Helper()
	ctx := context.Background()
	payload := mustJSON(t, adr)
	if _, err := db.Shared.Exec(ctx, `INSERT INTO shared_adrs(id,revision,payload,updated_at) VALUES(?,?,?,?)`, adr.ID, adr.Revision, payload, now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Shared.Exec(ctx, `INSERT INTO shared_entity_revisions(entity_type,entity_id,project_id,revision,mutation_kind,actor,reason,changed_fields,payload,recorded_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, "adr", adr.ID, adr.ProjectID, adr.Revision, "create", "planner", "create", []byte(`["title","summary"]`), payload, now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	return payload
}

func TestTSK409Rev7ADRSummaryMigrationAddsValidatedTitleAndPreservesHistory(t *testing.T) {
	ctx := context.Background()
	db := openRev7RawBaseline(t)
	oldTime := time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC)
	legacy := adrRev7Fixture("EXM-ADR1", model.ADRStatusAccepted, 1, oldTime)
	legacy.Summary = ""
	oldPayload := mustJSON(t, legacy)
	if _, err := db.Exec(ctx, `INSERT INTO shared_adrs(id,revision,payload,updated_at) VALUES(?,?,?,?)`, legacy.ID, 1, oldPayload, oldTime.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	migration, err := sharedADRSummaryMigration(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if err := migrate.Apply(ctx, db, []migrate.Migration{migration}, migrate.Options{}); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query(ctx, `SELECT revision,payload FROM shared_adrs WHERE id=?`, legacy.ID)
	if err != nil || len(rows.Rows) != 1 || rows.Rows[0][0] != int64(2) {
		t.Fatalf("migrated ADR row=%#v err=%v", rows.Rows, err)
	}
	var migrated model.ADR
	if err := json.Unmarshal(rows.Rows[0][1].([]byte), &migrated); err != nil {
		t.Fatal(err)
	}
	if migrated.Summary != legacy.Title {
		t.Fatalf("migrated summary=%q want validated title %q", migrated.Summary, legacy.Title)
	}
	if err := model.ValidateADR(migrated); err != nil {
		t.Fatalf("migrated ADR invalid: %v", err)
	}
	history, err := db.Query(ctx, `SELECT revision,mutation_kind,changed_fields,payload FROM shared_entity_revisions WHERE entity_type='adr' AND entity_id=? ORDER BY revision`, legacy.ID)
	if err != nil || len(history.Rows) != 2 {
		t.Fatalf("history=%#v err=%v", history.Rows, err)
	}
	if string(history.Rows[0][3].([]byte)) != string(oldPayload) || history.Rows[0][1] != "migration" || string(history.Rows[0][2].([]byte)) != `["legacy"]` {
		t.Fatalf("preserved pre-summary payload=%#v", history.Rows[0])
	}
	if string(history.Rows[1][2].([]byte)) != `["summary"]` {
		t.Fatalf("summary revision evidence=%#v", history.Rows[1])
	}
	if count, err := db.Query(ctx, `SELECT COUNT(*) FROM hub_outbox WHERE entity_id=?`, legacy.ID); err != nil || count.Rows[0][0] != int64(1) {
		t.Fatalf("summary migration outbox=%#v err=%v", count.Rows, err)
	}
}

func TestTSK409Rev7ADRSummaryMigrationFailsClosedOnOversizedTitle(t *testing.T) {
	ctx := context.Background()
	db := openRev7RawBaseline(t)
	now := time.Now().UTC()
	legacy := adrRev7Fixture("EXM-ADR2", model.ADRStatusAccepted, 1, now)
	legacy.Summary = ""
	legacy.Title = strings.Repeat("x", 129)
	if _, err := db.Exec(ctx, `INSERT INTO shared_adrs(id,revision,payload,updated_at) VALUES(?,?,?,?)`, legacy.ID, 1, mustJSON(t, legacy), now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if _, err := sharedADRSummaryMigration(ctx, db); err == nil {
		t.Fatal("oversized retained ADR title was accepted")
	}
}

func TestTSK409Rev7OpenAppliesADRSummaryMigrationOnce(t *testing.T) {
	ctx := context.Background()
	fresh := openRev7RawBaseline(t)
	now := time.Date(2026, 9, 11, 11, 0, 0, 0, time.UTC)
	legacy := adrRev7Fixture("EXM-ADR3", model.ADRStatusProposed, 1, now)
	legacy.Summary = ""
	if _, err := fresh.Exec(ctx, `INSERT INTO shared_adrs(id,revision,payload,updated_at) VALUES(?,?,?,?)`, legacy.ID, 1, mustJSON(t, legacy), now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if err := applySharedMigrations(ctx, fresh); err != nil {
		t.Fatal(err)
	}
	first, err := fresh.Query(ctx, `SELECT revision,payload FROM shared_adrs WHERE id=?`, legacy.ID)
	if err != nil || len(first.Rows) != 1 || first.Rows[0][0] != int64(2) {
		t.Fatalf("dispatcher migrated ADR=%#v err=%v", first.Rows, err)
	}
	for _, marker := range []struct {
		version int64
		name    string
	}{{sharedADRSummaryMigrationVersion, sharedADRSummaryMigrationName}, {sharedLifecycleEventMigrationVersion, sharedLifecycleEventMigrationName}} {
		rows, err := fresh.Query(ctx, `SELECT name FROM schema_migrations WHERE version=?`, marker.version)
		if err != nil || len(rows.Rows) != 1 || rows.Rows[0][0] != marker.name {
			t.Fatalf("marker %d=%#v err=%v", marker.version, rows.Rows, err)
		}
	}
	if err := applySharedMigrations(ctx, fresh); err != nil {
		t.Fatal(err)
	}
	second, err := fresh.Query(ctx, `SELECT revision,payload FROM shared_adrs WHERE id=?`, legacy.ID)
	if err != nil || len(second.Rows) != 1 || second.Rows[0][0] != first.Rows[0][0] || string(second.Rows[0][1].([]byte)) != string(first.Rows[0][1].([]byte)) {
		t.Fatalf("repeat dispatcher apply changed ADR: first=%#v second=%#v err=%v", first.Rows, second.Rows, err)
	}
}

func TestTSK409Rev7LifecycleEventStatusOnlyKeepsRevisionAndCommitsAtomically(t *testing.T) {
	ctx := context.Background()
	db := openRev7Databases(t)
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	adr := adrRev7Fixture("EXM-ADR4", model.ADRStatusProposed, 1, now)
	previous := insertRev7ADR(t, db, adr, now)
	next := adr
	next.Status = model.ADRStatusAccepted
	next.UpdatedAt = now.Add(time.Minute)
	next.LastReason = "accepted by owner"
	nextPayload := mustJSON(t, next)
	receipt, err := db.CommitSharedLifecycleEvent(ctx, SharedLifecycleEventRequest{
		OperationID:           "adr-update-rev7-status-only",
		EntityType:            "adr",
		ProjectID:             adr.ProjectID,
		EntityID:              adr.ID,
		ExpectedRevision:      1,
		ExpectedStoreRevision: 1,
		ExpectedPayload:       previous,
		Revision:              1,
		Kind:                  "adr-update",
		EventKind:             SharedLifecycleEventKindStatus,
		FromStatus:            model.ADRStatusProposed,
		ToStatus:              model.ADRStatusAccepted,
		Payload:               nextPayload,
		Actor:                 "planner",
		Reason:                next.LastReason,
		ChangedFields:         []string{"status"},
		Contract:              []byte(`{"contract":"rev7"}`),
		CreatedAt:             next.UpdatedAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Committed || receipt.Revision != 1 {
		t.Fatalf("receipt=%#v", receipt)
	}
	state, err := db.Shared.Query(ctx, `SELECT revision,payload FROM shared_adrs WHERE id=?`, adr.ID)
	if err != nil || len(state.Rows) != 1 || state.Rows[0][0] != int64(1) {
		t.Fatalf("state=%#v err=%v", state.Rows, err)
	}
	var stored model.ADR
	if err := json.Unmarshal(state.Rows[0][1].([]byte), &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Status != model.ADRStatusAccepted || stored.Revision != 1 {
		t.Fatalf("status-only state=%#v", stored)
	}
	history, err := db.Shared.Query(ctx, `SELECT revision,payload FROM shared_entity_revisions WHERE entity_type='adr' AND entity_id=? ORDER BY revision`, adr.ID)
	if err != nil || len(history.Rows) != 1 || string(history.Rows[0][1].([]byte)) != string(previous) {
		t.Fatalf("immutable history=%#v err=%v", history.Rows, err)
	}
	events, err := db.Shared.Query(ctx, `SELECT revision,event_kind,from_status,to_status FROM shared_lifecycle_events WHERE entity_id=?`, adr.ID)
	if err != nil || len(events.Rows) != 1 || events.Rows[0][0] != int64(1) || events.Rows[0][2] != model.ADRStatusProposed || events.Rows[0][3] != model.ADRStatusAccepted {
		t.Fatalf("lifecycle event=%#v err=%v", events.Rows, err)
	}
	outbox, err := db.Shared.Query(ctx, `SELECT revision,kind FROM hub_outbox WHERE entity_id=?`, adr.ID)
	if err != nil || len(outbox.Rows) != 1 || outbox.Rows[0][0] != int64(1) {
		t.Fatalf("outbox=%#v err=%v", outbox.Rows, err)
	}
}

func TestTSK409Rev7LifecycleEventContentTransitionIncrementsOnce(t *testing.T) {
	ctx := context.Background()
	db := openRev7Databases(t)
	now := time.Date(2026, 9, 11, 13, 0, 0, 0, time.UTC)
	adr := adrRev7Fixture("EXM-ADR5", model.ADRStatusProposed, 1, now)
	previous := insertRev7ADR(t, db, adr, now)
	next := adr
	next.Status = model.ADRStatusAccepted
	next.Revision = 2
	next.RevisionCount = 2
	next.Decision = "revised decision"
	next.UpdatedAt = now.Add(time.Minute)
	next.LastReason = "content and status"
	receipt, err := db.CommitSharedLifecycleEvent(ctx, SharedLifecycleEventRequest{
		OperationID:           "adr-update-rev7-content",
		EntityType:            "adr",
		ProjectID:             adr.ProjectID,
		EntityID:              adr.ID,
		ExpectedRevision:      1,
		ExpectedStoreRevision: 1,
		ExpectedPayload:       previous,
		Revision:              2,
		Kind:                  "adr-update",
		EventKind:             SharedLifecycleEventKindStatus,
		FromStatus:            model.ADRStatusProposed,
		ToStatus:              model.ADRStatusAccepted,
		Payload:               mustJSON(t, next),
		Actor:                 "planner",
		Reason:                next.LastReason,
		ChangedFields:         []string{"decision", "status"},
		Contract:              []byte(`{"contract":"rev7-content"}`),
		CreatedAt:             next.UpdatedAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Committed || receipt.Revision != 2 {
		t.Fatalf("receipt=%#v", receipt)
	}
	state, err := db.Shared.Query(ctx, `SELECT revision FROM shared_adrs WHERE id=?`, adr.ID)
	if err != nil || len(state.Rows) != 1 || state.Rows[0][0] != int64(2) {
		t.Fatalf("content state=%#v err=%v", state.Rows, err)
	}
	history, err := db.Shared.Query(ctx, `SELECT revision FROM shared_entity_revisions WHERE entity_type='adr' AND entity_id=? ORDER BY revision`, adr.ID)
	if err != nil || len(history.Rows) != 2 || history.Rows[1][0] != int64(2) {
		t.Fatalf("content history=%#v err=%v", history.Rows, err)
	}
	events, err := db.Shared.Query(ctx, `SELECT revision FROM shared_lifecycle_events WHERE entity_id=?`, adr.ID)
	if err != nil || len(events.Rows) != 1 || events.Rows[0][0] != int64(2) {
		t.Fatalf("content event=%#v err=%v", events.Rows, err)
	}
	outbox, err := db.Shared.Query(ctx, `SELECT COUNT(*) FROM hub_outbox WHERE entity_id=?`, adr.ID)
	if err != nil || outbox.Rows[0][0] != int64(1) {
		t.Fatalf("content outbox=%#v err=%v", outbox.Rows, err)
	}
}

func TestTSK409Rev7LifecycleEventCASAndHistoryConflictFailClosed(t *testing.T) {
	ctx := context.Background()
	db := openRev7Databases(t)
	now := time.Date(2026, 9, 11, 14, 0, 0, 0, time.UTC)
	adr := adrRev7Fixture("EXM-ADR6", model.ADRStatusProposed, 1, now)
	previous := insertRev7ADR(t, db, adr, now)
	next := adr
	next.Status = model.ADRStatusAccepted
	next.UpdatedAt = now.Add(time.Minute)
	nextPayload := mustJSON(t, next)
	base := SharedLifecycleEventRequest{
		EntityType:            "adr",
		ProjectID:             adr.ProjectID,
		EntityID:              adr.ID,
		ExpectedRevision:      1,
		ExpectedStoreRevision: 1,
		ExpectedPayload:       previous,
		Revision:              1,
		Kind:                  "adr-update",
		EventKind:             SharedLifecycleEventKindStatus,
		FromStatus:            model.ADRStatusProposed,
		ToStatus:              model.ADRStatusAccepted,
		Payload:               nextPayload,
		Actor:                 "planner",
		Reason:                "conflict probe",
		ChangedFields:         []string{"status"},
		Contract:              []byte(`{"contract":"rev7-cas"}`),
		CreatedAt:             next.UpdatedAt,
	}
	stale := base
	stale.OperationID = "adr-update-rev7-stale-cas"
	stale.ExpectedPayload = []byte(`{"schema_version":1,"id":"EXM-ADR6","project_id":"example","revision":1,"title":"Rev7 ADR","summary":"stale","status":"proposed"}`)
	if _, err := db.CommitSharedLifecycleEvent(ctx, stale); err == nil {
		t.Fatal("stale CAS payload was accepted")
	}
	conflict := base
	conflict.OperationID = "adr-update-rev7-history-conflict"
	conflict.PreviousHistory = &SharedHistorySeed{
		Revision:      1,
		MutationKind:  "migration",
		Actor:         "system",
		Reason:        "seed",
		ChangedFields: []string{"legacy"},
		Payload:       []byte(`{"different":true}`),
		RecordedAt:    now.Format(time.RFC3339Nano),
	}
	if _, err := db.CommitSharedLifecycleEvent(ctx, conflict); err == nil {
		t.Fatal("conflicting immutable history seed was accepted")
	}
	state, err := db.Shared.Query(ctx, `SELECT revision,payload FROM shared_adrs WHERE id=?`, adr.ID)
	if err != nil || len(state.Rows) != 1 || state.Rows[0][0] != int64(1) || string(state.Rows[0][1].([]byte)) != string(previous) {
		t.Fatalf("state after rejected transitions=%#v err=%v", state.Rows, err)
	}
	for _, table := range []string{"shared_lifecycle_events", "hub_outbox"} {
		count, err := db.Shared.Query(ctx, `SELECT COUNT(*) FROM `+table+` WHERE entity_id=?`, adr.ID)
		if err != nil || count.Rows[0][0] != int64(0) {
			t.Fatalf("%s rows after rejected transitions=%#v err=%v", table, count.Rows, err)
		}
	}
	history, err := db.Shared.Query(ctx, `SELECT COUNT(*) FROM shared_entity_revisions WHERE entity_type='adr' AND entity_id=?`, adr.ID)
	if err != nil || history.Rows[0][0] != int64(1) {
		t.Fatalf("history after rejected transitions=%#v err=%v", history.Rows, err)
	}
}

func TestTSK409Rev7LifecycleEventReuseIsIdempotent(t *testing.T) {
	ctx := context.Background()
	db := openRev7Databases(t)
	now := time.Date(2026, 9, 11, 15, 0, 0, 0, time.UTC)
	adr := adrRev7Fixture("EXM-ADR7", model.ADRStatusProposed, 1, now)
	previous := insertRev7ADR(t, db, adr, now)
	next := adr
	next.Status = model.ADRStatusAccepted
	next.UpdatedAt = now.Add(time.Minute)
	request := SharedLifecycleEventRequest{
		OperationID:           "adr-update-rev7-reuse",
		EntityType:            "adr",
		ProjectID:             adr.ProjectID,
		EntityID:              adr.ID,
		ExpectedRevision:      1,
		ExpectedStoreRevision: 1,
		ExpectedPayload:       previous,
		Revision:              1,
		Kind:                  "adr-update",
		EventKind:             SharedLifecycleEventKindStatus,
		FromStatus:            model.ADRStatusProposed,
		ToStatus:              model.ADRStatusAccepted,
		Payload:               mustJSON(t, next),
		Actor:                 "planner",
		Reason:                "reuse probe",
		ChangedFields:         []string{"status"},
		Contract:              []byte(`{"contract":"rev7-reuse"}`),
		CreatedAt:             next.UpdatedAt,
	}
	first, err := db.CommitSharedLifecycleEvent(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := db.CommitSharedLifecycleEvent(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if first.Reused || !second.Reused || second.OperationID != first.OperationID || second.Revision != first.Revision {
		t.Fatalf("reuse receipts first=%#v second=%#v", first, second)
	}
	for _, table := range []string{"shared_lifecycle_events", "hub_outbox"} {
		count, err := db.Shared.Query(ctx, `SELECT COUNT(*) FROM `+table+` WHERE entity_id=?`, adr.ID)
		if err != nil || count.Rows[0][0] != int64(1) {
			t.Fatalf("%s rows after reuse=%#v err=%v", table, count.Rows, err)
		}
	}
}

func TestTSK409Rev7LifecycleHistoryMergesEventsAndPaginates(t *testing.T) {
	ctx := context.Background()
	db := openRev7Databases(t)
	now := time.Date(2026, 9, 11, 16, 0, 0, 0, time.UTC)
	adr := adrRev7Fixture("EXM-ADR8", model.ADRStatusProposed, 1, now)
	insertRev7ADR(t, db, adr, now)
	for revision := 2; revision <= 3; revision++ {
		payload := adr
		payload.Revision = revision
		payload.RevisionCount = revision
		payload.Decision = "decision " + time.Duration(revision).String()
		recordedAt := now.Add(time.Duration(revision) * time.Minute).Format(time.RFC3339Nano)
		if _, err := db.Shared.Exec(ctx, `INSERT INTO shared_entity_revisions(entity_type,entity_id,project_id,revision,mutation_kind,actor,reason,changed_fields,payload,recorded_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, "adr", adr.ID, adr.ProjectID, revision, "update", "planner", "update", []byte(`["decision"]`), mustJSON(t, payload), recordedAt); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Shared.Exec(ctx, `UPDATE shared_adrs SET revision=?,payload=?,updated_at=? WHERE id=?`, revision, mustJSON(t, payload), recordedAt, adr.ID); err != nil {
			t.Fatal(err)
		}
	}
	for _, event := range []struct {
		revision   int64
		from, to   string
		recordedAt time.Time
	}{{1, model.ADRStatusProposed, model.ADRStatusAccepted, now.Add(time.Minute)}, {2, model.ADRStatusAccepted, model.ADRStatusSuperseded, now.Add(2 * time.Minute)}} {
		if _, err := db.Shared.Exec(ctx, `INSERT INTO shared_lifecycle_events(operation_id,entity_type,project_id,entity_id,revision,event_kind,from_status,to_status,actor,reason,contract,recorded_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, "rev7-event-"+time.Duration(event.revision).String(), "adr", adr.ProjectID, adr.ID, event.revision, SharedLifecycleEventKindStatus, event.from, event.to, "planner", "transition", []byte(`{"contract":true}`), event.recordedAt.Format(time.RFC3339Nano)); err != nil {
			t.Fatal(err)
		}
	}
	page, err := db.ListSharedLifecycleHistoryPage(ctx, "adr", adr.ProjectID, adr.ID, SharedLifecycleHistoryCursor{}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if !page.HasMore || page.NextCursor == "" || len(page.Records) != 3 {
		t.Fatalf("first page=%#v", page)
	}
	if page.Records[0].Revision != 1 || page.Records[0].MutationKind != "create" || page.Records[1].Revision != 1 || page.Records[1].MutationKind != SharedLifecycleEventKindStatus {
		t.Fatalf("first page ordering=%#v", page.Records)
	}
	cursor, err := DecodeSharedLifecycleHistoryCursor(page.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	second, err := db.ListSharedLifecycleHistoryPage(ctx, "adr", adr.ProjectID, adr.ID, cursor, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Records) != 2 {
		t.Fatalf("second page=%#v", second.Records)
	}
	seen := map[string]int{}
	for _, record := range append(append([]SharedRevisionRecord(nil), page.Records...), second.Records...) {
		seen[record.MutationKind+"@"+time.Duration(record.Revision).String()]++
	}
	for key, count := range seen {
		if count != 1 {
			t.Fatalf("merged history duplicated %s: %#v", key, seen)
		}
	}
	if len(seen) != 5 {
		t.Fatalf("merged history entries=%#v", seen)
	}
}

func TestTSK409Rev7ADRSummaryMigrationMetadataIsExact(t *testing.T) {
	ctx := context.Background()
	db := openRev7RawBaseline(t)
	oldTime := time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC)
	legacy := adrRev7Fixture("EXM-ADR11", model.ADRStatusAccepted, 1, oldTime)
	legacy.Summary = ""
	oldPayload := mustJSON(t, legacy)
	if _, err := db.Exec(ctx, `INSERT INTO shared_adrs(id,revision,payload,updated_at) VALUES(?,?,?,?)`, legacy.ID, 1, oldPayload, oldTime.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	migration, err := sharedADRSummaryMigration(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if err := migrate.Apply(ctx, db, []migrate.Migration{migration}, migrate.Options{}); err != nil {
		t.Fatal(err)
	}
	history, err := db.Query(ctx, `SELECT revision,mutation_kind,actor,reason,changed_fields,payload,recorded_at FROM shared_entity_revisions WHERE entity_type='adr' AND entity_id=? ORDER BY revision`, legacy.ID)
	if err != nil || len(history.Rows) != 2 {
		t.Fatalf("migration history=%#v err=%v", history.Rows, err)
	}
	preserved := history.Rows[0]
	if preserved[0] != int64(1) || preserved[1] != "migration" || preserved[2] != "system:adr-summary-migration" ||
		preserved[3] != "preserve pre-summary ADR payload" || string(preserved[4].([]byte)) != `["legacy"]` ||
		string(preserved[5].([]byte)) != string(oldPayload) || preserved[6] != oldTime.Format(time.RFC3339Nano) {
		t.Fatalf("preserved legacy evidence=%#v", preserved)
	}
	added := history.Rows[1]
	if added[0] != int64(2) || added[1] != "migration" || added[2] != "system:adr-summary-migration" ||
		added[3] != "add ADR summary" || string(added[4].([]byte)) != `["summary"]` {
		t.Fatalf("summary evidence=%#v", added)
	}
	recordedAt, ok := added[6].(string)
	if !ok {
		t.Fatalf("summary recorded_at=%#v", added[6])
	}
	parsed, err := time.Parse(time.RFC3339Nano, recordedAt)
	if err != nil || parsed.Location() != time.UTC || parsed.Format(time.RFC3339Nano) != recordedAt {
		t.Fatalf("summary recorded_at is not canonical UTC: %q err=%v", recordedAt, err)
	}
	state, err := db.Query(ctx, `SELECT revision,payload,updated_at FROM shared_adrs WHERE id=?`, legacy.ID)
	if err != nil || len(state.Rows) != 1 || state.Rows[0][0] != int64(2) || state.Rows[0][2] != recordedAt {
		t.Fatalf("migrated state=%#v err=%v", state.Rows, err)
	}
	if string(state.Rows[0][1].([]byte)) != string(added[5].([]byte)) {
		t.Fatal("migrated state payload differs from the summary history revision")
	}
	var migrated model.ADR
	if err := json.Unmarshal(state.Rows[0][1].([]byte), &migrated); err != nil {
		t.Fatal(err)
	}
	if migrated.Revision != 2 || migrated.RevisionCount != 2 || migrated.UpdatedBy != "system:adr-summary-migration" ||
		migrated.LastReason != "add ADR summary" || migrated.Summary != legacy.Title {
		t.Fatalf("migrated payload=%#v", migrated)
	}
	outbox, err := db.Query(ctx, `SELECT id,entity_type,entity_id,revision,kind,payload,created_at FROM hub_outbox WHERE entity_id=?`, legacy.ID)
	if err != nil || len(outbox.Rows) != 1 {
		t.Fatalf("migration outbox=%#v err=%v", outbox.Rows, err)
	}
	entry := outbox.Rows[0]
	if entry[0] != "adr-summary-migration-EXM-ADR11-r2" || entry[1] != "adr" || entry[2] != legacy.ID ||
		entry[3] != int64(2) || entry[4] != "adr-summary-migration" || entry[6] != recordedAt ||
		string(entry[5].([]byte)) != string(state.Rows[0][1].([]byte)) {
		t.Fatalf("migration outbox entry=%#v", entry)
	}
}

func TestTSK409Rev7ADRSummaryMigrationBoundsInventory(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 11, 9, 30, 0, 0, time.UTC)
	legacy := adrRev7Fixture("EXM-ADR12", model.ADRStatusAccepted, 1, now)
	legacy.Summary = ""
	seed := func(db *upstream.Store, count int) {
		for index := 0; index < count; index++ {
			row := legacy
			row.ID = fmt.Sprintf("%s%05d", legacy.ID, index)
			if _, err := db.Exec(ctx, `INSERT INTO shared_adrs(id,revision,payload,updated_at) VALUES(?,?,?,?)`, row.ID, 1, mustJSON(t, row), now.Format(time.RFC3339Nano)); err != nil {
				t.Fatal(err)
			}
		}
	}
	bounded := openRev7RawBaseline(t)
	seed(bounded, sharedADRSummaryMigrationMaxRows)
	migration, err := sharedADRSummaryMigration(ctx, bounded)
	if err != nil {
		t.Fatalf("inventory at the bounded maximum was rejected: %v", err)
	}
	if len(migration.Statements) <= 1 {
		t.Fatalf("inventory at the bounded maximum produced no migration statements: %d", len(migration.Statements))
	}
	over := openRev7RawBaseline(t)
	seed(over, sharedADRSummaryMigrationMaxRows+1)
	if _, err := sharedADRSummaryMigration(ctx, over); err == nil || !strings.Contains(err.Error(), "exceeds bounded migration maximum") {
		t.Fatalf("inventory above the bounded maximum was not rejected by the bound: %v", err)
	}
	oversized := openRev7RawBaseline(t)
	seed(oversized, sharedADRSummaryMigrationMaxRows+8)
	if _, err := sharedADRSummaryMigration(ctx, oversized); err == nil || !strings.Contains(err.Error(), "exceeds bounded migration maximum") {
		t.Fatalf("oversized inventory was not rejected by the bound: %v", err)
	}
}

func TestTSK409Rev7LifecycleEventCorruptTransitionFailsClosed(t *testing.T) {
	ctx := context.Background()
	db := openRev7Databases(t)
	now := time.Date(2026, 9, 11, 19, 0, 0, 0, time.UTC)
	corrupt := adrRev7Fixture("EXM-ADR13", "bogus", 1, now)
	corruptPayload := mustJSON(t, corrupt)
	if _, err := db.Shared.Exec(ctx, `INSERT INTO shared_adrs(id,revision,payload,updated_at) VALUES(?,?,?,?)`, corrupt.ID, 1, corruptPayload, now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	next := corrupt
	next.Status = model.ADRStatusAccepted
	next.UpdatedAt = now.Add(time.Minute)
	request := SharedLifecycleEventRequest{
		OperationID:           "adr-update-rev7-corrupt",
		EntityType:            "adr",
		ProjectID:             corrupt.ProjectID,
		EntityID:              corrupt.ID,
		ExpectedRevision:      1,
		ExpectedStoreRevision: 1,
		ExpectedPayload:       corruptPayload,
		Revision:              1,
		Kind:                  "adr-update",
		EventKind:             SharedLifecycleEventKindStatus,
		FromStatus:            "bogus",
		ToStatus:              model.ADRStatusAccepted,
		Payload:               mustJSON(t, next),
		Actor:                 "planner",
		Reason:                "corrupt transition probe",
		ChangedFields:         []string{"status"},
		Contract:              []byte(`{"contract":"rev7-corrupt"}`),
		CreatedAt:             next.UpdatedAt,
	}
	if _, err := db.CommitSharedLifecycleEvent(ctx, request); err == nil {
		t.Fatal("corrupt stored status was accepted by the lifecycle seam")
	}
	disallowed := adrRev7Fixture("EXM-ADR14", model.ADRStatusSuperseded, 1, now)
	disallowedPayload := mustJSON(t, disallowed)
	if _, err := db.Shared.Exec(ctx, `INSERT INTO shared_adrs(id,revision,payload,updated_at) VALUES(?,?,?,?)`, disallowed.ID, 1, disallowedPayload, now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	disallowedNext := disallowed
	disallowedNext.Status = model.ADRStatusProposed
	disallowedNext.UpdatedAt = now.Add(time.Minute)
	disallowedRequest := request
	disallowedRequest.OperationID = "adr-update-rev7-disallowed"
	disallowedRequest.EntityID = disallowed.ID
	disallowedRequest.ExpectedPayload = disallowedPayload
	disallowedRequest.FromStatus = model.ADRStatusSuperseded
	disallowedRequest.ToStatus = model.ADRStatusProposed
	disallowedRequest.Payload = mustJSON(t, disallowedNext)
	if _, err := db.CommitSharedLifecycleEvent(ctx, disallowedRequest); err == nil {
		t.Fatal("unregistered status transition was accepted by the lifecycle seam")
	}
	for _, id := range []string{corrupt.ID, disallowed.ID} {
		state, err := db.Shared.Query(ctx, `SELECT revision,payload FROM shared_adrs WHERE id=?`, id)
		if err != nil || len(state.Rows) != 1 || state.Rows[0][0] != int64(1) {
			t.Fatalf("state after rejected transition %s=%#v err=%v", id, state.Rows, err)
		}
		for _, table := range []string{"shared_lifecycle_events", "hub_outbox"} {
			count, err := db.Shared.Query(ctx, `SELECT COUNT(*) FROM `+table+` WHERE entity_id=?`, id)
			if err != nil || count.Rows[0][0] != int64(0) {
				t.Fatalf("%s rows after rejected transition %s=%#v err=%v", table, id, count.Rows, err)
			}
		}
	}
}

func TestTSK409Rev7LifecycleEventCorruptRowFailsClosedOnRead(t *testing.T) {
	ctx := context.Background()
	db := openRev7Databases(t)
	now := time.Date(2026, 9, 11, 20, 0, 0, 0, time.UTC)
	adr := adrRev7Fixture("EXM-ADR15", model.ADRStatusProposed, 1, now)
	previous := insertRev7ADR(t, db, adr, now)
	next := adr
	next.Status = model.ADRStatusAccepted
	next.UpdatedAt = now.Add(time.Minute)
	request := SharedLifecycleEventRequest{
		OperationID:           "adr-update-rev7-read-corrupt",
		EntityType:            "adr",
		ProjectID:             adr.ProjectID,
		EntityID:              adr.ID,
		ExpectedRevision:      1,
		ExpectedStoreRevision: 1,
		ExpectedPayload:       previous,
		Revision:              1,
		Kind:                  "adr-update",
		EventKind:             SharedLifecycleEventKindStatus,
		FromStatus:            model.ADRStatusProposed,
		ToStatus:              model.ADRStatusAccepted,
		Payload:               mustJSON(t, next),
		Actor:                 "planner",
		Reason:                "read corrupt probe",
		ChangedFields:         []string{"status"},
		Contract:              []byte(`{"contract":"rev7-read-corrupt"}`),
		CreatedAt:             next.UpdatedAt,
	}
	if _, err := db.CommitSharedLifecycleEvent(ctx, request); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ListSharedLifecycleEvents(ctx, "adr", adr.ProjectID, adr.ID, 10); err != nil {
		t.Fatalf("healthy event read failed: %v", err)
	}
	if _, err := db.Shared.Exec(ctx, `UPDATE shared_lifecycle_events SET from_status=?,to_status=? WHERE entity_id=?`, model.ADRStatusSuperseded, model.ADRStatusProposed, adr.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ListSharedLifecycleEvents(ctx, "adr", adr.ProjectID, adr.ID, 10); err == nil {
		t.Fatal("corrupt unregistered lifecycle transition was accepted by the event reader")
	}
	if _, err := db.ListSharedLifecycleHistoryPage(ctx, "adr", adr.ProjectID, adr.ID, SharedLifecycleHistoryCursor{}, SharedLifecycleQueryMaxRows); err == nil {
		t.Fatal("corrupt unregistered lifecycle transition was accepted by the history reader")
	}
	if _, err := db.Shared.Exec(ctx, `UPDATE shared_lifecycle_events SET from_status=?,to_status=? WHERE entity_id=?`, model.ADRStatusProposed, model.ADRStatusProposed, adr.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ListSharedLifecycleEvents(ctx, "adr", adr.ProjectID, adr.ID, 10); err == nil {
		t.Fatal("degenerate lifecycle transition was accepted by the event reader")
	}
	if _, err := db.Shared.Exec(ctx, `UPDATE shared_lifecycle_events SET from_status=?,to_status=? WHERE entity_id=?`, "bogus", model.ADRStatusAccepted, adr.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ListSharedLifecycleEvents(ctx, "adr", adr.ProjectID, adr.ID, 10); err == nil {
		t.Fatal("corrupt lifecycle status was accepted by the event reader")
	}
}

func TestTSK409Rev7LifecycleEventSeamIsEntityNeutralReuse(t *testing.T) {
	ctx := context.Background()
	db := openRev7Databases(t)
	for _, table := range []string{"shared_adrs", "shared_entity_revisions", "shared_lifecycle_events", "hub_outbox"} {
		rows, err := db.Shared.Query(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table)
		if err != nil || len(rows.Rows) != 1 {
			t.Fatalf("shared table %s missing: %#v err=%v", table, rows.Rows, err)
		}
	}
	for _, table := range []string{"shared_adr_lifecycle_events", "shared_adr_revisions", "adr_outbox"} {
		rows, err := db.Shared.Query(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table)
		if err != nil || len(rows.Rows) != 0 {
			t.Fatalf("ADR-specific table %s exists: %#v err=%v", table, rows.Rows, err)
		}
	}
	now := time.Date(2026, 9, 11, 18, 0, 0, 0, time.UTC)
	adr := adrRev7Fixture("EXM-ADR10", model.ADRStatusProposed, 1, now)
	previous := insertRev7ADR(t, db, adr, now)
	next := adr
	next.Status = model.ADRStatusAccepted
	next.UpdatedAt = now.Add(time.Minute)
	request := SharedLifecycleEventRequest{
		OperationID:           "adr-update-rev7-neutral",
		EntityType:            "adr",
		ProjectID:             adr.ProjectID,
		EntityID:              adr.ID,
		ExpectedRevision:      1,
		ExpectedStoreRevision: 1,
		ExpectedPayload:       previous,
		Revision:              1,
		Kind:                  "adr-update",
		EventKind:             SharedLifecycleEventKindStatus,
		FromStatus:            model.ADRStatusProposed,
		ToStatus:              model.ADRStatusAccepted,
		Payload:               mustJSON(t, next),
		Actor:                 "planner",
		Reason:                "neutral seam probe",
		ChangedFields:         []string{"status"},
		Contract:              []byte(`{"contract":"rev7-neutral"}`),
		CreatedAt:             next.UpdatedAt,
	}
	if _, err := db.CommitSharedLifecycleEvent(ctx, request); err != nil {
		t.Fatal(err)
	}
	stateless := request
	stateless.EntityType = "rule"
	stateless.OperationID = "rule-update-rev7-neutral"
	if _, err := db.CommitSharedLifecycleEvent(ctx, stateless); err == nil {
		t.Fatal("statusless entity type was accepted by the lifecycle seam")
	}
	if _, err := db.ListSharedLifecycleEvents(ctx, "rule", adr.ProjectID, adr.ID, 10); err == nil {
		t.Fatal("statusless entity type was accepted by the lifecycle event reader")
	}
	events, err := db.ListSharedLifecycleEvents(ctx, "adr", adr.ProjectID, adr.ID, 10)
	if err != nil || len(events) != 1 || events[0].EntityType != "adr" {
		t.Fatalf("entity-neutral events=%#v err=%v", events, err)
	}
}

func TestTSK409Rev7StateEventOutboxSurviveRestart(t *testing.T) {
	state := t.TempDir()
	ctx := context.Background()
	now := time.Now().UTC()
	db, err := Open(state)
	if err != nil {
		t.Fatal(err)
	}
	adr := adrRev7Fixture("EXM-ADR9", model.ADRStatusProposed, 1, now)
	previous := mustJSON(t, adr)
	if _, err := db.Shared.Exec(ctx, `INSERT INTO shared_adrs(id,revision,payload,updated_at) VALUES(?,?,?,?)`, adr.ID, 1, previous, now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Shared.Exec(ctx, `INSERT INTO shared_entity_revisions(entity_type,entity_id,project_id,revision,mutation_kind,actor,reason,changed_fields,payload,recorded_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, "adr", adr.ID, adr.ProjectID, 1, "create", "planner", "create", []byte(`["title"]`), previous, now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	next := adr
	next.Status = model.ADRStatusArchived
	next.ArchivedAt = &now
	next.ArchivedBy = "planner"
	next.ArchiveReason = "restart probe"
	next.LastReason = "restart probe"
	next.UpdatedAt = now
	if _, err := db.CommitSharedLifecycleEvent(ctx, SharedLifecycleEventRequest{
		OperationID:           "adr-archive-rev7-restart",
		EntityType:            "adr",
		ProjectID:             adr.ProjectID,
		EntityID:              adr.ID,
		ExpectedRevision:      1,
		ExpectedStoreRevision: 1,
		ExpectedPayload:       previous,
		Revision:              1,
		Kind:                  "adr-archive",
		EventKind:             SharedLifecycleEventKindArchive,
		FromStatus:            model.ADRStatusProposed,
		ToStatus:              model.ADRStatusArchived,
		Payload:               mustJSON(t, next),
		Actor:                 "planner",
		Reason:                next.LastReason,
		ChangedFields:         []string{"status", "archived_at", "archived_by", "archive_reason"},
		Contract:              []byte(`{"contract":"rev7-restart"}`),
		CreatedAt:             now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(state)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	events, err := reopened.ListSharedLifecycleEvents(ctx, "adr", adr.ProjectID, adr.ID, SharedLifecycleQueryMaxRows)
	if err != nil || len(events) != 1 || events[0].EventKind != SharedLifecycleEventKindArchive || events[0].Revision != 1 {
		t.Fatalf("events after restart=%#v err=%v", events, err)
	}
	page, err := reopened.ListSharedLifecycleHistoryPage(ctx, "adr", adr.ProjectID, adr.ID, SharedLifecycleHistoryCursor{}, SharedLifecycleQueryMaxRows)
	if err != nil || len(page.Records) != 2 {
		t.Fatalf("history after restart=%#v err=%v", page.Records, err)
	}
	outbox, err := reopened.PendingOutbox(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range outbox {
		if entry.ID == "adr-archive-rev7-restart" {
			found = true
			if entry.Revision != 1 || entry.Kind != "adr-archive" {
				t.Fatalf("restart outbox entry=%#v", entry)
			}
		}
	}
	if !found {
		t.Fatal("restart outbox entry is missing")
	}
}
