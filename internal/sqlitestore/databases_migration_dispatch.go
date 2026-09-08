package sqlitestore

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/rceman/go-sqlite-store/migrate"
	upstream "github.com/rceman/go-sqlite-store/store"
)

type migrationSchemaPlan struct {
	tables       []migrationTableSpec
	legacyTables []string
	statements   []upstream.Statement
	legacy       func(map[string]bool) []upstream.Statement
}

type migrationTableSpec struct {
	name       string
	create     string
	columns    []string
	addColumns map[string]string
}

func applySharedMigrations(ctx context.Context, db *upstream.Store) error {
	return applyManagedMigrations(ctx, db, sharedBaselineMigration(), sharedBridgeVersion, sharedBridgeName, sharedSchemaPlan(), validateSharedMigrationHistory)
}

func applyLocalMigrations(ctx context.Context, db *upstream.Store) error {
	return applyManagedMigrations(ctx, db, localBaselineMigration(), localBridgeVersion, localBridgeName, localSchemaPlan(), validateLocalMigrationHistory)
}

func applyManagedMigrations(ctx context.Context, db *upstream.Store, baseline migrate.Migration, bridgeVersion int64, bridgeName string, plan migrationSchemaPlan, validate func(map[int64]string) error) error {
	exists, markers, err := readMigrationMarkers(ctx, db)
	if err != nil {
		return err
	}
	if !exists || len(markers) == 0 {
		objects, err := managedObjectCount(ctx, db)
		if err != nil {
			return err
		}
		if objects != 0 {
			return fmt.Errorf("migration history is missing beside %d managed schema objects", objects)
		}
		if err := validateActiveMigrationIdentity(baseline); err != nil {
			return err
		}
		if err := migrate.Apply(ctx, db, []migrate.Migration{baseline}, migrate.Options{}); err != nil {
			return err
		}
		return nil
	}
	if err := validate(markers); err != nil {
		return err
	}
	if baseline.Version == sharedBaselineVersion {
		if err := validateSharedLegacySchema(ctx, db, markers); err != nil {
			return err
		}
	}
	if baseline.Version == localBaselineVersion {
		if err := validateLocalLegacySchema(ctx, db, markers); err != nil {
			return err
		}
	}
	if _, ok := markers[baseline.Version]; ok {
		return nil
	}
	if _, ok := markers[bridgeVersion]; ok {
		return nil
	}
	if err := validateActiveMigrationIdentity(baseline); err != nil {
		return err
	}
	statements, err := compatibilityStatements(ctx, db, plan)
	if err != nil {
		return err
	}
	bridge := migrate.Migration{Version: bridgeVersion, Name: bridgeName, Statements: statements}
	if err := validateActiveMigrationIdentity(bridge); err != nil {
		return err
	}
	if err := migrate.Apply(ctx, db, []migrate.Migration{bridge}, migrate.Options{}); err != nil {
		return err
	}
	return nil
}

func validateSharedLegacySchema(ctx context.Context, db *upstream.Store, markers map[int64]string) error {
	if markers[12] == sharedLifecycleMigrationName {
		if err := requireLegacyTable(ctx, db, "shared_entity_sequences", []string{"entity_type", "project_id", "project_code", "next_number"}); err != nil {
			return err
		}
		if err := requireLegacyTable(ctx, db, "shared_entity_revisions", []string{"entity_type", "entity_id", "project_id", "revision", "mutation_kind", "actor", "reason", "changed_fields", "payload", "recorded_at"}); err != nil {
			return err
		}
		rows, err := db.Query(ctx, `SELECT type FROM sqlite_master WHERE type='index' AND name=?`, "shared_entity_revisions_project_idx")
		if err != nil || len(rows.Rows) != 1 {
			return fmt.Errorf("Shared lifecycle v12 requires missing index shared_entity_revisions_project_idx: %v", err)
		}
	}
	if markers[12] == "gpt_tunnel_shared_agents_v12" {
		if err := requireLegacyTable(ctx, db, "shared_agents", []string{"id", "revision", "payload", "updated_at"}); err != nil {
			return err
		}
	}
	if markers[13] != "" {
		if err := requireLegacyTable(ctx, db, "shared_watcher_guides", []string{"id", "revision", "payload", "updated_at"}); err != nil {
			return err
		}
	}
	if markers[14] != "" {
		if err := requireLegacyTable(ctx, db, "shared_journal_sequences", []string{"project_id", "project_code", "next_event_number"}); err != nil {
			return err
		}
		if err := requireLegacyTable(ctx, db, "shared_journal_supersessions", []string{"target_id", "operation_id", "created_at"}); err != nil {
			return err
		}
	}
	if markers[15] != "" {
		if err := requireLegacyTable(ctx, db, "shared_integration_operations", []string{"id", "revision", "payload", "updated_at"}); err != nil {
			return err
		}
	}
	if markers[16] != "" {
		columns, err := tableColumns(ctx, db, "hub_outbox")
		if err != nil {
			return err
		}
		if !columns["project_id"] {
			return fmt.Errorf("Shared v16 marker requires hub_outbox.project_id")
		}
	}
	return nil
}

func validateLocalLegacySchema(ctx context.Context, db *upstream.Store, markers map[int64]string) error {
	if _, ok := markers[2]; !ok {
		return nil
	}
	if err := requireLegacyTable(ctx, db, "local_inter_session_messages", []string{"id", "project_id", "source_session_id", "target_session_id", "topic", "body", "tags", "created_at", "expires_at"}); err != nil {
		return err
	}
	if _, ok := markers[3]; ok {
		rows, err := db.Query(ctx, `SELECT type FROM sqlite_master WHERE type='index' AND name=?`, "local_inter_session_messages_expiry_idx")
		if err != nil || len(rows.Rows) != 1 {
			return fmt.Errorf("Local v3 marker requires missing index local_inter_session_messages_expiry_idx: %v", err)
		}
	}
	return nil
}

