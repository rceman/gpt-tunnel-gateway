package sqlitestore

import (
	"github.com/rceman/go-sqlite-store/migrate"
	upstream "github.com/rceman/go-sqlite-store/store"
)

func localSessionBootstrapMigration() migrate.Migration {
	return migrate.Migration{
		Version: localSessionBootstrapMigrationVersion,
		Name:    localSessionBootstrapMigrationName,
		Statements: []upstream.Statement{
			{SQL: `CREATE TABLE IF NOT EXISTS local_session_bootstrap_grants (project_id TEXT PRIMARY KEY, project_code TEXT NOT NULL, gateway_id TEXT NOT NULL, role TEXT NOT NULL, agent_id TEXT NOT NULL DEFAULT '', token TEXT NOT NULL UNIQUE, created_at TEXT NOT NULL, updated_at TEXT NOT NULL)`},
			{SQL: `CREATE UNIQUE INDEX IF NOT EXISTS local_session_bootstrap_grants_token_idx ON local_session_bootstrap_grants(token)`},
		},
	}
}
