package sqlitestore

import (
	"github.com/rceman/go-sqlite-store/migrate"
	upstream "github.com/rceman/go-sqlite-store/store"
)

func sharedTaskLifecycleMigration() migrate.Migration {
	return migrate.Migration{
		Version: sharedTaskLifecycleMigrationVersion,
		Name:    sharedTaskLifecycleMigrationName,
		Statements: []upstream.Statement{
			{SQL: `CREATE TABLE IF NOT EXISTS shared_task_lifecycle_events (
id INTEGER PRIMARY KEY AUTOINCREMENT,
operation_id TEXT NOT NULL UNIQUE,
project_id TEXT NOT NULL,
task_id TEXT NOT NULL,
revision INTEGER NOT NULL,
event_kind TEXT NOT NULL,
from_status TEXT NOT NULL,
to_status TEXT NOT NULL,
actor TEXT NOT NULL,
reason TEXT NOT NULL,
contract BLOB NOT NULL,
recorded_at TEXT NOT NULL,
UNIQUE(project_id,task_id,event_kind)
)`},
			{SQL: `CREATE INDEX IF NOT EXISTS shared_task_lifecycle_events_task_idx ON shared_task_lifecycle_events(project_id,task_id,id)`},
		},
	}
}
