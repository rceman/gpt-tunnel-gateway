package sqlitestore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	upstream "github.com/rceman/go-sqlite-store/store"
)

func TestOpenMigratesTwoIndependentStoresAndSharedCASIsAtomic(t *testing.T) {
	stateDir := t.TempDir()
	db, err := Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	sharedPath, localPath := Paths(stateDir)
	if db.SharedPath() != sharedPath || db.LocalPath() != localPath || sharedPath == localPath {
		t.Fatalf("database paths shared=%q local=%q", db.SharedPath(), db.LocalPath())
	}
	ctx := context.Background()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := db.Shared.Exec(ctx, `INSERT INTO shared_tasks(id,revision,payload,updated_at) VALUES(?,?,?,?)`, "TSK-1", 1, []byte("v1"), now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Local.Exec(ctx, `INSERT INTO local_logs(id,level,component,event,payload,recorded_at) VALUES(?,?,?,?,?,?)`, "LOG-1", "info", "test", "local", []byte("local"), now); err != nil {
		t.Fatal(err)
	}
	_, err = db.Shared.Batch(ctx, []upstream.Statement{
		{SQL: `UPDATE shared_tasks SET revision=?, payload=? WHERE id=? AND revision=?`, Args: []any{2, []byte("v2"), "TSK-1", 0}, RequireRowsAffected: 1},
		{SQL: `INSERT INTO hub_outbox(id,entity_type,entity_id,revision,kind,payload,created_at) VALUES(?,?,?,?,?,?,?)`, Args: []any{"OUT-1", "task", "TSK-1", 2, "update", []byte("v2"), now}, RequireRowsAffected: 1},
	})
	if !errors.Is(err, upstream.ErrRowsAffectedMismatch) {
		t.Fatalf("stale shared CAS error = %v", err)
	}
	row, err := db.Shared.Query(ctx, `SELECT revision, payload, (SELECT COUNT(*) FROM hub_outbox) FROM shared_tasks WHERE id=?`, "TSK-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(row.Rows) != 1 || row.Rows[0][0] != int64(1) || string(row.Rows[0][1].([]byte)) != "v1" || row.Rows[0][2] != int64(0) {
		t.Fatalf("stale CAS was not atomic: %#v", row.Rows)
	}
	localOutbox, err := db.Local.Query(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name='hub_outbox'`)
	if err != nil {
		t.Fatal(err)
	}
	if len(localOutbox.Rows) != 0 {
		t.Fatalf("local store contains Hub outbox schema: %#v", localOutbox.Rows)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(localPath); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	row, err = reopened.Shared.Query(ctx, `SELECT revision, payload FROM shared_tasks WHERE id=?`, "TSK-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(row.Rows) != 1 || row.Rows[0][0] != int64(1) || string(row.Rows[0][1].([]byte)) != "v1" {
		t.Fatalf("shared correctness was lost after local recreation: %#v", row.Rows)
	}
}

func TestOpenRejectsSecondOwner(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = upstream.Open(upstream.Config{Path: db.SharedPath()})
	if !errors.Is(err, upstream.ErrAlreadyOpen) {
		t.Fatalf("second shared owner error = %v", err)
	}
}

func TestOpenReportsLockAcquisitionStage(t *testing.T) {
	stateDir := t.TempDir()
	owned, err := Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer owned.Close()

	_, err = Open(stateDir)
	var openErr *OpenError
	if !errors.As(err, &openErr) {
		t.Fatalf("open error type=%T value=%v", err, err)
	}
	if openErr.Stage != "lock_acquisition" || openErr.Database != "shared" || openErr.Path == "" {
		t.Fatalf("open error=%#v", openErr)
	}
	if !errors.Is(err, upstream.ErrAlreadyOpen) {
		t.Fatalf("open error does not preserve ownership sentinel: %v", err)
	}
}

func TestOpenObserverReportsSQLiteStartupPhases(t *testing.T) {
	var phases []string
	db, err := OpenWithObserver(t.TempDir(), func(phase string) { phases = append(phases, phase) })
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	want := []string{
		"SQLITE_DIRECTORY_PREPARE",
		"SQLITE_SHARED_OPEN",
		"SQLITE_LOCAL_OPEN",
		"SQLITE_SHARED_MIGRATION",
		"SQLITE_LOCAL_MIGRATION",
		"SQLITE_READY",
	}
	if len(phases) != len(want) {
		t.Fatalf("startup phases=%v, want=%v", phases, want)
	}
	for i := range want {
		if phases[i] != want[i] {
			t.Fatalf("startup phases=%v, want=%v", phases, want)
		}
	}
}

func TestOpenAcceptsReleasedVersionThreeAndAppliesBootstrapMigration(t *testing.T) {
	stateDir := t.TempDir()
	db, err := Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	sharedPath := db.SharedPath()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	raw, err := upstream.Open(upstream.Config{Path: sharedPath})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := raw.Exec(ctx, `DELETE FROM schema_migrations WHERE version=?`, int64(9)); err != nil {
		raw.Close()
		t.Fatal(err)
	}
	if _, err := raw.Exec(ctx, `DROP TABLE shared_bootstrap_markers`); err != nil {
		raw.Close()
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	rows, err := reopened.Shared.Query(ctx, `SELECT name FROM schema_migrations WHERE version=?`, int64(3))
	if err != nil || len(rows.Rows) != 1 || rows.Rows[0][0] != sharedCutoverMigrationName {
		t.Fatalf("released version-3 identity changed: rows=%#v err=%v", rows.Rows, err)
	}
	rows, err = reopened.Shared.Query(ctx, `SELECT name FROM schema_migrations WHERE version=?`, int64(9))
	if err != nil || len(rows.Rows) != 1 || rows.Rows[0][0] != sharedBootstrapMigrationName {
		t.Fatalf("bootstrap migration was not applied at version 9: rows=%#v err=%v", rows.Rows, err)
	}
}

func TestSharedMigrationHistoryPreservesReleasedVersionsBeforeCandidates(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	rows, err := db.Shared.Query(context.Background(), `SELECT version,name FROM schema_migrations ORDER BY version`)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		version int64
		name    string
	}{
		{1, "gpt_tunnel_shared_authority_v1"},
		{2, sharedReplicationMigrationName},
		{3, sharedCutoverMigrationName},
		{4, "gpt_tunnel_shared_project_identifiers_v1"},
		{5, "gpt_tunnel_shared_train_admission_v1"},
		{6, "gpt_tunnel_shared_train_admission_update_guard_v1"},
		{7, sharedTaskSequenceMigrationName},
		{8, sharedIntegrationCurrentMigrationName},
		{9, sharedBootstrapMigrationName},
		{10, sharedADROutboxMigrationName},
	}
	if len(rows.Rows) != len(want) {
		t.Fatalf("migration history length=%d, want=%d: %#v", len(rows.Rows), len(want), rows.Rows)
	}
	for i, entry := range want {
		if rows.Rows[i][0] != entry.version || rows.Rows[i][1] != entry.name {
			t.Fatalf("migration history[%d]=%#v, want version=%d name=%q", i, rows.Rows[i], entry.version, entry.name)
		}
	}
}

func BenchmarkSharedAndLocalTraffic(b *testing.B) {
	db, err := Open(filepath.Join(b.TempDir(), "state"))
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		now := time.Now().UTC().Format(time.RFC3339Nano)
		if _, err := db.Shared.Batch(ctx, []upstream.Statement{
			{SQL: `INSERT OR REPLACE INTO shared_tasks(id,revision,payload,updated_at) VALUES(?,?,?,?)`, Args: []any{"TSK-BENCH", int64(i + 1), []byte("shared"), now}},
			{SQL: `INSERT INTO hub_outbox(id,entity_type,entity_id,revision,kind,payload,created_at) VALUES(?,?,?,?,?,?,?)`, Args: []any{"OUT-BENCH-" + strconv.Itoa(i), "task", "TSK-BENCH", int64(i + 1), "update", []byte("shared"), now}},
		}); err != nil {
			b.Fatal(err)
		}
		if _, err := db.Local.Exec(ctx, `INSERT INTO local_logs(id,level,component,event,payload,recorded_at) VALUES(?,?,?,?,?,?)`, "LOG-BENCH-"+strconv.Itoa(i), "info", "bench", "local", []byte("local"), now); err != nil {
			b.Fatal(err)
		}
	}
}
