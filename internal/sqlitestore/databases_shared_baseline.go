package sqlitestore

import "github.com/rceman/go-sqlite-store/migrate"

func sharedBaselineMigration() migrate.Migration {
	return migrate.Migration{Version: sharedBaselineVersion, Name: sharedBaselineName, Statements: baselineStatements(sharedSchemaPlan())}
}

func sharedSchemaPlan() migrationSchemaPlan {
	return migrationSchemaPlan{
		legacyTables: []string{"shared_task_sequences", "shared_adr_sequences"},
		tables:       sharedSchemaTables(),
		statements:   sharedBaselineStatements(),
		legacy:       sharedLegacyStatements,
	}
}
