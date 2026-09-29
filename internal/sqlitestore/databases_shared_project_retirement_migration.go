package sqlitestore

import (
	"github.com/rceman/go-sqlite-store/migrate"
	upstream "github.com/rceman/go-sqlite-store/store"
)

func sharedProjectRetirementMigration() migrate.Migration {
	return migrate.Migration{
		Version: sharedProjectRetirementMigrationVersion,
		Name:    sharedProjectRetirementMigrationName,
		Statements: []upstream.Statement{
			{SQL: `ALTER TABLE hub_outbox ADD COLUMN cancelled_at TEXT`},
			{SQL: `ALTER TABLE hub_outbox ADD COLUMN cancellation_reason TEXT NOT NULL DEFAULT ''`},
			{SQL: `CREATE TABLE shared_project_retirements (project_id TEXT PRIMARY KEY, revision INTEGER NOT NULL CHECK(revision=1), payload BLOB NOT NULL, retired_at TEXT NOT NULL)`},
			{SQL: `CREATE INDEX hub_outbox_retirement_pending_idx ON hub_outbox(published_at,cancelled_at,next_attempt_at,created_at,id)`},
		},
	}
}
