package sqlitestore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/rceman/go-sqlite-store/migrate"
	upstream "github.com/rceman/go-sqlite-store/store"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func tsk531ReleasedChainThroughLifecycleEvents() []migrate.Migration {
	return []migrate.Migration{
		sharedBaselineMigration(),
		migrate.Migration{Version: sharedTaskSummaryMigrationVersion, Name: sharedTaskSummaryMigrationName, Statements: []upstream.Statement{{SQL: "SELECT 1"}}},
		migrate.Migration{Version: sharedTaskSequenceMigrationVersion, Name: sharedTaskSequenceMigrationName, Statements: []upstream.Statement{{SQL: "SELECT 1"}}},
		sharedTaskExecutionMigration(),
		sharedTaskExecutionPhasesMigration(),
		sharedTaskExecutionVerificationMigration(),
		sharedTaskLifecycleMigration(),
		sharedLifecycleEventMigration(),
	}
}

func tsk531PreTSK409Chain() []migrate.Migration {
	return []migrate.Migration{
		sharedBaselineMigration(),
		migrate.Migration{Version: sharedTaskSummaryMigrationVersion, Name: sharedTaskSummaryMigrationName, Statements: []upstream.Statement{{SQL: "SELECT 1"}}},
		migrate.Migration{Version: sharedTaskSequenceMigrationVersion, Name: sharedTaskSequenceMigrationName, Statements: []upstream.Statement{{SQL: "SELECT 1"}}},
		sharedTaskExecutionMigration(),
		sharedTaskExecutionPhasesMigration(),
		sharedTaskExecutionVerificationMigration(),
		sharedTaskLifecycleMigration(),
	}
}

