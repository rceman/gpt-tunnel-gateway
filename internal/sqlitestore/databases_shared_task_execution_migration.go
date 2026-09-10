package sqlitestore

import (
	"github.com/rceman/go-sqlite-store/migrate"
	upstream "github.com/rceman/go-sqlite-store/store"
)

func sharedTaskExecutionMigration() migrate.Migration {
	return migrate.Migration{
		Version: sharedTaskExecutionMigrationVersion,
		Name:    sharedTaskExecutionMigrationName,
		Statements: []upstream.Statement{{SQL: `CREATE TABLE IF NOT EXISTS shared_task_execution_states (
task_id TEXT PRIMARY KEY,
project_id TEXT NOT NULL,
	task_revision INTEGER NOT NULL,
	task_revision_sha256 TEXT NOT NULL,
	status TEXT NOT NULL,
	stage TEXT NOT NULL,
	worktree TEXT NOT NULL,
	base_head_sha TEXT NOT NULL,
	head_sha TEXT NOT NULL,
	branch TEXT NOT NULL,
	agent TEXT NOT NULL,
execution_revision INTEGER NOT NULL,
updated_at TEXT NOT NULL
)`}},
	}
}
