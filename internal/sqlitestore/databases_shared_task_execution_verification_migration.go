package sqlitestore

import (
	"github.com/rceman/go-sqlite-store/migrate"
	upstream "github.com/rceman/go-sqlite-store/store"
)

func sharedTaskExecutionVerificationMigration() migrate.Migration {
	return migrate.Migration{
		Version: sharedTaskExecutionVerificationMigrationVersion,
		Name:    sharedTaskExecutionVerificationMigrationName,
		Statements: []upstream.Statement{
			{SQL: `CREATE TABLE IF NOT EXISTS shared_task_execution_verifications (
id INTEGER PRIMARY KEY AUTOINCREMENT,
project_id TEXT NOT NULL,
task_id TEXT NOT NULL,
operation_id TEXT NOT NULL,
attempt_revision INTEGER NOT NULL,
outcome TEXT NOT NULL,
receipt_json TEXT NOT NULL,
created_at TEXT NOT NULL,
UNIQUE(project_id,task_id,operation_id,attempt_revision)
)`},
			{SQL: `UPDATE shared_task_execution_states SET status='ready_for_verification' WHERE status='ready_for_integration'`},
		},
	}
}
