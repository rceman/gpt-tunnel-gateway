package sqlitestore

import "github.com/rceman/go-sqlite-store/migrate"

func sharedBaselineMigration() migrate.Migration {
	return migrate.Migration{Version: sharedBaselineVersion, Name: sharedBaselineName, Statements: baselineStatements(sharedSchemaPlan())}
}

func sharedSchemaPlan() migrationSchemaPlan {
	return migrationSchemaPlan{
		tables:     sharedSchemaTables(),
		statements: sharedBaselineStatements(),
	}
}
