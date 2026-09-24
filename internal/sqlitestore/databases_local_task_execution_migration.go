package sqlitestore

import (
	"github.com/rceman/go-sqlite-store/migrate"
	upstream "github.com/rceman/go-sqlite-store/store"
)

func localTaskExecutionMigration() migrate.Migration {
	return migrate.Migration{
		Version: localTaskExecutionMigrationVersion,
		Name:    localTaskExecutionMigrationName,
		Statements: []upstream.Statement{
			{SQL: `CREATE TABLE IF NOT EXISTS local_upgrade_migrations (migration_id TEXT PRIMARY KEY, state TEXT NOT NULL, updated_at TEXT NOT NULL)`},
			{SQL: `CREATE TABLE IF NOT EXISTS local_task_execution_states (
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
)`},
			{SQL: `CREATE TABLE IF NOT EXISTS local_task_execution_phases (
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
)`},
			{SQL: `CREATE TABLE IF NOT EXISTS local_task_execution_verifications (
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
			{SQL: `CREATE INDEX IF NOT EXISTS local_task_execution_project_idx ON local_task_execution_states(project_id,task_id)`},
			{SQL: `CREATE INDEX IF NOT EXISTS local_task_execution_phase_project_idx ON local_task_execution_phases(project_id,task_id,id)`},
			{SQL: `CREATE INDEX IF NOT EXISTS local_task_execution_verification_project_idx ON local_task_execution_verifications(project_id,task_id,attempt_revision)`},
		},
	}
}
