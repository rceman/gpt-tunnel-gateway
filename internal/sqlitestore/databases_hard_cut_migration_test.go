package sqlitestore

import (
	"context"
	"testing"

	"github.com/rceman/go-sqlite-store/migrate"
	upstream "github.com/rceman/go-sqlite-store/store"
)

func TestFreshBaselinesAreTheOnlyMarkersAndReopen(t *testing.T) {
	state := t.TempDir()
	db, err := Open(state)
	if err != nil {
		t.Fatal(err)
	}
	assertFreshSharedMigrationMarkers(t, db.Shared)
	assertMigrationMarker(t, db.Local, localBaselineVersion, localBaselineName)
	assertFinalSharedSchema(t, db.Shared)
	assertFinalLocalSchema(t, db.Local)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(state)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	assertFreshSharedMigrationMarkers(t, reopened.Shared)
	assertMigrationMarker(t, reopened.Local, localBaselineVersion, localBaselineName)
}

func assertFreshSharedMigrationMarkers(t *testing.T, db *upstream.Store) {
	t.Helper()
	rows, err := db.Query(context.Background(), `SELECT version,name FROM schema_migrations ORDER BY version`)
	if err != nil {
		t.Fatal(err)
	}
	want := [][2]any{{sharedBaselineVersion, sharedBaselineName}, {sharedTaskSummaryMigrationVersion, sharedTaskSummaryMigrationName}, {sharedTaskSequenceMigrationVersion, sharedTaskSequenceMigrationName}}
	if len(rows.Rows) != len(want) {
		t.Fatalf("fresh Shared migration markers=%#v, want=%#v", rows.Rows, want)
	}
	for i, marker := range want {
		if rows.Rows[i][0] != marker[0] || rows.Rows[i][1] != marker[1] {
			t.Fatalf("fresh Shared migration marker[%d]=%#v, want=%#v", i, rows.Rows[i], marker)
		}
	}
}

func TestLegacyNumericAndBridgeMarkersFailClosed(t *testing.T) {
	for _, tc := range []struct {
		name       string
		path       func(string) string
		version    int64
		markerName string
	}{
		{name: "shared numeric", path: func(state string) string { shared, _ := Paths(state); return shared }, version: 1, markerName: "gpt_tunnel_shared_authority_v1"},
		{name: "shared bridge", path: func(state string) string { shared, _ := Paths(state); return shared }, version: 202609080504, markerName: "bridge legacy shared migrations"},
		{name: "local numeric", path: func(state string) string { _, local := Paths(state); return local }, version: 6, markerName: "gpt_tunnel_local_callback_epochs_v6"},
		{name: "local bridge", path: func(state string) string { _, local := Paths(state); return local }, version: 202609080506, markerName: "bridge legacy local migrations"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := t.TempDir()
			path := tc.path(state)
			raw, err := upstream.Open(engineConfig(Config{})(path))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := raw.Exec(context.Background(), `CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, name TEXT NOT NULL)`); err != nil {
				t.Fatal(err)
			}
			if _, err := raw.Exec(context.Background(), `INSERT INTO schema_migrations(version,name) VALUES(?,?)`, tc.version, tc.markerName); err != nil {
				t.Fatal(err)
			}
			if err := raw.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := Open(state); err == nil {
				t.Fatal("legacy migration marker was accepted")
			}
		})
	}
}

func TestMigrationHistoryMismatchAndUnknownTimestampFailClosed(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(context.Context, *upstream.Store) error
	}{
		{
			name: "baseline version and name mismatch",
			setup: func(ctx context.Context, db *upstream.Store) error {
				_, err := db.Exec(ctx, `INSERT INTO schema_migrations(version,name) VALUES(?,?)`, sharedBaselineVersion+1, sharedBaselineName)
				return err
			},
		},
		{
			name: "unknown extra timestamp marker",
			setup: func(ctx context.Context, db *upstream.Store) error {
				if _, err := db.Exec(ctx, `INSERT INTO schema_migrations(version,name) VALUES(?,?)`, sharedBaselineVersion, sharedBaselineName); err != nil {
					return err
				}
				_, err := db.Exec(ctx, `INSERT INTO schema_migrations(version,name) VALUES(?,?)`, 209901010101, "unknown future migration")
				return err
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := t.TempDir()
			sharedPath, _ := Paths(state)
			raw, err := upstream.Open(engineConfig(Config{})(sharedPath))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := raw.Exec(context.Background(), `CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, name TEXT NOT NULL)`); err != nil {
				raw.Close()
				t.Fatal(err)
			}
			if err := tc.setup(context.Background(), raw); err != nil {
				raw.Close()
				t.Fatal(err)
			}
			if err := raw.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := Open(state); err == nil {
				t.Fatal("invalid migration history was accepted")
			}
		})
	}
}

func TestApplyActiveMigrationsAcceptsFutureTimestampMigration(t *testing.T) {
	path := t.TempDir() + "/future.db"
	db, err := upstream.Open(upstream.Config{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	migrations := []migrate.Migration{
		{Version: sharedBaselineVersion, Name: sharedBaselineName, Statements: []upstream.Statement{{SQL: `CREATE TABLE future_baseline_probe (id INTEGER PRIMARY KEY)`}}},
		{Version: 209901010101, Name: "future timestamp probe", Statements: []upstream.Statement{{SQL: `CREATE TABLE future_timestamp_probe (id INTEGER PRIMARY KEY)`}}},
	}
	if err := applyActiveMigrations(context.Background(), db, migrations...); err != nil {
		t.Fatal(err)
	}
	if err := applyActiveMigrations(context.Background(), db, migrations...); err != nil {
		t.Fatalf("reopen/idempotent apply: %v", err)
	}
	rows, err := db.Query(context.Background(), `SELECT version,name FROM schema_migrations ORDER BY version`)
	if err != nil || len(rows.Rows) != 2 {
		t.Fatalf("future markers=%#v err=%v", rows.Rows, err)
	}
	if rows.Rows[1][0] != int64(209901010101) || rows.Rows[1][1] != "future timestamp probe" {
		t.Fatalf("future marker=%#v", rows.Rows[1])
	}
}
