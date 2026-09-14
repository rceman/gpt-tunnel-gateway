package sqlitestore

import (
	"github.com/rceman/go-sqlite-store/migrate"
	upstream "github.com/rceman/go-sqlite-store/store"
)

func localOperationMigration() migrate.Migration {
	return migrate.Migration{
		Version: localOperationMigrationVersion,
		Name:    localOperationMigrationName,
		Statements: []upstream.Statement{
			{SQL: `CREATE TABLE IF NOT EXISTS local_operation_sequences (project_id TEXT PRIMARY KEY, project_code TEXT NOT NULL, next_number INTEGER NOT NULL CHECK(next_number BETWEEN 1 AND 9007199254740991))`},
			{SQL: `CREATE TABLE IF NOT EXISTS local_operations (operation_id TEXT PRIMARY KEY, project_id TEXT NOT NULL, project_code TEXT NOT NULL, operation_number INTEGER NOT NULL, mutation_id TEXT NOT NULL UNIQUE, kind TEXT NOT NULL, status TEXT NOT NULL, result_payload BLOB, error TEXT NOT NULL DEFAULT '', recovery_reason TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL, updated_at TEXT NOT NULL)`},
			{SQL: `CREATE UNIQUE INDEX IF NOT EXISTS local_operations_mutation_idx ON local_operations(mutation_id)`},
			{SQL: `CREATE INDEX IF NOT EXISTS local_operations_project_idx ON local_operations(project_id,operation_number)`},
			{SQL: `CREATE INDEX IF NOT EXISTS local_operation_sequences_project_idx ON local_operation_sequences(project_id)`},
		},
	}
}
