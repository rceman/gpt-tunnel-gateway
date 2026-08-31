package sqlitestore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/rceman/go-sqlite-store/migrate"
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

func TestOpenUpgradesHistoricalLocalVersionTwoWithoutRewritingHistory(t *testing.T) {
	stateDir := t.TempDir()
	_, localPath := Paths(stateDir)
	if err := os.MkdirAll(filepath.Dir(localPath), 0o700); err != nil {
		t.Fatal(err)
	}
	historical, err := upstream.Open(upstream.Config{Path: localPath})
	if err != nil {
		t.Fatal(err)
	}
	historicalMigrations := []migrate.Migration{
		{
			Version: 1,
			Name:    localOperationalMigrationName,
			Statements: []upstream.Statement{
				{SQL: `CREATE TABLE IF NOT EXISTS local_events (id TEXT PRIMARY KEY, kind TEXT NOT NULL, payload BLOB NOT NULL, recorded_at TEXT NOT NULL)`},
				{SQL: `CREATE TABLE IF NOT EXISTS local_messages (id TEXT PRIMARY KEY, session_id TEXT, payload BLOB NOT NULL, recorded_at TEXT NOT NULL)`},
				{SQL: `CREATE TABLE IF NOT EXISTS local_logs (id TEXT PRIMARY KEY, level TEXT NOT NULL, component TEXT NOT NULL, event TEXT NOT NULL, payload BLOB NOT NULL, recorded_at TEXT NOT NULL)`},
				{SQL: `CREATE TABLE IF NOT EXISTS local_retention (name TEXT PRIMARY KEY, cutoff_at TEXT NOT NULL)`},
			},
		},
		{
			Version: 2,
			Name:    "gpt_tunnel_local_inter_session_messages_v1",
			Statements: []upstream.Statement{{SQL: `CREATE TABLE IF NOT EXISTS local_inter_session_messages (
				id TEXT PRIMARY KEY,
				project_id TEXT NOT NULL,
				source_session_id TEXT NOT NULL,
				target_session_id TEXT NOT NULL,
				topic TEXT NOT NULL,
				body TEXT NOT NULL,
				tags BLOB NOT NULL,
				created_at TEXT NOT NULL,
				expires_at TEXT NOT NULL
			)`}},
		},
	}
	if err := migrate.Apply(context.Background(), historical, historicalMigrations, migrate.Options{}); err != nil {
		historical.Close()
		t.Fatal(err)
	}
	if err := historical.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := db.Local.Query(context.Background(), `SELECT version,name FROM schema_migrations ORDER BY version`)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	want := [][]any{
		{int64(1), localOperationalMigrationName},
		{int64(2), "gpt_tunnel_local_inter_session_messages_v1"},
		{int64(3), localCallbackEpochsMigrationName},
	}
	if len(rows.Rows) != len(want) {
		db.Close()
		t.Fatalf("migration history=%#v, want=%#v", rows.Rows, want)
	}
	for i := range want {
		if rows.Rows[i][0] != want[i][0] || rows.Rows[i][1] != want[i][1] {
			db.Close()
			t.Fatalf("migration history[%d]=%#v, want=%#v", i, rows.Rows[i], want[i])
		}
	}
	objects, err := db.Local.Query(context.Background(), `SELECT type,name FROM sqlite_master WHERE name IN ('local_callback_epochs','local_callback_epochs_pending_idx') ORDER BY type,name`)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	if len(objects.Rows) != 2 || objects.Rows[0][0] != "index" || objects.Rows[0][1] != "local_callback_epochs_pending_idx" || objects.Rows[1][0] != "table" || objects.Rows[1][1] != "local_callback_epochs" {
		db.Close()
		t.Fatalf("callback schema=%#v", objects.Rows)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	rows, err = reopened.Local.Query(context.Background(), `SELECT version,name FROM schema_migrations ORDER BY version`)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows.Rows) != len(want) || rows.Rows[1][0] != int64(2) || rows.Rows[1][1] != "gpt_tunnel_local_inter_session_messages_v1" || rows.Rows[2][0] != int64(3) || rows.Rows[2][1] != localCallbackEpochsMigrationName {
		t.Fatalf("reopened migration history=%#v", rows.Rows)
	}
}

func TestOpenFreshLocalAppliesCallbackEpochMigrationAtVersionThree(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Local.Query(context.Background(), `SELECT version,name FROM schema_migrations ORDER BY version`)
	if err != nil {
		t.Fatal(err)
	}
	want := [][]any{{int64(1), localOperationalMigrationName}, {int64(3), localCallbackEpochsMigrationName}}
	if len(rows.Rows) != len(want) {
		t.Fatalf("fresh migration history=%#v, want=%#v", rows.Rows, want)
	}
	for i := range want {
		if rows.Rows[i][0] != want[i][0] || rows.Rows[i][1] != want[i][1] {
			t.Fatalf("fresh migration history[%d]=%#v, want=%#v", i, rows.Rows[i], want[i])
		}
	}
	rows, err = db.Local.Query(context.Background(), `SELECT type,name FROM sqlite_master WHERE name IN ('local_callback_epochs','local_callback_epochs_pending_idx') ORDER BY type,name`)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows.Rows) != 2 || rows.Rows[0][0] != "index" || rows.Rows[0][1] != "local_callback_epochs_pending_idx" || rows.Rows[1][0] != "table" || rows.Rows[1][1] != "local_callback_epochs" {
		t.Fatalf("fresh callback schema=%#v", rows.Rows)
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

func TestOpenRejectsWrongVersionTwoNameWithoutChangingMarker(t *testing.T) {
	stateDir := t.TempDir()
	db, err := Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	sharedPath := db.SharedPath()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	const wrongName = "gpt_tunnel_shared_wrong_v2"
	ctx := context.Background()
	raw, err := upstream.Open(upstream.Config{Path: sharedPath})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(ctx, `UPDATE schema_migrations SET name=? WHERE version=?`, wrongName, int64(2)); err != nil {
		raw.Close()
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := Open(stateDir); err == nil {
		t.Fatal("Open succeeded with a wrong version-2 migration name")
	}

	raw, err = upstream.Open(upstream.Config{Path: sharedPath})
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	rows, err := raw.Query(ctx, `SELECT name FROM schema_migrations WHERE version=?`, int64(2))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows.Rows) != 1 || rows.Rows[0][0] != wrongName {
		t.Fatalf("version-2 marker changed after rejection: %#v", rows.Rows)
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
		{11, sharedProjectConfigurationMigrationName},
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
