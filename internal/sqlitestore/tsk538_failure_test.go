package sqlitestore

import (
	"context"
	"testing"

	upstream "github.com/rceman/go-sqlite-store/store"
)

func TestTSK538MigrationHistoryFailuresAreClosed(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(context.Context, *upstream.Store) error
	}{
		{"unknown marker", func(ctx context.Context, db *upstream.Store) error {
			_, err := db.Exec(ctx, `UPDATE schema_migrations SET name='unknown' WHERE version=?`, sharedBaselineVersion)
			return err
		}},
		{"bridge only", func(ctx context.Context, db *upstream.Store) error {
			_, err := db.Exec(ctx, `DELETE FROM schema_migrations`)
			if err != nil {
				return err
			}
			_, err = db.Exec(ctx, `INSERT INTO schema_migrations(version,name) VALUES(?,?)`, sharedBridgeVersion, sharedBridgeName)
			return err
		}},
		{"baseline plus legacy", func(ctx context.Context, db *upstream.Store) error {
			_, err := db.Exec(ctx, `INSERT INTO schema_migrations(version,name) VALUES(?,?)`, int64(1), "gpt_tunnel_shared_authority_v1")
			return err
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stateDir := t.TempDir()
			db, err := Open(stateDir)
			if err != nil {
				t.Fatal(err)
			}
			path := db.SharedPath()
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			raw, err := upstream.Open(engineConfig(Config{})(path))
			if err != nil {
				t.Fatal(err)
			}
			if err := tc.mutate(context.Background(), raw); err != nil {
				raw.Close()
				t.Fatal(err)
			}
			if err := raw.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := Open(stateDir); err == nil {
				t.Fatal("Open accepted invalid migration history")
			}
		})
	}
}

func TestTSK538MigrationPrerequisitesAndMissingHistoryFailClosed(t *testing.T) {
	t.Run("missing deployed legacy table", func(t *testing.T) {
		stateDir := t.TempDir()
		sharedPath, _ := Paths(stateDir)
		raw, err := upstream.Open(engineConfig(Config{})(sharedPath))
		if err != nil {
			t.Fatal(err)
		}
		if err := applyTSK538DeployedSharedHistory(context.Background(), raw); err != nil {
			t.Fatal(err)
		}
		if _, err := raw.Exec(context.Background(), `DROP TABLE shared_agents`); err != nil {
			t.Fatal(err)
		}
		if err := raw.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(stateDir); err == nil {
			t.Fatal("Open accepted missing v12 prerequisite")
		}
	})

	t.Run("missing schema history beside managed object", func(t *testing.T) {
		stateDir := t.TempDir()
		sharedPath, _ := Paths(stateDir)
		raw, err := upstream.Open(engineConfig(Config{})(sharedPath))
		if err != nil {
			t.Fatal(err)
		}
		ctx := context.Background()
		if _, err := raw.Exec(ctx, `CREATE TABLE marker_probe(id TEXT)`); err != nil {
			t.Fatal(err)
		}
		if err := raw.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(stateDir); err == nil {
			t.Fatal("Open accepted managed schema without history")
		}
	})
}
