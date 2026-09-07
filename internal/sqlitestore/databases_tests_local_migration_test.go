package sqlitestore

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/rceman/go-sqlite-store/migrate"
	upstream "github.com/rceman/go-sqlite-store/store"
)

func TestOpenUpgradesFullHistoricalLocalLineageWithoutRewritingHistory(t *testing.T) {
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
			Name:    historicalLocalInterSessionMessagesMigrationName,
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
		{
			Version: 3,
			Name:    historicalLocalHistoryIndexesMigrationName,
			Statements: []upstream.Statement{
				{SQL: `CREATE INDEX IF NOT EXISTS local_events_kind_idx ON local_events(kind,recorded_at DESC,id DESC)`},
				{SQL: `CREATE INDEX IF NOT EXISTS local_events_recorded_idx ON local_events(recorded_at DESC,id DESC)`},
				{SQL: `CREATE INDEX IF NOT EXISTS local_logs_filter_idx ON local_logs(level,component,recorded_at DESC,id DESC)`},
				{SQL: `CREATE INDEX IF NOT EXISTS local_logs_recorded_idx ON local_logs(recorded_at DESC,id DESC)`},
				{SQL: `CREATE INDEX IF NOT EXISTS local_inter_session_messages_expiry_idx ON local_inter_session_messages(expires_at)`},
			},
		},
		{
			Version: 4,
			Name:    historicalLocalHistoryProjectIndexesMigrationName,
			Statements: []upstream.Statement{
				{SQL: `ALTER TABLE local_events ADD COLUMN project_id TEXT NOT NULL DEFAULT ''`},
				{SQL: `ALTER TABLE local_logs ADD COLUMN project_id TEXT NOT NULL DEFAULT ''`},
				{SQL: `CREATE INDEX IF NOT EXISTS local_events_project_idx ON local_events(project_id,recorded_at DESC,id DESC)`},
				{SQL: `CREATE INDEX IF NOT EXISTS local_logs_project_idx ON local_logs(project_id,recorded_at DESC,id DESC)`},
			},
		},
		{
			Version: 5,
			Name:    historicalLocalHistoryProjectBackfillMigrationName,
			Statements: []upstream.Statement{
				{SQL: `UPDATE local_events SET project_id='' WHERE project_id IS NULL`},
				{SQL: `UPDATE local_logs SET project_id='' WHERE project_id IS NULL`},
			},
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
		{int64(2), historicalLocalInterSessionMessagesMigrationName},
		{int64(3), historicalLocalHistoryIndexesMigrationName},
		{int64(4), historicalLocalHistoryProjectIndexesMigrationName},
		{int64(5), historicalLocalHistoryProjectBackfillMigrationName},
		{localCallbackEpochsMigrationVersion, localCallbackEpochsMigrationDescription},
		{localAgentRegistryMigrationVersion, localAgentRegistryMigrationDescription},
		{localSessionStoreMigrationVersion, localSessionStoreMigrationDescription},
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
	objects, err := db.Local.Query(context.Background(), `SELECT type,name FROM sqlite_master WHERE name IN ('local_agents','local_agents_project_idx','local_callback_epochs','local_callback_epochs_pending_idx') ORDER BY type,name`)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	if len(objects.Rows) != 4 || objects.Rows[0][0] != "index" || objects.Rows[0][1] != "local_agents_project_idx" || objects.Rows[1][0] != "index" || objects.Rows[1][1] != "local_callback_epochs_pending_idx" || objects.Rows[2][0] != "table" || objects.Rows[2][1] != "local_agents" || objects.Rows[3][0] != "table" || objects.Rows[3][1] != "local_callback_epochs" {
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
	if len(rows.Rows) != len(want) {
		t.Fatalf("reopened migration history=%#v", rows.Rows)
	}
	for i := range want {
		if rows.Rows[i][0] != want[i][0] || rows.Rows[i][1] != want[i][1] {
			t.Fatalf("reopened migration history[%d]=%#v, want=%#v", i, rows.Rows[i], want[i])
		}
	}
}

func TestOpenFreshLocalAppliesTimestampedLocalMigrations(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Local.Query(context.Background(), `SELECT version,name FROM schema_migrations ORDER BY version`)
	if err != nil {
		t.Fatal(err)
	}
	want := [][]any{{int64(1), localOperationalMigrationName}, {localCallbackEpochsMigrationVersion, localCallbackEpochsMigrationDescription}, {localAgentRegistryMigrationVersion, localAgentRegistryMigrationDescription}, {localSessionStoreMigrationVersion, localSessionStoreMigrationDescription}}
	if len(rows.Rows) != len(want) {
		t.Fatalf("fresh migration history=%#v, want=%#v", rows.Rows, want)
	}
	for i := range want {
		if rows.Rows[i][0] != want[i][0] || rows.Rows[i][1] != want[i][1] {
			t.Fatalf("fresh migration history[%d]=%#v, want=%#v", i, rows.Rows[i], want[i])
		}
	}
	rows, err = db.Local.Query(context.Background(), `SELECT type,name FROM sqlite_master WHERE name IN ('local_agents','local_agents_project_idx','local_callback_epochs','local_callback_epochs_pending_idx') ORDER BY type,name`)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows.Rows) != 4 || rows.Rows[0][0] != "index" || rows.Rows[0][1] != "local_agents_project_idx" || rows.Rows[1][0] != "index" || rows.Rows[1][1] != "local_callback_epochs_pending_idx" || rows.Rows[2][0] != "table" || rows.Rows[2][1] != "local_agents" || rows.Rows[3][0] != "table" || rows.Rows[3][1] != "local_callback_epochs" {
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
