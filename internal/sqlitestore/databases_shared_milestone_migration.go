package sqlitestore

import (
	"github.com/rceman/go-sqlite-store/migrate"
	upstream "github.com/rceman/go-sqlite-store/store"
)

func sharedMilestoneMigration() migrate.Migration {
	return migrate.Migration{
		Version:    sharedMilestoneMigrationVersion,
		Name:       sharedMilestoneMigrationName,
		Statements: []upstream.Statement{{SQL: `CREATE TABLE IF NOT EXISTS shared_milestones (id TEXT PRIMARY KEY, revision INTEGER NOT NULL, payload BLOB NOT NULL, updated_at TEXT NOT NULL)`}},
	}
}
