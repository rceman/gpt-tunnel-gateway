package sqlitestore

import (
	"github.com/rceman/go-sqlite-store/migrate"
	upstream "github.com/rceman/go-sqlite-store/store"
)

func localBaselineMigration() migrate.Migration {
	return migrate.Migration{Version: localBaselineVersion, Name: localBaselineName, Statements: baselineStatements(localSchemaPlan())}
}

func localSchemaPlan() migrationSchemaPlan {
	return migrationSchemaPlan{
		tables: []migrationTableSpec{
			{name: "local_events", create: `CREATE TABLE IF NOT EXISTS local_events (id TEXT PRIMARY KEY, kind TEXT NOT NULL, payload BLOB NOT NULL, recorded_at TEXT NOT NULL, project_id TEXT NOT NULL DEFAULT '')`, columns: []string{"id", "kind", "payload", "recorded_at", "project_id"}, addColumns: map[string]string{"project_id": `ALTER TABLE local_events ADD COLUMN project_id TEXT NOT NULL DEFAULT ''`}},
			{name: "local_messages", create: `CREATE TABLE IF NOT EXISTS local_messages (id TEXT PRIMARY KEY, session_id TEXT, payload BLOB NOT NULL, recorded_at TEXT NOT NULL)`, columns: []string{"id", "session_id", "payload", "recorded_at"}},
			{name: "local_logs", create: `CREATE TABLE IF NOT EXISTS local_logs (id TEXT PRIMARY KEY, level TEXT NOT NULL, component TEXT NOT NULL, event TEXT NOT NULL, payload BLOB NOT NULL, recorded_at TEXT NOT NULL, project_id TEXT NOT NULL DEFAULT '')`, columns: []string{"id", "level", "component", "event", "payload", "recorded_at", "project_id"}, addColumns: map[string]string{"project_id": `ALTER TABLE local_logs ADD COLUMN project_id TEXT NOT NULL DEFAULT ''`}},
			{name: "local_retention", create: `CREATE TABLE IF NOT EXISTS local_retention (name TEXT PRIMARY KEY, cutoff_at TEXT NOT NULL)`, columns: []string{"name", "cutoff_at"}},
			{name: "local_callback_epochs", create: `CREATE TABLE IF NOT EXISTS local_callback_epochs (epoch_id TEXT PRIMARY KEY, project_id TEXT NOT NULL, agent_id TEXT NOT NULL DEFAULT '', session_key TEXT NOT NULL, armed_at TEXT NOT NULL, busy_seen INTEGER NOT NULL DEFAULT 0, idle_observations INTEGER NOT NULL DEFAULT 0, emitted_at TEXT)`, columns: []string{"epoch_id", "project_id", "agent_id", "session_key", "armed_at", "busy_seen", "idle_observations", "emitted_at"}},
			{name: "local_agents", create: `CREATE TABLE IF NOT EXISTS local_agents (project_id TEXT NOT NULL, agent_id TEXT NOT NULL, payload BLOB NOT NULL, updated_at TEXT NOT NULL, PRIMARY KEY(project_id,agent_id))`, columns: []string{"project_id", "agent_id", "payload", "updated_at"}},
			{name: "local_sessions", create: `CREATE TABLE IF NOT EXISTS local_sessions (session_id TEXT PRIMARY KEY, payload BLOB NOT NULL, updated_at TEXT NOT NULL, status TEXT NOT NULL CHECK(status IN ('active','ended')))`, columns: []string{"session_id", "payload", "updated_at", "status"}},
		},
		statements: []upstream.Statement{
			{SQL: `CREATE INDEX IF NOT EXISTS local_events_kind_idx ON local_events(kind,recorded_at DESC,id DESC)`},
			{SQL: `CREATE INDEX IF NOT EXISTS local_events_recorded_idx ON local_events(recorded_at DESC,id DESC)`},
			{SQL: `CREATE INDEX IF NOT EXISTS local_events_project_idx ON local_events(project_id,recorded_at DESC,id DESC)`},
			{SQL: `CREATE INDEX IF NOT EXISTS local_logs_filter_idx ON local_logs(level,component,recorded_at DESC,id DESC)`},
			{SQL: `CREATE INDEX IF NOT EXISTS local_logs_recorded_idx ON local_logs(recorded_at DESC,id DESC)`},
			{SQL: `CREATE INDEX IF NOT EXISTS local_logs_project_idx ON local_logs(project_id,recorded_at DESC,id DESC)`},
			{SQL: `CREATE INDEX IF NOT EXISTS local_callback_epochs_pending_idx ON local_callback_epochs(emitted_at,armed_at,epoch_id)`},
			{SQL: `CREATE INDEX IF NOT EXISTS local_agents_project_idx ON local_agents(project_id,agent_id)`},
			{SQL: `CREATE INDEX IF NOT EXISTS local_sessions_updated_idx ON local_sessions(updated_at,session_id)`},
		},
	}
}
