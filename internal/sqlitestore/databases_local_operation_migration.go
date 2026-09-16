package sqlitestore

import (
	"context"
	"fmt"

	"github.com/rceman/go-sqlite-store/migrate"
	upstream "github.com/rceman/go-sqlite-store/store"
)

func localOperationMigration() migrate.Migration {
	return migrate.Migration{
		Version: localOperationMigrationVersion,
		Name:    localOperationMigrationName,
		Statements: []upstream.Statement{
			{SQL: `CREATE TABLE IF NOT EXISTS local_operation_sequences (project_id TEXT PRIMARY KEY, project_code TEXT NOT NULL, next_number INTEGER NOT NULL CHECK(next_number BETWEEN 1 AND 9007199254740991))`},
			{SQL: `CREATE TABLE IF NOT EXISTS local_operations (operation_id TEXT PRIMARY KEY, project_id TEXT NOT NULL, project_code TEXT NOT NULL, operation_number INTEGER NOT NULL, mutation_id TEXT NOT NULL UNIQUE, kind TEXT NOT NULL, status TEXT NOT NULL, result_payload BLOB, error TEXT NOT NULL DEFAULT '', recovery_reason TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL, updated_at TEXT NOT NULL, admission_session_id TEXT NOT NULL DEFAULT '', admission_input_sha256 TEXT NOT NULL DEFAULT '')`},
			{SQL: `CREATE UNIQUE INDEX IF NOT EXISTS local_operations_mutation_idx ON local_operations(mutation_id)`},
			{SQL: `CREATE INDEX IF NOT EXISTS local_operations_project_idx ON local_operations(project_id,operation_number)`},
			{SQL: `CREATE INDEX IF NOT EXISTS local_operations_admission_idx ON local_operations(project_id,kind,admission_session_id,admission_input_sha256,operation_number)`},
			{SQL: `CREATE INDEX IF NOT EXISTS local_operation_sequences_project_idx ON local_operation_sequences(project_id)`},
		},
	}
}

func localOperationAdmissionMigrationMarker() migrate.Migration {
	return migrate.Migration{
		Version:    localOperationAdmissionMigrationVersion,
		Name:       localOperationAdmissionMigrationName,
		Statements: []upstream.Statement{{SQL: "SELECT 1"}},
	}
}

func localOperationAdmissionMigration(ctx context.Context, db *upstream.Store) (migrate.Migration, error) {
	if db == nil {
		return migrate.Migration{}, fmt.Errorf("local operation migration store is unavailable")
	}
	rows, err := db.Query(ctx, `SELECT name FROM pragma_table_info(?)`, "local_operations")
	if err != nil {
		return migrate.Migration{}, fmt.Errorf("inspect local operation schema: %w", err)
	}
	columns := make(map[string]bool, len(rows.Rows))
	for _, row := range rows.Rows {
		if len(row) != 1 {
			return migrate.Migration{}, fmt.Errorf("invalid local operation schema row")
		}
		name, ok := row[0].(string)
		if !ok || name == "" {
			return migrate.Migration{}, fmt.Errorf("invalid local operation schema column")
		}
		columns[name] = true
	}
	var operationSpec migrationTableSpec
	for _, table := range localSchemaPlan().tables {
		if table.name == "local_operations" {
			operationSpec = table
			break
		}
	}
	if operationSpec.name == "" {
		return migrate.Migration{}, fmt.Errorf("local operation schema plan is missing")
	}
	statements := make([]upstream.Statement, 0, 3)
	for _, column := range []string{"admission_session_id", "admission_input_sha256"} {
		if columns[column] {
			continue
		}
		sql, ok := operationSpec.addColumns[column]
		if !ok {
			return migrate.Migration{}, fmt.Errorf("local operation schema plan is missing column %q", column)
		}
		statements = append(statements, upstream.Statement{SQL: sql})
	}
	statements = append(statements, upstream.Statement{SQL: `CREATE INDEX IF NOT EXISTS local_operations_admission_idx ON local_operations(project_id,kind,admission_session_id,admission_input_sha256,operation_number)`})
	return migrate.Migration{
		Version:    localOperationAdmissionMigrationVersion,
		Name:       localOperationAdmissionMigrationName,
		Statements: statements,
	}, nil
}