func openTSK531RawPreCutStore(t *testing.T) *upstream.Store {
	t.Helper()
	db, err := upstream.Open(upstream.Config{Path: t.TempDir() + "/shared.db"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := migrate.Apply(context.Background(), db, tsk531ReleasedChainThroughLifecycleEvents(), migrate.Options{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func insertTSK531TaskState(t *testing.T, db *upstream.Store, taskID, status string, revision int64, updatedAt time.Time) {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"id": taskID, "project_id": "example", "revision": revision, "status": status})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(context.Background(), `INSERT INTO shared_tasks(id,revision,payload,updated_at) VALUES(?,?,?,?)`, taskID, revision, payload, updatedAt.UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
}

func insertTSK531RetainedTaskLifecycleEvent(t *testing.T, db *upstream.Store, id int64, operationID, taskID, kind, from, to string, revision int64, recordedAt time.Time) {
	t.Helper()
	if _, err := db.Exec(context.Background(), `INSERT INTO shared_task_lifecycle_events(id,operation_id,project_id,task_id,revision,event_kind,from_status,to_status,actor,reason,contract,recorded_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		id, operationID, "example", taskID, revision, kind, from, to, "planner", "retained", []byte(`{"schema_version":1,"retained":true}`), recordedAt.UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
}

func insertTSK531ADRState(t *testing.T, db *upstream.Store, adrID, status string, revision int64, updatedAt time.Time) {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"id": adrID, "project_id": "example", "revision": revision, "status": status})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(context.Background(), `INSERT INTO shared_adrs(id,revision,payload,updated_at) VALUES(?,?,?,?)`, adrID, revision, payload, updatedAt.UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
}

func insertTSK531SharedLifecycleEvent(t *testing.T, db *upstream.Store, operationID, entityType, entityID, eventKind, from, to string, revision int64, recordedAt time.Time) {
	t.Helper()
	if _, err := db.Exec(context.Background(), `INSERT INTO shared_lifecycle_events(operation_id,entity_type,project_id,entity_id,revision,event_kind,from_status,to_status,actor,reason,contract,recorded_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		operationID, entityType, "example", entityID, revision, eventKind, from, to, "planner", "retained", []byte(`{"schema_version":1}`), recordedAt.UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
}

func tsk531SharedLifecycleEventRow(t *testing.T, db *upstream.Store, entityID string) []any {
	t.Helper()
	rows, err := db.Query(context.Background(), `SELECT mutation_kind,changed_fields FROM shared_lifecycle_events WHERE entity_id=?`, entityID)
	if err != nil || len(rows.Rows) != 1 {
		t.Fatalf("shared lifecycle row=%#v err=%v", rows.Rows, err)
	}
	return rows.Rows[0]
}

func TestTSK531TaskLifecycleHardCutCopiesFieldExactlyAndCuts(t *testing.T) {
	ctx := context.Background()
	db := openTSK531RawPreCutStore(t)
	completeAt := time.Date(2026, 9, 12, 9, 0, 0, 123456789, time.UTC)
	archiveAt := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
	insertTSK531TaskState(t, db, "EXM-TSK1", model.TaskAuthoringArchived, 2, archiveAt)
	insertTSK531RetainedTaskLifecycleEvent(t, db, 1, "task-complete-retained", "EXM-TSK1", "complete", "planned", "done", 1, completeAt)
	insertTSK531RetainedTaskLifecycleEvent(t, db, 2, "task-archive-retained", "EXM-TSK1", "archive", "done", "archived", 2, archiveAt)
	migration, err := sharedTaskLifecycleHardCutMigration(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if err := migrate.Apply(ctx, db, []migrate.Migration{migration}, migrate.Options{}); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query(ctx, `SELECT operation_id,entity_type,project_id,entity_id,revision,event_kind,from_status,to_status,actor,reason,contract,recorded_at,mutation_kind,changed_fields FROM shared_lifecycle_events ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	want := [][]any{
		{"task-complete-retained", "task", "example", "EXM-TSK1", int64(1), SharedLifecycleEventKindStatus, "planned", "done", "planner", "retained", []byte(`{"schema_version":1,"retained":true}`), completeAt.Format(time.RFC3339Nano), "complete", []byte(`["status"]`)},
		{"task-archive-retained", "task", "example", "EXM-TSK1", int64(2), SharedLifecycleEventKindArchive, "done", "archived", "planner", "retained", []byte(`{"schema_version":1,"retained":true}`), archiveAt.Format(time.RFC3339Nano), "archive", []byte(`["status"]`)},
	}
	if len(rows.Rows) != len(want) {
		t.Fatalf("copied rows=%#v", rows.Rows)
	}
	for i, row := range want {
		if len(rows.Rows[i]) != len(row) {
			t.Fatalf("copied row %d shape=%#v", i, rows.Rows[i])
		}
		for j := range row {
			left, right := rows.Rows[i][j], row[j]
			if lb, ok := left.([]byte); ok {
				rb, _ := right.([]byte)
				if !bytes.Equal(lb, rb) {
					t.Fatalf("copied row %d field %d=%q want %q", i, j, lb, rb)
				}
				continue
			}
			if left != right {
				t.Fatalf("copied row %d field %d=%#v want %#v", i, j, left, right)
			}
		}
	}
	gone, err := db.Query(ctx, `SELECT name FROM sqlite_master WHERE name='shared_task_lifecycle_events'`)
	if err != nil || len(gone.Rows) != 0 {
		t.Fatalf("old task lifecycle table survived the hard cut: rows=%#v err=%v", gone.Rows, err)
	}
	events, err := (&Databases{Shared: db}).ListSharedLifecycleEvents(ctx, "task", "example", "EXM-TSK1", 64)
	if err != nil || len(events) != 2 || events[0].EventKind != SharedLifecycleEventKindStatus || events[1].ToStatus != "archived" {
		t.Fatalf("shared read after hard cut=%#v err=%v", events, err)
	}
	if events[0].MutationKind != "complete" || events[1].MutationKind != "archive" || !containsString(events[0].ChangedFields, "status") {
		t.Fatalf("shared mutation kinds after hard cut=%#v", events)
	}
	page, err := (&Databases{Shared: db}).ListSharedLifecycleHistoryPage(ctx, "task", "example", "EXM-TSK1", SharedLifecycleHistoryCursor{}, 64)
	if err != nil || len(page.Records) != 2 {
		t.Fatalf("shared history after hard cut=%#v err=%v", page, err)
	}
	if page.Records[0].MutationKind != "complete" || page.Records[1].MutationKind != "archive" ||
		!containsString(page.Records[0].ChangedFields, "status") || containsString(page.Records[0].ChangedFields, "archived_at") {
		t.Fatalf("history projected fields from the event kind: %#v", page.Records)
	}
	markers, err := db.Query(ctx, `SELECT version,name FROM schema_migrations WHERE version=?`, sharedTaskLifecycleHardCutMigrationVersion)
	if err != nil || len(markers.Rows) != 1 || markers.Rows[0][1] != sharedTaskLifecycleHardCutMigrationName {
		t.Fatalf("hard cut marker=%#v err=%v", markers.Rows, err)
	}
}

func TestTSK531TaskLifecycleHardCutBackfillsExistingADREvents(t *testing.T) {
	ctx := context.Background()
	db := openTSK531RawPreCutStore(t)
	statusAt := time.Date(2026, 9, 12, 8, 0, 0, 0, time.UTC)
	archiveAt := time.Date(2026, 9, 12, 8, 30, 0, 0, time.UTC)
	insertTSK531ADRState(t, db, "EXM-ADR1", "accepted", 1, statusAt)
	insertTSK531ADRState(t, db, "EXM-ADR2", "archived", 2, archiveAt)
	insertTSK531SharedLifecycleEvent(t, db, "adr-status-retained", "adr", "EXM-ADR1", SharedLifecycleEventKindStatus, "proposed", "accepted", 1, statusAt)
	insertTSK531SharedLifecycleEvent(t, db, "adr-archive-retained", "adr", "EXM-ADR2", SharedLifecycleEventKindArchive, "accepted", "archived", 2, archiveAt)
	migration, err := sharedTaskLifecycleHardCutMigration(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if err := migrate.Apply(ctx, db, []migrate.Migration{migration}, migrate.Options{}); err != nil {
		t.Fatal(err)
	}
	status := tsk531SharedLifecycleEventRow(t, db, "EXM-ADR1")
	if status[0] != "status" || string(status[1].([]byte)) != `["status"]` {
		t.Fatalf("backfilled ADR status event=%#v", status)
	}
	archived := tsk531SharedLifecycleEventRow(t, db, "EXM-ADR2")
	if archived[0] != "archive" || string(archived[1].([]byte)) != `["status","archived_at","archived_by","archive_reason"]` {
		t.Fatalf("backfilled ADR archive event=%#v", archived)
	}
	events, err := (&Databases{Shared: db}).ListSharedLifecycleEvents(ctx, "adr", "example", "EXM-ADR1", 64)
	if err != nil || len(events) != 1 || events[0].MutationKind != "status" || !containsString(events[0].ChangedFields, "status") {
		t.Fatalf("backfilled ADR read=%#v err=%v", events, err)
	}
}

func TestTSK531TaskLifecycleHardCutPreservesLegacyKindsAndReadyFields(t *testing.T) {
	ctx := context.Background()
	db := openTSK531RawPreCutStore(t)
	at := time.Date(2026, 9, 12, 9, 0, 0, 0, time.UTC)
	insertTSK531TaskState(t, db, "EXM-TSK1", model.TaskAuthoringDone, 1, at)
	insertTSK531TaskState(t, db, "EXM-TSK2", model.TaskAuthoringDone, 1, at)
	insertTSK531TaskState(t, db, "EXM-TSK3", model.TaskAuthoringArchived, 1, at)
	insertTSK531TaskState(t, db, "EXM-TSK4", model.TaskAuthoringArchived, 1, at)
	insertTSK531RetainedTaskLifecycleEvent(t, db, 1, "legacy-complete-planned", "EXM-TSK1", "complete", "planned", "done", 1, at)
	insertTSK531RetainedTaskLifecycleEvent(t, db, 2, "legacy-complete-ready", "EXM-TSK2", "complete", "ready", "done", 1, at)
	insertTSK531RetainedTaskLifecycleEvent(t, db, 3, "legacy-archive-planned", "EXM-TSK3", "archive", "planned", "archived", 1, at)
	insertTSK531RetainedTaskLifecycleEvent(t, db, 4, "legacy-archive-ready", "EXM-TSK4", "archive", "ready", "archived", 1, at)
	migration, err := sharedTaskLifecycleHardCutMigration(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if err := migrate.Apply(ctx, db, []migrate.Migration{migration}, migrate.Options{}); err != nil {
		t.Fatal(err)
	}
	want := map[string]struct {
		mutationKind string
		fields       string
	}{
		"EXM-TSK1": {"complete", `["status"]`},
		"EXM-TSK2": {"complete", `["status","ready_seal"]`},
		"EXM-TSK3": {"archive", `["status"]`},
		"EXM-TSK4": {"archive", `["status","ready_seal"]`},
	}
	for entityID, expected := range want {
		row := tsk531SharedLifecycleEventRow(t, db, entityID)
		if row[0] != expected.mutationKind || string(row[1].([]byte)) != expected.fields {
			t.Fatalf("%s imported event=%#v want %#v", entityID, row, expected)
		}
	}
	events, err := (&Databases{Shared: db}).ListTaskLifecycleEvents(ctx, "example", "EXM-TSK2", 64)
	if err != nil || len(events) != 1 || events[0].EventKind != TaskLifecycleEventKindComplete || events[0].ToStatus != model.TaskAuthoringDone {
		t.Fatalf("legacy complete kind read=%#v err=%v", events, err)
	}
}

func TestTSK531TaskLifecycleHardCutFailsClosed(t *testing.T) {
	ctx := context.Background()
	recorded := time.Date(2026, 9, 12, 11, 0, 0, 0, time.UTC)
	t.Run("inventory overflow", func(t *testing.T) {
		db := openTSK531RawPreCutStore(t)
		for i := 1; i <= sharedTaskLifecycleHardCutMaxRows+1; i++ {
			insertTSK531RetainedTaskLifecycleEvent(t, db, int64(i), fmt.Sprintf("task-complete-retained-%d", i), fmt.Sprintf("EXM-TSK%03d", i), "complete", "planned", "done", 1, recorded)
		}
		if _, err := sharedTaskLifecycleHardCutMigration(ctx, db); err == nil {
			t.Fatal("inventory above the bounded maximum was accepted")
		}
	})
	t.Run("shared event inventory overflow", func(t *testing.T) {
		db := openTSK531RawPreCutStore(t)
		for i := 1; i <= sharedTaskLifecycleHardCutMaxRows+1; i++ {
			insertTSK531SharedLifecycleEvent(t, db, fmt.Sprintf("adr-status-retained-%d", i), "adr", fmt.Sprintf("EXM-ADR%03d", i), SharedLifecycleEventKindStatus, "proposed", "accepted", 1, recorded)
		}
		if _, err := sharedTaskLifecycleHardCutMigration(ctx, db); err == nil {
			t.Fatal("shared lifecycle inventory above the bounded maximum was accepted")
		}
	})
	t.Run("foreign shared entity", func(t *testing.T) {
		db := openTSK531RawPreCutStore(t)
		insertTSK531SharedLifecycleEvent(t, db, "foreign-status-retained", "rule", "EXM-RUL1", SharedLifecycleEventKindStatus, "proposed", "accepted", 1, recorded)
		if _, err := sharedTaskLifecycleHardCutMigration(ctx, db); err == nil {
			t.Fatal("a retained non-ADR lifecycle event was accepted")
		}
	})
	t.Run("operation conflict", func(t *testing.T) {
		db := openTSK531RawPreCutStore(t)
		insertTSK531TaskState(t, db, "EXM-TSK1", model.TaskAuthoringDone, 1, recorded)
		insertTSK531RetainedTaskLifecycleEvent(t, db, 1, "task-complete-conflict", "EXM-TSK1", "complete", "planned", "done", 1, recorded)
		insertTSK531SharedLifecycleEvent(t, db, "task-complete-conflict", "adr", "EXM-ADR1", SharedLifecycleEventKindStatus, "proposed", "accepted", 1, recorded)
		if _, err := sharedTaskLifecycleHardCutMigration(ctx, db); err == nil {
			t.Fatal("conflicting operation identity was accepted")
		}
	})
	t.Run("duplicate task authority", func(t *testing.T) {
		db := openTSK531RawPreCutStore(t)
		insertTSK531TaskState(t, db, "EXM-TSK1", model.TaskAuthoringDone, 1, recorded)
		insertTSK531RetainedTaskLifecycleEvent(t, db, 1, "task-complete-duplicate", "EXM-TSK1", "complete", "planned", "done", 1, recorded)
		insertTSK531SharedLifecycleEvent(t, db, "task-complete-other", "task", "EXM-TSK1", SharedLifecycleEventKindStatus, "planned", "done", 1, recorded)
		if _, err := sharedTaskLifecycleHardCutMigration(ctx, db); err == nil {
			t.Fatal("duplicate retained task lifecycle authority was accepted")
		}
	})
	t.Run("malformed retained row", func(t *testing.T) {
		db := openTSK531RawPreCutStore(t)
		insertTSK531RetainedTaskLifecycleEvent(t, db, 1, "task-complete-malformed", "EXM-TSK1", "explode", "planned", "done", 1, recorded)
		if _, err := sharedTaskLifecycleHardCutMigration(ctx, db); err == nil {
			t.Fatal("malformed retained kind was accepted")
		}
	})
	t.Run("undecodable transition", func(t *testing.T) {
		db := openTSK531RawPreCutStore(t)
		insertTSK531RetainedTaskLifecycleEvent(t, db, 1, "task-archive-undecodable", "EXM-TSK1", "archive", "planned", "done", 1, recorded)
		if _, err := sharedTaskLifecycleHardCutMigration(ctx, db); err == nil {
			t.Fatal("undecodable retained transition was accepted")
		}
	})
	t.Run("discontinuous chain", func(t *testing.T) {
		db := openTSK531RawPreCutStore(t)
		insertTSK531TaskState(t, db, "EXM-TSK1", model.TaskAuthoringArchived, 1, recorded)
		insertTSK531RetainedTaskLifecycleEvent(t, db, 1, "task-complete-chain", "EXM-TSK1", "complete", "planned", "done", 1, recorded)
		insertTSK531RetainedTaskLifecycleEvent(t, db, 2, "task-archive-chain", "EXM-TSK1", "archive", "planned", "archived", 1, recorded)
		if _, err := sharedTaskLifecycleHardCutMigration(ctx, db); err == nil {
			t.Fatal("an individually valid but discontinuous retained chain was accepted")
		}
	})
	t.Run("final state disagreement", func(t *testing.T) {
		db := openTSK531RawPreCutStore(t)
		insertTSK531TaskState(t, db, "EXM-TSK1", model.TaskAuthoringPlanned, 1, recorded)
		insertTSK531RetainedTaskLifecycleEvent(t, db, 1, "task-complete-state", "EXM-TSK1", "complete", "planned", "done", 1, recorded)
		if _, err := sharedTaskLifecycleHardCutMigration(ctx, db); err == nil {
			t.Fatal("a retained chain that disagrees with the current status was accepted")
		}
	})
	t.Run("missing current state", func(t *testing.T) {
		db := openTSK531RawPreCutStore(t)
		insertTSK531RetainedTaskLifecycleEvent(t, db, 1, "task-complete-stateless", "EXM-TSK1", "complete", "planned", "done", 1, recorded)
		if _, err := sharedTaskLifecycleHardCutMigration(ctx, db); err == nil {
			t.Fatal("a retained chain without current state was accepted")
		}
	})
	t.Run("identity disagreement", func(t *testing.T) {
		db := openTSK531RawPreCutStore(t)
		payload, err := json.Marshal(map[string]any{"id": "EXM-TSK9", "project_id": "example", "revision": 1, "status": model.TaskAuthoringDone})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(ctx, `INSERT INTO shared_tasks(id,revision,payload,updated_at) VALUES('EXM-TSK1',1,?,?)`, payload, recorded.Format(time.RFC3339Nano)); err != nil {
			t.Fatal(err)
		}
		insertTSK531RetainedTaskLifecycleEvent(t, db, 1, "task-complete-identity", "EXM-TSK1", "complete", "planned", "done", 1, recorded)
		if _, err := sharedTaskLifecycleHardCutMigration(ctx, db); err == nil {
			t.Fatal("a retained chain whose current identity disagrees was accepted")
		}
	})
	t.Run("already extended schema", func(t *testing.T) {
		db := openTSK531RawPreCutStore(t)
		if _, err := db.Exec(ctx, `ALTER TABLE shared_lifecycle_events ADD COLUMN mutation_kind TEXT NOT NULL DEFAULT ''`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(ctx, `ALTER TABLE shared_lifecycle_events ADD COLUMN changed_fields BLOB NOT NULL DEFAULT '[]'`); err != nil {
			t.Fatal(err)
		}
		if _, err := sharedTaskLifecycleHardCutMigration(ctx, db); err == nil {
			t.Fatal("an already extended shared lifecycle schema was accepted")
		}
	})
}

func TestTSK531TaskLifecycleHardCutIsSingleAuthorityOnFreshAndReopenedStores(t *testing.T) {
	state := t.TempDir()
	db, err := Open(state)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	gone, err := db.Shared.Query(ctx, `SELECT name FROM sqlite_master WHERE name='shared_task_lifecycle_events'`)
	if err != nil || len(gone.Rows) != 0 {
		t.Fatalf("fresh store retained the old task lifecycle table: rows=%#v err=%v", gone.Rows, err)
	}
	markers, err := db.Shared.Query(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version=? AND name=?`, sharedTaskLifecycleHardCutMigrationVersion, sharedTaskLifecycleHardCutMigrationName)
	if err != nil || markers.Rows[0][0] != int64(1) {
		t.Fatalf("hard cut marker=%#v err=%v", markers.Rows, err)
	}
	columns, err := db.Shared.Query(ctx, `SELECT sql FROM sqlite_master WHERE type='table' AND name='shared_lifecycle_events'`)
	if err != nil || len(columns.Rows) != 1 || !strings.Contains(columns.Rows[0][0].(string), "mutation_kind") || !strings.Contains(columns.Rows[0][0].(string), "changed_fields") {
		t.Fatalf("fresh shared lifecycle schema=%#v err=%v", columns.Rows, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(state)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	again, err := reopened.Shared.Query(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version=?`, sharedTaskLifecycleHardCutMigrationVersion)
	if err != nil || again.Rows[0][0] != int64(1) {
		t.Fatalf("reopen reapplied the hard cut: rows=%#v err=%v", again.Rows, err)
	}
}

func TestTSK531ActualUpgradeFromPreTSK409Store(t *testing.T) {
	state := t.TempDir()
	sharedPath, _ := Paths(state)
	db, err := upstream.Open(upstream.Config{Path: sharedPath})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := migrate.Apply(ctx, db, tsk531PreTSK409Chain(), migrate.Options{}); err != nil {
		t.Fatal(err)
	}
	completeAt := time.Date(2026, 9, 12, 7, 0, 0, 123456789, time.UTC)
	archiveAt := time.Date(2026, 9, 12, 7, 30, 0, 0, time.UTC)
	insertTSK531TaskState(t, db, "EXM-TSK1", model.TaskAuthoringArchived, 2, archiveAt)
	insertTSK531TaskState(t, db, "EXM-TSK2", model.TaskAuthoringDone, 1, completeAt)
	insertTSK531RetainedTaskLifecycleEvent(t, db, 1, "task-complete-upgrade", "EXM-TSK1", "complete", "planned", "done", 1, completeAt)
	insertTSK531RetainedTaskLifecycleEvent(t, db, 2, "task-archive-upgrade", "EXM-TSK1", "archive", "done", "archived", 2, archiveAt)
	insertTSK531RetainedTaskLifecycleEvent(t, db, 3, "task-complete-ready-upgrade", "EXM-TSK2", "complete", "ready", "done", 1, completeAt)
	preCut, err := db.Query(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version>=?`, sharedADRSummaryMigrationVersion)
	if err != nil || preCut.Rows[0][0] != int64(0) {
		t.Fatalf("pre-TSK409 markers=%#v err=%v", preCut.Rows, err)
	}
	absent, err := db.Query(ctx, `SELECT name FROM sqlite_master WHERE name='shared_lifecycle_events'`)
	if err != nil || len(absent.Rows) != 0 {
		t.Fatalf("pre-TSK409 store already had shared lifecycle events: rows=%#v err=%v", absent.Rows, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := Open(state)
	if err != nil {
		t.Fatalf("upgrade from the pre-TSK409 store failed: %v", err)
	}
	defer upgraded.Close()
	gone, err := upgraded.Shared.Query(ctx, `SELECT name FROM sqlite_master WHERE name='shared_task_lifecycle_events'`)
	if err != nil || len(gone.Rows) != 0 {
		t.Fatalf("upgrade retained the old task lifecycle table: rows=%#v err=%v", gone.Rows, err)
	}
	rows, err := upgraded.Shared.Query(ctx, `SELECT entity_id,event_kind,from_status,to_status,mutation_kind,changed_fields FROM shared_lifecycle_events WHERE entity_type='task' ORDER BY id`)
	if err != nil || len(rows.Rows) != 3 {
		t.Fatalf("upgraded task lifecycle rows=%#v err=%v", rows.Rows, err)
	}
	want := [][]any{
		{"EXM-TSK1", SharedLifecycleEventKindStatus, "planned", "done", "complete", []byte(`["status"]`)},
		{"EXM-TSK1", SharedLifecycleEventKindArchive, "done", "archived", "archive", []byte(`["status"]`)},
		{"EXM-TSK2", SharedLifecycleEventKindStatus, "ready", "done", "complete", []byte(`["status","ready_seal"]`)},
	}
	for i, row := range want {
		if len(rows.Rows[i]) != len(row) {
			t.Fatalf("upgraded row %d shape=%#v", i, rows.Rows[i])
		}
		for j := range row {
			left, right := rows.Rows[i][j], row[j]
			if lb, ok := left.([]byte); ok {
				rb, _ := right.([]byte)
				if !bytes.Equal(lb, rb) {
					t.Fatalf("upgraded row %d field %d=%q want %q", i, j, lb, rb)
				}
				continue
			}
			if left != right {
				t.Fatalf("upgraded row %d field %d=%#v want %#v", i, j, left, right)
			}
		}
	}
	history, err := upgraded.ListSharedLifecycleHistoryPage(ctx, "task", "example", "EXM-TSK1", SharedLifecycleHistoryCursor{}, 64)
	if err != nil || len(history.Records) != 2 || history.Records[0].MutationKind != "complete" || history.Records[1].MutationKind != "archive" {
		t.Fatalf("upgraded history=%#v err=%v", history.Records, err)
	}
	markers, err := upgraded.Shared.Query(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version IN (?,?,?)`, sharedADRSummaryMigrationVersion, sharedLifecycleEventMigrationVersion, sharedTaskLifecycleHardCutMigrationVersion)
	if err != nil || markers.Rows[0][0] != int64(3) {
		t.Fatalf("upgrade markers=%#v err=%v", markers.Rows, err)
	}
	if err := upgraded.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(state)
	if err != nil {
		t.Fatalf("reopen after upgrade failed: %v", err)
	}
	defer reopened.Close()
	replay, err := reopened.Shared.Query(ctx, `SELECT COUNT(*) FROM shared_lifecycle_events WHERE entity_type='task'`)
	if err != nil || replay.Rows[0][0] != int64(3) {
		t.Fatalf("reopen duplicated upgraded authority: rows=%#v err=%v", replay.Rows, err)
	}
}

func tsk531ArchiveFixture(t *testing.T) (*Databases, CommitTaskArchiveRequest, model.TaskAuthoring) {
	t.Helper()
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	now := time.Date(2026, 9, 13, 10, 0, 0, 0, time.UTC)
	prev := model.TaskAuthoring{
		SchemaVersion: model.TaskAuthoringSchemaVersion, ID: "EXM-TSK9", ProjectID: "example",
		Type: model.TaskTypeTask, Title: "Archive task", Summary: "Archive summary.", Objective: "Archive safely.",
		ADRRelation: model.TaskADRNoRequired, CreatedBy: "planner",
		Status: model.TaskAuthoringDone, Revision: 2, CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Minute),
	}
	prev.RevisionSHA256, _ = model.HashTaskAuthoring(prev)
	prevPayload := mustJSON(t, prev)
	if _, err := db.Shared.Exec(ctx, `INSERT INTO shared_tasks(id,revision,payload,updated_at) VALUES(?,?,?,?)`, prev.ID, int64(prev.Revision), prevPayload, prev.UpdatedAt.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	final := prev
	final.Status = model.TaskAuthoringArchived
	final.UpdatedAt = now
	finalPayload := mustJSON(t, final)
	contract := mustJSON(t, map[string]any{"schema_version": 1, "task_revision": prev.Revision, "reason": "retire"})
	sum := sha256.Sum256(append([]byte("example\x00"+prev.ID+"\x00"), contract...))
	return db, CommitTaskArchiveRequest{
		OperationID:         "task-archive-" + hex.EncodeToString(sum[:]),
		ProjectID:           "example",
		TaskID:              prev.ID,
		Revision:            int64(prev.Revision),
		PreviousTaskPayload: prevPayload,
		TaskPayload:         finalPayload,
		FromStatus:          model.TaskAuthoringDone,
		Actor:               "planner",
		Reason:              "retire",
		Contract:            contract,
		RecordedAt:          now,
	}, final
}

func TestTSK531TaskArchiveAdoptsSharedLifecycleAuthority(t *testing.T) {
	db, req, final := tsk531ArchiveFixture(t)
	ctx := context.Background()
	before, err := db.Shared.Query(ctx, `SELECT revision,payload FROM shared_tasks WHERE id=?`, req.TaskID)
	if err != nil || len(before.Rows) != 1 {
		t.Fatalf("task row=%#v err=%v", before.Rows, err)
	}
	if err := db.CommitTaskArchive(ctx, req); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Shared.Query(ctx, `SELECT revision,payload FROM shared_tasks WHERE id=?`, req.TaskID)
	if err != nil || len(rows.Rows) != 1 || rows.Rows[0][0] != int64(final.Revision) || string(rows.Rows[0][1].([]byte)) != string(mustJSON(t, final)) {
		t.Fatalf("status-only archive changed the content revision: rows=%#v err=%v", rows.Rows, err)
	}
	events, err := db.Shared.Query(ctx, `SELECT operation_id,entity_type,entity_id,revision,event_kind,from_status,to_status,mutation_kind,changed_fields FROM shared_lifecycle_events WHERE entity_id=?`, req.TaskID)
	if err != nil || len(events.Rows) != 1 {
		t.Fatalf("shared lifecycle rows=%#v err=%v", events.Rows, err)
	}
	if events.Rows[0][1] != "task" || events.Rows[0][4] != SharedLifecycleEventKindArchive || events.Rows[0][5] != "done" || events.Rows[0][6] != "archived" ||
		events.Rows[0][7] != "archive" || string(events.Rows[0][8].([]byte)) != `["status"]` {
		t.Fatalf("shared archive event=%#v", events.Rows[0])
	}
	content, err := db.Shared.Query(ctx, `SELECT COUNT(*) FROM shared_entity_revisions WHERE entity_type='task' AND entity_id=?`, req.TaskID)
	if err != nil || content.Rows[0][0] != int64(0) {
		t.Fatalf("status-only archive wrote content history: rows=%#v err=%v", content.Rows, err)
	}
	outbox, err := db.Shared.Query(ctx, `SELECT COUNT(*) FROM hub_outbox WHERE entity_id=? AND kind='task-archive'`, req.TaskID)
	if err != nil || outbox.Rows[0][0] != int64(1) {
		t.Fatalf("archive outbox=%#v err=%v", outbox.Rows, err)
	}
	history, err := db.ListSharedLifecycleHistoryPage(ctx, "task", "example", req.TaskID, SharedLifecycleHistoryCursor{}, 64)
	if err != nil || len(history.Records) != 1 || history.Records[0].MutationKind != "archive" ||
		!containsString(history.Records[0].ChangedFields, "status") || containsString(history.Records[0].ChangedFields, "archived_at") {
		t.Fatalf("archive history=%#v err=%v", history.Records, err)
	}
	if err := db.CommitTaskArchive(ctx, req); err != nil {
		t.Fatalf("repeated archive must reuse the committed receipt: %v", err)
	}
	repeat, err := db.Shared.Query(ctx, `SELECT COUNT(*) FROM shared_lifecycle_events WHERE entity_id=?`, req.TaskID)
	if err != nil || repeat.Rows[0][0] != int64(1) {
		t.Fatalf("repeated archive duplicated lifecycle authority: rows=%#v err=%v", repeat.Rows, err)
	}
}

func TestTSK531ReadyTaskArchiveRecordsSealTransition(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	now := time.Date(2026, 9, 13, 11, 0, 0, 0, time.UTC)
	prev := model.TaskAuthoring{
		SchemaVersion: model.TaskAuthoringSchemaVersion, ID: "EXM-TSK9", ProjectID: "example",
		Type: model.TaskTypeTask, Title: "Ready task", Summary: "Ready summary.", Objective: "Archive a ready task.",
		ADRRelation: model.TaskADRNoRequired, CreatedBy: "planner",
		Status: model.TaskAuthoringReady, Revision: 2, CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Minute),
	}
	prev.RevisionSHA256, _ = model.HashTaskAuthoring(prev)
	prev.ReadySeal = &model.TaskReadySeal{Revision: prev.Revision, RevisionSHA256: prev.RevisionSHA256, ReadyBy: "planner", ReadyAt: now.Add(-time.Minute)}
	prevPayload := mustJSON(t, prev)
	if _, err := db.Shared.Exec(ctx, `INSERT INTO shared_tasks(id,revision,payload,updated_at) VALUES(?,?,?,?)`, prev.ID, int64(prev.Revision), prevPayload, prev.UpdatedAt.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	final := prev
	final.Status = model.TaskAuthoringArchived
	final.ReadySeal = nil
	final.UpdatedAt = now
	contract := mustJSON(t, map[string]any{"schema_version": 1, "task_revision": prev.Revision, "reason": "retire"})
	sum := sha256.Sum256(append([]byte("example\x00"+prev.ID+"\x00"), contract...))
	req := CommitTaskArchiveRequest{
		OperationID:         "task-archive-" + hex.EncodeToString(sum[:]),
		ProjectID:           "example",
		TaskID:              prev.ID,
		Revision:            int64(prev.Revision),
		PreviousTaskPayload: prevPayload,
		TaskPayload:         mustJSON(t, final),
		FromStatus:          model.TaskAuthoringReady,
		Actor:               "planner",
		Reason:              "retire",
		Contract:            contract,
		RecordedAt:          now,
	}
	if err := db.CommitTaskArchive(ctx, req); err != nil {
		t.Fatal(err)
	}
	row := tsk531SharedLifecycleEventRow(t, db.Shared, prev.ID)
	if row[0] != "archive" || string(row[1].([]byte)) != `["status","ready_seal"]` {
		t.Fatalf("ready archive event=%#v", row)
	}
}

func TestTSK531TaskCompletionAdoptsSharedLifecycleAuthorityAndKeepsExecutionSeparate(t *testing.T) {
	db, req := tsk585CompletionFixture(t)
	ctx := context.Background()
	execution := model.TaskExecutionState{
		ProjectID: req.ProjectID, TaskID: req.TaskID,
		TaskRevision: 1, TaskRevisionSHA256: "d", Status: model.TaskExecutionIntegrated, Stage: "code",
		Worktree: "WT-TSK1-bbbbbbbb", BaseHead: strings.Repeat("a", 40), Head: strings.Repeat("b", 40), Branch: "task/EXM-TSK1-authority", Agent: "gtw-worker", ExecutionRevision: 1, UpdatedAt: req.RecordedAt.Add(-time.Minute),
	}
	var task model.TaskAuthoring
	if err := json.Unmarshal(req.PreviousTaskPayload, &task); err != nil {
		t.Fatal(err)
	}
	execution.TaskRevisionSHA256 = task.RevisionSHA256
	if _, err := db.Shared.Exec(ctx, `INSERT INTO shared_task_execution_states(project_id,task_id,task_revision,task_revision_sha256,status,stage,worktree,base_head_sha,head_sha,branch,agent,execution_revision,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		execution.ProjectID, execution.TaskID, execution.TaskRevision, execution.TaskRevisionSHA256, execution.Status, execution.Stage, execution.Worktree, execution.BaseHead, execution.Head, execution.Branch, execution.Agent, execution.ExecutionRevision, execution.UpdatedAt.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	final := execution
	final.Status = model.TaskExecutionDone
	final.ExecutionRevision++
	final.UpdatedAt = req.RecordedAt
	req.PreviousExecution = &execution
	req.FinalExecution = &final
	if err := db.CommitTaskCompletion(ctx, req); err != nil {
		t.Fatal(err)
	}
	events, err := db.Shared.Query(ctx, `SELECT entity_type,event_kind,from_status,to_status,revision,mutation_kind,changed_fields FROM shared_lifecycle_events WHERE entity_id=?`, req.TaskID)
	if err != nil || len(events.Rows) != 1 || events.Rows[0][0] != "task" || events.Rows[0][1] != SharedLifecycleEventKindStatus || events.Rows[0][4] != int64(1) ||
		events.Rows[0][5] != "complete" || string(events.Rows[0][6].([]byte)) != `["status"]` {
		t.Fatalf("shared completion event=%#v err=%v", events.Rows, err)
	}
	state, found, err := db.ReadTaskExecutionState(ctx, req.ProjectID, req.TaskID)
	if err != nil || !found || state.Status != model.TaskExecutionDone || state.ExecutionRevision != 2 {
		t.Fatalf("execution state=%#v found=%v err=%v", state, found, err)
	}
	if state.Stage != execution.Stage || state.Worktree != execution.Worktree || state.Head != execution.Head || state.Branch != execution.Branch || state.Agent != execution.Agent {
		t.Fatalf("execution orchestration contract drifted: %#v", state)
	}
	lifecycleRows, err := db.Shared.Query(ctx, `SELECT COUNT(*) FROM shared_lifecycle_events WHERE entity_type='task'`)
	if err != nil || lifecycleRows.Rows[0][0] != int64(1) {
		t.Fatalf("lifecycle authority rows=%#v err=%v", lifecycleRows.Rows, err)
	}
	if err := db.CommitTaskCompletion(ctx, req); err != nil {
		t.Fatalf("repeated completion must reuse the committed receipt: %v", err)
	}
	repeated, err := db.Shared.Query(ctx, `SELECT COUNT(*) FROM shared_lifecycle_events WHERE entity_id=?`, req.TaskID)
	if err != nil || repeated.Rows[0][0] != int64(1) {
		t.Fatalf("repeated completion duplicated lifecycle authority: rows=%#v err=%v", repeated.Rows, err)
	}
}

func TestTSK531TaskCompletionExecutionSideEffectRollsBackWithLifecycleEvent(t *testing.T) {
	db, req := tsk585CompletionFixture(t)
	ctx := context.Background()
	var task model.TaskAuthoring
	if err := json.Unmarshal(req.PreviousTaskPayload, &task); err != nil {
		t.Fatal(err)
	}
	execution := model.TaskExecutionState{
		ProjectID: req.ProjectID, TaskID: req.TaskID,
		TaskRevision: 1, TaskRevisionSHA256: task.RevisionSHA256, Status: model.TaskExecutionIntegrated, Stage: "code",
		Worktree: "WT-TSK1-bbbbbbbb", BaseHead: strings.Repeat("a", 40), Head: strings.Repeat("b", 40), Branch: "task/EXM-TSK1-authority", Agent: "gtw-worker", ExecutionRevision: 1, UpdatedAt: req.RecordedAt.Add(-time.Minute),
	}
	if _, err := db.Shared.Exec(ctx, `INSERT INTO shared_task_execution_states(project_id,task_id,task_revision,task_revision_sha256,status,stage,worktree,base_head_sha,head_sha,branch,agent,execution_revision,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		execution.ProjectID, execution.TaskID, execution.TaskRevision, execution.TaskRevisionSHA256, execution.Status, execution.Stage, execution.Worktree, execution.BaseHead, execution.Head, execution.Branch, execution.Agent, execution.ExecutionRevision, execution.UpdatedAt.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	final := execution
	final.Status = model.TaskExecutionDone
	final.ExecutionRevision++
	final.UpdatedAt = req.RecordedAt
	req.PreviousExecution = &execution
	req.FinalExecution = &final
	if _, err := db.Shared.Exec(ctx, `CREATE TRIGGER fail_tsk531_completion_event BEFORE INSERT ON shared_lifecycle_events BEGIN SELECT RAISE(ABORT,'injected lifecycle failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := db.CommitTaskCompletion(ctx, req); err == nil {
		t.Fatal("injected lifecycle failure must fail the completion")
	}
	state, found, err := db.ReadTaskExecutionState(ctx, req.ProjectID, req.TaskID)
	if err != nil || !found || state.Status != model.TaskExecutionIntegrated || state.ExecutionRevision != 1 {
		t.Fatalf("execution state survived a failed lifecycle commit: %#v found=%v err=%v", state, found, err)
	}
	rows, err := db.Shared.Query(ctx, `SELECT payload FROM shared_tasks WHERE id=?`, req.TaskID)
	if err != nil || len(rows.Rows) != 1 {
		t.Fatalf("task row=%#v err=%v", rows.Rows, err)
	}
	var stored model.TaskAuthoring
	if err := json.Unmarshal(rows.Rows[0][0].([]byte), &stored); err != nil || stored.Status != model.TaskAuthoringPlanned {
		t.Fatalf("failed completion mutated the task: %#v err=%v", stored, err)
	}
}

func TestTSK531TaskLifecycleReadsFailClosedOnCorruptSharedRows(t *testing.T) {
	db, req := tsk585CompletionFixture(t)
	ctx := context.Background()
	if err := db.CommitTaskCompletion(ctx, req); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Shared.Exec(ctx, `UPDATE shared_lifecycle_events SET from_status='ready',to_status='planned' WHERE entity_id=?`, req.TaskID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ListTaskLifecycleEvents(ctx, req.ProjectID, req.TaskID, 64); err == nil {
		t.Fatal("corrupt shared lifecycle row must fail the task lifecycle list")
	}
	if _, _, err := db.ReadTaskCompletionEvent(ctx, req.ProjectID, req.TaskID); err == nil {
		t.Fatal("corrupt shared lifecycle row must fail the completion read")
	}
	if _, err := db.ListSharedLifecycleHistoryPage(ctx, "task", req.ProjectID, req.TaskID, SharedLifecycleHistoryCursor{}, 64); err == nil {
		t.Fatal("corrupt shared lifecycle row must fail the shared history page")
	}
}

func tsk531ArchiveRequest(t *testing.T, db *Databases, completion CommitTaskCompletionRequest, taskID string) CommitTaskArchiveRequest {
	t.Helper()
	ctx := context.Background()
	rows, err := db.Shared.Query(ctx, `SELECT revision,payload FROM shared_tasks WHERE id=?`, taskID)
	if err != nil || len(rows.Rows) != 1 {
		t.Fatalf("task row=%#v err=%v", rows.Rows, err)
	}
	revision, ok := rows.Rows[0][0].(int64)
	if !ok {
		t.Fatalf("task revision=%#v", rows.Rows[0])
	}
	prevPayload := rows.Rows[0][1].([]byte)
	var prev model.TaskAuthoring
	if err := json.Unmarshal(prevPayload, &prev); err != nil {
		t.Fatal(err)
	}
	now := completion.RecordedAt.Add(time.Minute)
	final := prev
	final.Status = model.TaskAuthoringArchived
	final.ReadySeal = nil
	final.UpdatedAt = now
	contract := mustJSON(t, map[string]any{"schema_version": 1, "task_revision": prev.Revision, "reason": "retire"})
	sum := sha256.Sum256(append([]byte("example\x00"+taskID+"\x00"), contract...))
	return CommitTaskArchiveRequest{
		OperationID:         "task-archive-" + hex.EncodeToString(sum[:]),
		ProjectID:           "example",
		TaskID:              taskID,
		Revision:            revision,
		PreviousTaskPayload: prevPayload,
		TaskPayload:         mustJSON(t, final),
		FromStatus:          prev.Status,
		Actor:               "planner",
		Reason:              "retire",
		Contract:            contract,
		RecordedAt:          now,
	}
}

func TestTSK531SharedChainValidationFailsClosedOnReads(t *testing.T) {
	ctx := context.Background()
	t.Run("discontinuity", func(t *testing.T) {
		db, req := tsk585CompletionFixture(t)
		if err := db.CommitTaskCompletion(ctx, req); err != nil {
			t.Fatal(err)
		}
		archive := tsk531ArchiveRequest(t, db, req, "EXM-TSK1")
		if err := db.CommitTaskArchive(ctx, archive); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Shared.Exec(ctx, `UPDATE shared_lifecycle_events SET from_status='planned' WHERE operation_id=?`, archive.OperationID); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ListTaskLifecycleEvents(ctx, req.ProjectID, req.TaskID, 64); err == nil {
			t.Fatal("a discontinuous chain must fail the lifecycle list")
		}
		if _, err := db.ListSharedLifecycleHistoryPage(ctx, "task", req.ProjectID, req.TaskID, SharedLifecycleHistoryCursor{}, 64); err == nil {
			t.Fatal("a discontinuous chain must fail the history page")
		}
	})
	t.Run("final state disagreement", func(t *testing.T) {
		db, req := tsk585CompletionFixture(t)
		if err := db.CommitTaskCompletion(ctx, req); err != nil {
			t.Fatal(err)
		}
		var stored model.TaskAuthoring
		if err := json.Unmarshal(req.TaskPayload, &stored); err != nil {
			t.Fatal(err)
		}
		stored.Status = model.TaskAuthoringPlanned
		payload := mustJSON(t, stored)
		if _, err := db.Shared.Exec(ctx, `UPDATE shared_tasks SET payload=? WHERE id=?`, payload, req.TaskID); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ListTaskLifecycleEvents(ctx, req.ProjectID, req.TaskID, 64); err == nil {
			t.Fatal("a chain that disagrees with the current status must fail the lifecycle list")
		}
		if _, _, err := db.ReadTaskCompletionEvent(ctx, req.ProjectID, req.TaskID); err == nil {
			t.Fatal("a chain that disagrees with the current status must fail the completion read")
		}
	})
	t.Run("chain overflow", func(t *testing.T) {
		db, req := tsk585CompletionFixture(t)
		if err := db.CommitTaskCompletion(ctx, req); err != nil {
			t.Fatal(err)
		}
		archive := tsk531ArchiveRequest(t, db, req, "EXM-TSK1")
		if err := db.CommitTaskArchive(ctx, archive); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ListSharedLifecycleEvents(ctx, "task", req.ProjectID, req.TaskID, 1); err == nil {
			t.Fatal("a chain above the bounded read maximum must fail closed")
		}
		if _, err := db.ListSharedLifecycleHistoryPage(ctx, "task", req.ProjectID, req.TaskID, SharedLifecycleHistoryCursor{}, 1); err != nil {
			t.Fatalf("history pagination must stay bounded rather than overflow: %v", err)
		}
	})
}

func TestTSK531LifecycleMutationKindIsRequiredAndExact(t *testing.T) {
	db, req := tsk585CompletionFixture(t)
	ctx := context.Background()
	base := SharedLifecycleEventRequest{
		OperationID:           "tsk531-mutation-kind",
		EntityType:            "task",
		ProjectID:             req.ProjectID,
		EntityID:              req.TaskID,
		ExpectedRevision:      1,
		ExpectedStoreRevision: 1,
		ExpectedPayload:       req.PreviousTaskPayload,
		Revision:              1,
		Kind:                  "task-status",
		EventKind:             SharedLifecycleEventKindStatus,
		FromStatus:            model.TaskAuthoringPlanned,
		ToStatus:              model.TaskAuthoringDone,
		Payload:               req.TaskPayload,
		Actor:                 "planner",
		Reason:                "probe",
		ChangedFields:         []string{"status"},
		Contract:              []byte(`{"probe":true}`),
		CreatedAt:             req.RecordedAt,
	}
	missing := base
	if _, err := db.CommitSharedLifecycleEvent(ctx, missing); err == nil {
		t.Fatal("a lifecycle event without a mutation kind was accepted")
	}
	wrong := base
	wrong.HistoryMutationKind = "archive"
	if _, err := db.CommitSharedLifecycleEvent(ctx, wrong); err == nil {
		t.Fatal("a lifecycle event with the wrong mutation kind was accepted")
	}
	unpaired := base
	unpaired.HistoryMutationKind = "status"
	unpaired.ChangedFields = nil
	if _, err := db.CommitSharedLifecycleEvent(ctx, unpaired); err == nil {
		t.Fatal("a lifecycle event without changed fields was accepted")
	}
	duplicated := base
	duplicated.HistoryMutationKind = "status"
	duplicated.ChangedFields = []string{"status", "status"}
	if _, err := db.CommitSharedLifecycleEvent(ctx, duplicated); err == nil {
		t.Fatal("a lifecycle event with duplicate changed fields was accepted")
	}
	rows, err := db.Shared.Query(ctx, `SELECT COUNT(*) FROM shared_lifecycle_events`)
	if err != nil || rows.Rows[0][0] != int64(0) {
		t.Fatalf("rejected lifecycle probes wrote authority: rows=%#v err=%v", rows.Rows, err)
	}
}

func TestTSK531LifecycleKindTargetPairingIsDescriptorOwned(t *testing.T) {
	db, req := tsk585CompletionFixture(t)
	ctx := context.Background()
	if _, err := db.CommitSharedLifecycleEvent(ctx, SharedLifecycleEventRequest{
		OperationID:           "tsk531-status-to-archived",
		EntityType:            "task",
		ProjectID:             req.ProjectID,
		EntityID:              req.TaskID,
		ExpectedRevision:      1,
		ExpectedStoreRevision: 1,
		ExpectedPayload:       req.PreviousTaskPayload,
		Revision:              1,
		Kind:                  "task-status",
		EventKind:             SharedLifecycleEventKindStatus,
		HistoryMutationKind:   "complete",
		FromStatus:            model.TaskAuthoringPlanned,
		ToStatus:              model.TaskAuthoringArchived,
		Payload:               req.TaskPayload,
		Actor:                 "planner",
		Reason:                "probe",
		ChangedFields:         []string{"status"},
		Contract:              []byte(`{"probe":true}`),
		CreatedAt:             req.RecordedAt,
	}); err == nil {
		t.Fatal("a status event targeting the archive status must fail closed")
	}
	if _, err := db.CommitSharedLifecycleEvent(ctx, SharedLifecycleEventRequest{
		OperationID:           "tsk531-archive-to-done",
		EntityType:            "task",
		ProjectID:             req.ProjectID,
		EntityID:              req.TaskID,
		ExpectedRevision:      1,
		ExpectedStoreRevision: 1,
		ExpectedPayload:       req.PreviousTaskPayload,
		Revision:              1,
		Kind:                  "task-archive",
		EventKind:             SharedLifecycleEventKindArchive,
		HistoryMutationKind:   "archive",
		FromStatus:            model.TaskAuthoringPlanned,
		ToStatus:              model.TaskAuthoringDone,
		Payload:               req.TaskPayload,
		Actor:                 "planner",
		Reason:                "probe",
		ChangedFields:         []string{"status"},
		Contract:              []byte(`{"probe":true}`),
		CreatedAt:             req.RecordedAt,
	}); err == nil {
		t.Fatal("an archive event not targeting the archive status must fail closed")
	}
	rows, err := db.Shared.Query(ctx, `SELECT COUNT(*) FROM shared_lifecycle_events`)
	if err != nil || rows.Rows[0][0] != int64(0) {
		t.Fatalf("rejected lifecycle probes wrote authority: rows=%#v err=%v", rows.Rows, err)
	}
}
