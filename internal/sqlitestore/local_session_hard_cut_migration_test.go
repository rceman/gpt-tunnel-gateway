package sqlitestore

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	upstream "github.com/rceman/go-sqlite-store/store"
)

func TestMigrateNoncanonicalLocalSessionsIsBoundedDeferredAndIdempotent(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	created := time.Now().UTC().Format(time.RFC3339Nano)
	legacyID := "SA-GTW-TYJ7"
	legacyPayload := []byte(`{"session_id":"SA-GTW-TYJ7","status":"active"}`)
	if err := db.CreateLocalSession(ctx, LocalSession{
		ID:        legacyID,
		Payload:   legacyPayload,
		UpdatedAt: created,
		Status:    "active",
	}); err != nil {
		t.Fatal(err)
	}
	canonicalID := "HOM_GTW_W_3pbtm"
	if err := db.CreateLocalSession(ctx, LocalSession{
		ID:        canonicalID,
		Payload:   []byte(`{"session_id":"HOM_GTW_W_3pbtm"}`),
		UpdatedAt: created,
		Status:    "active",
	}); err != nil {
		t.Fatal(err)
	}
	adminID := "HOM_ADM_" + strings.Repeat("a", 32)
	if err := db.CreateLocalSession(ctx, LocalSession{
		ID:        adminID,
		Payload:   []byte(`{"session_id":"admin"}`),
		UpdatedAt: created,
		Status:    "active",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Local.Exec(ctx, `INSERT INTO local_operations(operation_id,project_id,project_code,operation_number,mutation_id,kind,status,created_at,updated_at,admission_session_id) VALUES(?,?,?,?,?,?,?,?,?,?)`, "EXM-OPR1", "example", "EXM", 1, strings.Repeat("a", 64), "task-authoring-update", "running", created, created, legacyID); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.MigrateNoncanonicalLocalSessions(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ReadLocalSession(ctx, legacyID); err != nil {
		t.Fatalf("Session with live operation was removed: %v", err)
	}
	state, err := db.localUpgradeMigrationState(ctx, localNoncanonicalSessionMigrationID)
	if err != nil || state != "in_progress" {
		t.Fatalf("deferred migration state=%q err=%v", state, err)
	}
	if _, err := db.Local.Exec(ctx, `UPDATE local_operations SET status='completed' WHERE operation_id=?`, "EXM-OPR1"); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateNoncanonicalLocalSessions(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ReadLocalSession(ctx, legacyID); err == nil {
		t.Fatal("stale noncanonical Session remains")
	}
	for _, id := range []string{canonicalID, adminID} {
		if _, err := db.ReadLocalSession(ctx, id); err != nil {
			t.Fatalf("canonical Session %q was removed: %v", id, err)
		}
	}
	state, err = db.localUpgradeMigrationState(ctx, localNoncanonicalSessionMigrationID)
	if err != nil || state != "complete" {
		t.Fatalf("completed migration state=%q err=%v", state, err)
	}
	if err := db.MigrateNoncanonicalLocalSessions(ctx); err != nil {
		t.Fatalf("idempotent replay: %v", err)
	}
}

func TestMigrateNoncanonicalLocalSessionsRejectsOverBound(t *testing.T) {
	ctx := context.Background()
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	created := time.Now().UTC().Format(time.RFC3339Nano)
	statements := make([]struct {
		SQL  string
		Args []any
	}, 0, localNoncanonicalSessionMaxRows+1)
	for i := 0; i <= localNoncanonicalSessionMaxRows; i++ {
		id := fmt.Sprintf("legacy-session-%04d", i)
		statements = append(statements, struct {
			SQL  string
			Args []any
		}{`INSERT INTO local_sessions(session_id,payload,updated_at,status) VALUES(?,?,?,'ended')`, []any{id, []byte(`{}`), created}})
	}
	batch := make([]upstream.Statement, 0, len(statements))
	for _, statement := range statements {
		batch = append(batch, upstream.Statement{SQL: statement.SQL, Args: statement.Args})
	}
	if _, err := db.Local.Batch(ctx, batch); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateNoncanonicalLocalSessions(ctx); err == nil {
		t.Fatal("over-bound Session migration succeeded")
	}
	rows, err := db.Local.Query(ctx, `SELECT COUNT(*) FROM local_sessions`)
	if err != nil || len(rows.Rows) != 1 || rows.Rows[0][0] != int64(localNoncanonicalSessionMaxRows+1) {
		t.Fatalf("over-bound migration changed rows: %#v err=%v", rows.Rows, err)
	}
}
