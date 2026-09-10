package sqlitestore

import (
	"github.com/rceman/go-sqlite-store/migrate"
	upstream "github.com/rceman/go-sqlite-store/store"
)

func sharedTaskExecutionPhasesMigration() migrate.Migration {
	return migrate.Migration{
		Version: sharedTaskExecutionPhaseMigrationVersion,
		Name:    sharedTaskExecutionPhaseMigrationName,
		Statements: []upstream.Statement{{SQL: `CREATE TABLE IF NOT EXISTS shared_task_execution_phases (
id INTEGER PRIMARY KEY AUTOINCREMENT,
task_id TEXT NOT NULL,
project_id TEXT NOT NULL,
execution_revision INTEGER NOT NULL,
stage TEXT NOT NULL,
status TEXT NOT NULL,
head_sha TEXT NOT NULL,
branch TEXT NOT NULL,
task_revision_sha256 TEXT NOT NULL,
event_kind TEXT NOT NULL,
decision TEXT,
comment TEXT,
created_at TEXT NOT NULL,
UNIQUE(project_id,task_id,execution_revision)
)`}},
	}
}
