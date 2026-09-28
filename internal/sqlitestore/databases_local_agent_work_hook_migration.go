package sqlitestore

import (
	"github.com/rceman/go-sqlite-store/migrate"
	upstream "github.com/rceman/go-sqlite-store/store"
)

func localAgentWorkHookMigration() migrate.Migration {
	return migrate.Migration{
		Version: localAgentWorkHookMigrationVersion,
		Name:    localAgentWorkHookMigrationName,
		Statements: []upstream.Statement{
			{SQL: `ALTER TABLE local_callback_epochs ADD COLUMN session_id TEXT NOT NULL DEFAULT ''`},
			{SQL: `ALTER TABLE local_callback_epochs ADD COLUMN operation_id TEXT`},
			{SQL: `ALTER TABLE local_callback_epochs ADD COLUMN hook_outcome TEXT NOT NULL DEFAULT ''`},
			{SQL: `ALTER TABLE local_callback_epochs ADD COLUMN hook_completed_at TEXT`},
			{SQL: `CREATE INDEX IF NOT EXISTS local_callback_epochs_operation_idx ON local_callback_epochs(operation_id)`},
		},
	}
}
