package sqlitestore

import (
	"github.com/rceman/go-sqlite-store/migrate"
	upstream "github.com/rceman/go-sqlite-store/store"
)

func localProjectRetirementMigration() migrate.Migration {
	return migrate.Migration{
		Version: localProjectRetirementMigrationVersion,
		Name:    localProjectRetirementMigrationName,
		Statements: []upstream.Statement{
			{SQL: `ALTER TABLE local_sessions ADD COLUMN project_id TEXT NOT NULL DEFAULT ''`},
			{SQL: `ALTER TABLE local_sessions ADD COLUMN session_type TEXT NOT NULL DEFAULT ''`},
			{SQL: `CREATE TABLE local_project_retirements (project_id TEXT PRIMARY KEY, reason TEXT NOT NULL, retired_at TEXT NOT NULL)`},
			{SQL: `CREATE INDEX local_sessions_project_status_idx ON local_sessions(project_id,status,session_type)`},
			{SQL: `CREATE INDEX local_callback_epochs_project_pending_idx ON local_callback_epochs(project_id,emitted_at,hook_outcome)`},
		},
	}
}
