package sqlitestore

import (
	"context"
	"testing"
	"time"

	"github.com/rceman/go-sqlite-store/migrate"
	upstream "github.com/rceman/go-sqlite-store/store"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestTSK531TaskSequenceMigrationRepairsAndPreservesAllocators(t *testing.T) {
	tests := []struct {
		name       string
		sequence   *int64
		want       int64
		wantInsert bool
	}{
		{name: "stale", sequence: ptrInt64(546), want: 551},
		{name: "higher", sequence: ptrInt64(600), want: 600},
		{name: "missing", want: 551, wantInsert: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := newTaskSequenceMigrationDB(t)
			insertSequenceTask(t, db, "GTW-TSK550", "example", "GTW")
			if tt.sequence != nil {
				insertTaskSequence(t, db, "example", "GTW", *tt.sequence)
			}
			if err := applySharedMigrations(context.Background(), db); err != nil {
				t.Fatal(err)
			}
			assertTaskSequence(t, db, "example", "GTW", tt.want)
			if tt.wantInsert {
				rows, err := db.Query(context.Background(), `SELECT COUNT(*) FROM shared_entity_sequences WHERE entity_type='task' AND project_id='example'`)
				if err != nil || len(rows.Rows) != 1 || rows.Rows[0][0] != int64(1) {
					t.Fatalf("inserted sequence rows=%#v err=%v", rows.Rows, err)
				}
			}
			if err := applySharedMigrations(context.Background(), db); err != nil {
				t.Fatalf("idempotent reopen: %v", err)
			}
			assertTaskSequence(t, db, "example", "GTW", tt.want)
		})
	}
}

func TestTSK531TaskSequenceMigrationIsolatesProjectsAndRejectsInvalidState(t *testing.T) {
	db := newTaskSequenceMigrationDB(t)
	insertSequenceTask(t, db, "GTW-TSK10", "example", "GTW")
	insertSequenceTask(t, db, "RSM-TSK3", "reposuite", "RSM")
	if err := applySharedMigrations(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	assertTaskSequence(t, db, "example", "GTW", 11)
	assertTaskSequence(t, db, "reposuite", "RSM", 4)

	for _, tc := range []struct {
		name string
		id   string
	}{
		{name: "malformed id", id: "GTW-TSK0"},
		{name: "invalid id shape", id: "GTW-task11"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			invalid := newTaskAuthoring(tc.id, "invalid-project")
			payload := mustJSON(t, invalid)
			raw, err := upstream.Open(upstream.Config{Path: t.TempDir() + "/raw.db"})
			if err != nil {
				t.Fatal(err)
			}
			defer raw.Close()
			if err := applyBaselineForSequenceTest(raw); err != nil {
				t.Fatal(err)
			}
			if _, err := raw.Exec(context.Background(), `INSERT INTO shared_tasks(id,revision,payload,updated_at) VALUES(?,?,?,?)`, tc.id, 1, payload, invalid.UpdatedAt.Format(time.RFC3339Nano)); err != nil {
				t.Fatal(err)
			}
			if err := applySharedMigrations(context.Background(), raw); err == nil {
				t.Fatal("invalid retained Task accepted")
			}
		})
	}

	mismatch := newTaskSequenceMigrationDB(t)
	insertSequenceTask(t, mismatch, "GTW-TSK10", "example", "GTW")
	insertTaskSequence(t, mismatch, "example", "BAD", 1)
	if err := applySharedMigrations(context.Background(), mismatch); err == nil {
		t.Fatal("mismatched project code accepted")
	}

	crossProject := newTaskSequenceMigrationDB(t)
	insertSequenceTask(t, crossProject, "GTW-TSK10", "example", "GTW")
	insertSequenceTask(t, crossProject, "RSM-TSK3", "example", "RSM")
	if err := applySharedMigrations(context.Background(), crossProject); err == nil {
		t.Fatal("cross-project Task identity accepted")
	}
}

func newTaskSequenceMigrationDB(t *testing.T) *upstream.Store {
	t.Helper()
	db, err := upstream.Open(upstream.Config{Path: t.TempDir() + "/shared.db"})
	if err != nil {
		t.Fatal(err)
	}
	if err := applyBaselineForSequenceTest(db); err != nil {
		db.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func applyBaselineForSequenceTest(db *upstream.Store) error {
	return migrateApply(context.Background(), db, sharedBaselineMigration())
}

func migrateApply(ctx context.Context, db *upstream.Store, migration migrate.Migration) error {
	return migrate.Apply(ctx, db, []migrate.Migration{migration}, migrate.Options{})
}

func insertSequenceTask(t *testing.T, db *upstream.Store, id, projectID, projectCode string) {
	t.Helper()
	task := newTaskAuthoring(id, projectID)
	if projectCode != "" {
		if parsed, _, err := model.ParseTaskID(id); err != nil || parsed != projectCode {
			t.Fatalf("fixture project code mismatch: id=%s code=%s err=%v", id, projectCode, err)
		}
	}
	when := task.UpdatedAt.Format(time.RFC3339Nano)
	if _, err := db.Exec(context.Background(), `INSERT INTO shared_tasks(id,revision,payload,updated_at) VALUES(?,?,?,?)`, id, 1, mustJSON(t, task), when); err != nil {
		t.Fatal(err)
	}
}

func insertTaskSequence(t *testing.T, db *upstream.Store, projectID, projectCode string, next int64) {
	t.Helper()
	if _, err := db.Exec(context.Background(), `INSERT INTO shared_entity_sequences(entity_type,project_id,project_code,next_number) VALUES('task',?,?,?)`, projectID, projectCode, next); err != nil {
		t.Fatal(err)
	}
}

func assertTaskSequence(t *testing.T, db *upstream.Store, projectID, projectCode string, want int64) {
	t.Helper()
	rows, err := db.Query(context.Background(), `SELECT project_code,next_number FROM shared_entity_sequences WHERE entity_type='task' AND project_id=?`, projectID)
	if err != nil || len(rows.Rows) != 1 || rows.Rows[0][0] != projectCode || rows.Rows[0][1] != want {
		t.Fatalf("sequence=%#v err=%v want=%s/%d", rows.Rows, err, projectCode, want)
	}
}

func newTaskAuthoring(id, projectID string) model.TaskAuthoring {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	task := model.TaskAuthoring{SchemaVersion: model.TaskAuthoringSchemaVersion, ID: id, ProjectID: projectID, Revision: 1, Title: "Sequence fixture", Summary: "A bounded sequence fixture.", Objective: "A bounded sequence objective.", ADRRelation: model.TaskADRNoRequired, Status: model.TaskAuthoringPlanned, CreatedBy: "planner", CreatedAt: now, UpdatedAt: now}
	task.RevisionSHA256, _ = model.HashTaskAuthoring(task)
	return task
}

func ptrInt64(value int64) *int64 { return &value }
