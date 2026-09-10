package sqlitestore

import (
	"github.com/rceman/go-sqlite-store/migrate"
	upstream "github.com/rceman/go-sqlite-store/store"
)

func localTokenUsageMigration() migrate.Migration {
	return migrate.Migration{
		Version: localTokenUsageMigrationVersion,
		Name:    localTokenUsageMigrationName,
		Statements: []upstream.Statement{
			{SQL: `CREATE TABLE IF NOT EXISTS local_token_usage_events (event_id TEXT PRIMARY KEY, session_id TEXT NOT NULL, input_tokens INTEGER NOT NULL CHECK(input_tokens >= 0), output_tokens INTEGER NOT NULL CHECK(output_tokens >= 0), total_tokens INTEGER NOT NULL CHECK(total_tokens >= 0), recorded_at TEXT NOT NULL)`},
			{SQL: `CREATE UNIQUE INDEX IF NOT EXISTS local_token_usage_events_session_idx ON local_token_usage_events(session_id,event_id)`},
			{SQL: `CREATE TABLE IF NOT EXISTS local_token_usage (session_id TEXT PRIMARY KEY, request_count INTEGER NOT NULL CHECK(request_count >= 0), input_tokens INTEGER NOT NULL CHECK(input_tokens >= 0), output_tokens INTEGER NOT NULL CHECK(output_tokens >= 0), total_tokens INTEGER NOT NULL CHECK(total_tokens >= 0), updated_at TEXT NOT NULL)`},
		},
	}
}
