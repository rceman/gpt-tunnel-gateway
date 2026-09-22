package sqlitestore

import (
	"github.com/rceman/go-sqlite-store/migrate"
	upstream "github.com/rceman/go-sqlite-store/store"
)

func sharedTrackMigration() migrate.Migration {
	return migrate.Migration{
		Version:    sharedTrackMigrationVersion,
		Name:       sharedTrackMigrationName,
		Statements: []upstream.Statement{{SQL: `CREATE TABLE IF NOT EXISTS shared_tracks (id TEXT PRIMARY KEY, revision INTEGER NOT NULL, payload BLOB NOT NULL, updated_at TEXT NOT NULL)`}},
	}
}
