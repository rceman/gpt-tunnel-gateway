package sqlitestore

import (
	"github.com/rceman/go-sqlite-store/migrate"
	upstream "github.com/rceman/go-sqlite-store/store"
)

func localMessageMigration() migrate.Migration {
	return migrate.Migration{
		Version: localMessageMigrationVersion,
		Name:    localMessageMigrationName,
		Statements: []upstream.Statement{
			{SQL: `CREATE TABLE IF NOT EXISTS plaw_message_sequences (project_id TEXT PRIMARY KEY, project_code TEXT NOT NULL, next_number INTEGER NOT NULL CHECK(next_number BETWEEN 1 AND 9007199254740991))`},
			{SQL: `CREATE TABLE IF NOT EXISTS plaw_messages (id TEXT PRIMARY KEY, project_id TEXT NOT NULL, project_code TEXT NOT NULL, from_role TEXT NOT NULL, from_session TEXT NOT NULL, to_role TEXT NOT NULL, body BLOB NOT NULL, title TEXT NOT NULL DEFAULT '', in_reply_to TEXT NOT NULL DEFAULT '', state TEXT NOT NULL CHECK(state IN ('unread','read','cancelled')), created_at TEXT NOT NULL, read_at TEXT, read_session TEXT NOT NULL DEFAULT '', cancelled_at TEXT)`},
			{SQL: `CREATE INDEX IF NOT EXISTS plaw_messages_recipient_idx ON plaw_messages(project_id,to_role,state,created_at,id)`},
			{SQL: `CREATE INDEX IF NOT EXISTS plaw_messages_sender_idx ON plaw_messages(project_id,from_session,state,created_at,id)`},
			{SQL: `CREATE INDEX IF NOT EXISTS plaw_messages_reply_idx ON plaw_messages(project_id,in_reply_to)`},
		},
	}
}