func requireLegacyTable(ctx context.Context, db *upstream.Store, table string, columns []string) error {
	rows, err := db.Query(ctx, `SELECT type FROM sqlite_master WHERE name=?`, table)
	if err != nil || len(rows.Rows) != 1 || rows.Rows[0][0] != "table" {
		return fmt.Errorf("legacy marker requires table %s: %v", table, err)
	}
	actual, err := tableColumns(ctx, db, table)
	if err != nil {
		return err
	}
	for _, column := range columns {
		if !actual[column] {
			return fmt.Errorf("legacy table %s is missing required column %s", table, column)
		}
	}
	return nil
}

func readMigrationMarkers(ctx context.Context, db *upstream.Store) (bool, map[int64]string, error) {
	rows, err := db.Query(ctx, `SELECT version,name FROM schema_migrations ORDER BY version`)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "no such table") {
			return false, nil, nil
		}
		return false, nil, fmt.Errorf("read migration history: %w", err)
	}
	markers := make(map[int64]string, len(rows.Rows))
	for _, row := range rows.Rows {
		if len(row) != 2 {
			return true, nil, fmt.Errorf("invalid migration history row shape")
		}
		version, ok := row[0].(int64)
		if !ok || version <= 0 {
			return true, nil, fmt.Errorf("invalid migration history version %T", row[0])
		}
		name, ok := row[1].(string)
		if !ok || name == "" {
			return true, nil, fmt.Errorf("invalid migration history name %T", row[1])
		}
		if _, exists := markers[version]; exists {
			return true, nil, fmt.Errorf("duplicate migration history version %d", version)
		}
		markers[version] = name
	}
	return true, markers, nil
}

func managedObjectCount(ctx context.Context, db *upstream.Store) (int64, error) {
	rows, err := db.Query(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE name NOT LIKE 'sqlite_%' AND name <> 'schema_migrations'`)
	if err != nil {
		return 0, fmt.Errorf("inspect managed schema: %w", err)
	}
	if len(rows.Rows) != 1 || len(rows.Rows[0]) != 1 {
		return 0, fmt.Errorf("invalid managed schema inspection result")
	}
	count, ok := rows.Rows[0][0].(int64)
	if !ok {
		return 0, fmt.Errorf("invalid managed schema count %T", rows.Rows[0][0])
	}
	return count, nil
}

func compatibilityStatements(ctx context.Context, db *upstream.Store, plan migrationSchemaPlan) ([]upstream.Statement, error) {
	statements := make([]upstream.Statement, 0, len(plan.tables)+len(plan.statements)+8)
	known := make(map[string]bool, len(plan.tables))
	for _, name := range plan.legacyTables {
		rows, err := db.Query(ctx, `SELECT type FROM sqlite_master WHERE name=?`, name)
		if err != nil {
			return nil, fmt.Errorf("inspect legacy table %s: %w", name, err)
		}
		known[name] = len(rows.Rows) != 0
	}
	for _, table := range plan.tables {
		rows, err := db.Query(ctx, `SELECT type FROM sqlite_master WHERE name=?`, table.name)
		if err != nil {
			return nil, fmt.Errorf("inspect table %s: %w", table.name, err)
		}
		exists := len(rows.Rows) != 0
		known[table.name] = exists
		if !exists {
			statements = append(statements, upstream.Statement{SQL: table.create})
			continue
		}
		columns, err := tableColumns(ctx, db, table.name)
		if err != nil {
			return nil, err
		}
		for _, column := range table.columns {
			if columns[column] {
				continue
			}
			add, ok := table.addColumns[column]
			if !ok {
				return nil, fmt.Errorf("legacy table %s is missing required column %s", table.name, column)
			}
			statements = append(statements, upstream.Statement{SQL: add})
		}
	}
	statements = append(statements, plan.statements...)
	if plan.legacy != nil {
		statements = append(statements, plan.legacy(known)...)
	}
	return statements, nil
}

func validateActiveMigrationIdentity(migration migrate.Migration) error {
	version := strconv.FormatInt(migration.Version, 10)
	if len(version) != len("200601021504") {
		return fmt.Errorf("active migration %q has non-timestamp version %d", migration.Name, migration.Version)
	}
	if _, err := time.ParseInLocation("200601021504", version, time.UTC); err != nil {
		return fmt.Errorf("active migration %q has invalid UTC timestamp version %d: %w", migration.Name, migration.Version, err)
	}
	if migration.Name == "" || strings.Contains(migration.Name, "_v") {
		return fmt.Errorf("active migration %d has invalid description %q", migration.Version, migration.Name)
	}
	return nil
}

func baselineStatements(plan migrationSchemaPlan) []upstream.Statement {
	statements := make([]upstream.Statement, 0, len(plan.tables)+len(plan.statements))
	for _, table := range plan.tables {
		statements = append(statements, upstream.Statement{SQL: table.create})
	}
	return append(statements, plan.statements...)
}

func tableColumns(ctx context.Context, db *upstream.Store, table string) (map[string]bool, error) {
	rows, err := db.Query(ctx, fmt.Sprintf("PRAGMA table_info('%s')", table))
	if err != nil {
		return nil, fmt.Errorf("inspect columns %s: %w", table, err)
	}
	columns := make(map[string]bool, len(rows.Rows))
	for _, row := range rows.Rows {
		if len(row) < 2 {
			return nil, fmt.Errorf("invalid column metadata for %s", table)
		}
		name, ok := row[1].(string)
		if !ok || name == "" {
			return nil, fmt.Errorf("invalid column name metadata for %s", table)
		}
		columns[name] = true
	}
	return columns, nil
}
