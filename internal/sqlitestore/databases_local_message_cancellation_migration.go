package sqlitestore

import (
	"github.com/rceman/go-sqlite-store/migrate"
	upstream "github.com/rceman/go-sqlite-store/store"
)

func localMessageCancellationMigration() migrate.Migration {
	return migrate.Migration{
		Version: localMessageCancellationMigrationVersion,
		Name:    localMessageCancellationMigrationName,
		Statements: []upstream.Statement{
			{SQL: `ALTER TABLE plaw_messages ADD COLUMN cancelled_by TEXT NOT NULL DEFAULT ''`},
		},
	}
}
