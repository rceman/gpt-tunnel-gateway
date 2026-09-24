package sqlitestore

import (
	"github.com/rceman/go-sqlite-store/migrate"
	upstream "github.com/rceman/go-sqlite-store/store"
)

func sharedUpgradeMigration() migrate.Migration {
	return migrate.Migration{
		Version: sharedUpgradeMigrationVersion,
		Name:    sharedUpgradeMigrationName,
		Statements: []upstream.Statement{{SQL: `CREATE TABLE IF NOT EXISTS shared_upgrade_migrations (
 migration_id TEXT PRIMARY KEY,
 state TEXT NOT NULL,
 updated_at TEXT NOT NULL
)`}},
	}
}
