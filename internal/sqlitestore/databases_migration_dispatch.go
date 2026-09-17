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
	tables     []migrationTableSpec
	statements []upstream.Statement
}

type migrationTableSpec struct {
	name       string
	create     string
	columns    []string
	addColumns map[string]string
}

func applySharedMigrations(ctx context.Context, db *upstream.Store) error {
	baseline := sharedBaselineMigration()
	exists, markers, err := readMigrationMarkers(ctx, db)
	if err != nil {
		return err
	}
	if !exists || len(markers) == 0 {
		if err := applyActiveMigrations(ctx, db, baseline); err != nil {
			return err
		}
		markers = map[int64]string{}
	}
	summary := migrate.Migration{Version: sharedTaskSummaryMigrationVersion, Name: sharedTaskSummaryMigrationName, Statements: []upstream.Statement{{SQL: "SELECT 1"}}}
	if name, applied := markers[sharedTaskSummaryMigrationVersion]; applied {
		if name != sharedTaskSummaryMigrationName {
			return fmt.Errorf("unsupported migration marker %d/%q", sharedTaskSummaryMigrationVersion, name)
		}
	} else {
		summary, err = sharedTaskSummaryMigration(ctx, db)
		if err != nil {
			return err
		}
	}
	sequence := migrate.Migration{
		Version:    sharedTaskSequenceMigrationVersion,
		Name:       sharedTaskSequenceMigrationName,
		Statements: []upstream.Statement{{SQL: "SELECT 1"}},
	}
	if _, applied := markers[sharedTaskSequenceMigrationVersion]; !applied {
		sequence, err = sharedTaskSequenceMigration(ctx, db)
		if err != nil {
			return err
		}
	}
	adrSummary := migrate.Migration{Version: sharedADRSummaryMigrationVersion, Name: sharedADRSummaryMigrationName, Statements: []upstream.Statement{{SQL: "SELECT 1"}}}
	if name, applied := markers[sharedADRSummaryMigrationVersion]; applied {
		if name != sharedADRSummaryMigrationName {
			return fmt.Errorf("unsupported migration marker %d/%q", sharedADRSummaryMigrationVersion, name)
		}
	} else {
		adrSummary, err = sharedADRSummaryMigration(ctx, db)
		if err != nil {
			return err
		}
	}
	execution := sharedTaskExecutionMigration()
	phases := sharedTaskExecutionPhasesMigration()
	verification := sharedTaskExecutionVerificationMigration()
	lifecycle := sharedTaskLifecycleMigration()
	lifecycleEvents := sharedLifecycleEventMigration()
	released := []migrate.Migration{baseline, summary, sequence, execution, phases, verification, lifecycle, adrSummary, lifecycleEvents}
	if name, applied := markers[sharedTaskLifecycleHardCutMigrationVersion]; applied {
		if name != sharedTaskLifecycleHardCutMigrationName {
			return fmt.Errorf("unsupported migration marker %d/%q", sharedTaskLifecycleHardCutMigrationVersion, name)
		}
		return applyActiveMigrations(ctx, db, append(append([]migrate.Migration(nil), released...), sharedTaskLifecycleHardCutMigrationMarker())...)
	}
	if err := applyActiveMigrations(ctx, db, released...); err != nil {
		return err
	}
	hardCut, err := sharedTaskLifecycleHardCutMigration(ctx, db)
	if err != nil {
		return err
	}
	return applyActiveMigrations(ctx, db, append(append([]migrate.Migration(nil), released...), hardCut)...)
}

func applyLocalMigrations(ctx context.Context, db *upstream.Store) error {
	base := []migrate.Migration{localBaselineMigration(), localTokenUsageMigration(), localOperationMigration()}
	exists, markers, err := readMigrationMarkers(ctx, db)
	if err != nil {
		return err
	}
	if exists {
		if name, applied := markers[localOperationAdmissionMigrationVersion]; applied {
			if name != localOperationAdmissionMigrationName {
				return fmt.Errorf("unsupported migration marker %d/%q", localOperationAdmissionMigrationVersion, name)
			}
			migrations := append(append([]migrate.Migration(nil), base...), localOperationAdmissionMigrationMarker())
			return applyActiveMigrations(ctx, db, migrations...)
		}
	}
	if err := applyActiveMigrations(ctx, db, base...); err != nil {
		return err
	}
	admission, err := localOperationAdmissionMigration(ctx, db)
	if err != nil {
		return err
	}
	migrations := append(append([]migrate.Migration(nil), base...), admission)
	return applyActiveMigrations(ctx, db, migrations...)
}

func applyActiveMigrations(ctx context.Context, db *upstream.Store, migrations ...migrate.Migration) error {
	if len(migrations) == 0 {
		return fmt.Errorf("no active migrations configured")
	}
	active := make(map[int64]string, len(migrations))
	for _, migration := range migrations {
		if err := validateActiveMigrationIdentity(migration); err != nil {
			return err
		}
		if _, exists := active[migration.Version]; exists {
			return fmt.Errorf("duplicate active migration version %d", migration.Version)
		}
		active[migration.Version] = migration.Name
	}
	baseline := migrations[0]
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
		if err := migrate.Apply(ctx, db, migrations, migrate.Options{}); err != nil {
			return err
		}
		return nil
	}
	if markers[baseline.Version] != baseline.Name {
		return fmt.Errorf("unsupported migration history: baseline marker %d/%q is required", baseline.Version, baseline.Name)
	}
	for version, name := range markers {
		if active[version] != name {
			return fmt.Errorf("unsupported migration marker %d/%q", version, name)
		}
	}
	if err := migrate.Apply(ctx, db, migrations, migrate.Options{}); err != nil {
		return err
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
