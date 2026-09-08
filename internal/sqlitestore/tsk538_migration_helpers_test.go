package sqlitestore

import (
	"context"

	"github.com/rceman/go-sqlite-store/migrate"
	upstream "github.com/rceman/go-sqlite-store/store"
)

func applyTSK538SharedLegacyPrefix(ctx context.Context, db *upstream.Store) error {
	names := []string{"gpt_tunnel_shared_authority_v1", sharedReplicationMigrationName, sharedCutoverMigrationName, "gpt_tunnel_shared_project_identifiers_v1", "gpt_tunnel_shared_train_admission_v1", "gpt_tunnel_shared_train_admission_update_guard_v1", sharedTaskSequenceMigrationName, sharedIntegrationCurrentMigrationName, sharedBootstrapMigrationName, sharedADROutboxMigrationName, sharedProjectConfigurationMigrationName}
	migrations := make([]migrate.Migration, 0, len(names))
	for i, name := range names {
		statements := []upstream.Statement{{SQL: "SELECT 1"}}
		if i == 0 {
			statements = []upstream.Statement{
				{SQL: `CREATE TABLE shared_adrs (id TEXT PRIMARY KEY, revision INTEGER NOT NULL, payload BLOB NOT NULL, updated_at TEXT NOT NULL)`},
				{SQL: `CREATE TABLE shared_project_identifiers (project_id TEXT PRIMARY KEY, project_code TEXT NOT NULL, next_task_number INTEGER NOT NULL, next_adr_number INTEGER NOT NULL, next_rule_number INTEGER NOT NULL, next_journal_number INTEGER NOT NULL, next_train_number INTEGER NOT NULL)`},
				{SQL: `CREATE TABLE shared_task_sequences (project_id TEXT PRIMARY KEY, project_code TEXT NOT NULL, next_task_number INTEGER NOT NULL)`},
				{SQL: `CREATE TABLE shared_adr_sequences (project_id TEXT PRIMARY KEY, project_code TEXT NOT NULL, next_adr_number INTEGER NOT NULL)`},
				{SQL: `CREATE TABLE hub_outbox (id TEXT PRIMARY KEY, entity_type TEXT NOT NULL, entity_id TEXT NOT NULL, revision INTEGER NOT NULL, kind TEXT NOT NULL, payload BLOB NOT NULL, created_at TEXT NOT NULL, published_at TEXT)`},
			}
		}
		migrations = append(migrations, migrate.Migration{Version: int64(i + 1), Name: name, Statements: statements})
	}
	return migrate.Apply(ctx, db, migrations, migrate.Options{})
}

func applyTSK538DeployedSharedHistory(ctx context.Context, db *upstream.Store) error {
	if err := applyTSK538SharedLegacyPrefix(ctx, db); err != nil {
		return err
	}
	return migrate.Apply(ctx, db, []migrate.Migration{
		{Version: 12, Name: "gpt_tunnel_shared_agents_v12", Statements: []upstream.Statement{{SQL: `CREATE TABLE shared_agents (id TEXT PRIMARY KEY, revision INTEGER NOT NULL, payload BLOB NOT NULL, updated_at TEXT NOT NULL)`}}},
		{Version: 13, Name: "gpt_tunnel_shared_watcher_guides_v13", Statements: []upstream.Statement{{SQL: `CREATE TABLE shared_watcher_guides (id TEXT PRIMARY KEY, revision INTEGER NOT NULL, payload BLOB NOT NULL, updated_at TEXT NOT NULL)`}}},
		{Version: 14, Name: "gpt_tunnel_shared_journal_sequences_v14", Statements: []upstream.Statement{{SQL: `CREATE TABLE shared_journal_sequences (project_id TEXT PRIMARY KEY, project_code TEXT NOT NULL, next_event_number INTEGER NOT NULL)`}, {SQL: `CREATE TABLE shared_journal_supersessions (target_id TEXT PRIMARY KEY, operation_id TEXT NOT NULL, created_at TEXT NOT NULL)`}}},
		{Version: 15, Name: "gpt_tunnel_shared_integration_operations_v15", Statements: []upstream.Statement{{SQL: `CREATE TABLE shared_integration_operations (id TEXT PRIMARY KEY, revision INTEGER NOT NULL, payload BLOB NOT NULL, updated_at TEXT NOT NULL)`}}},
		{Version: 16, Name: "gpt_tunnel_shared_outbox_project_v16", Statements: []upstream.Statement{{SQL: `ALTER TABLE hub_outbox ADD COLUMN project_id TEXT NOT NULL DEFAULT ''`}}},
	}, migrate.Options{})
}

func applyTSK538LocalLegacyHistory(ctx context.Context, db *upstream.Store) error {
	migrations := []migrate.Migration{
		{Version: 1, Name: localOperationalMigrationName, Statements: []upstream.Statement{{SQL: `CREATE TABLE local_events (id TEXT PRIMARY KEY, kind TEXT NOT NULL, payload BLOB NOT NULL, recorded_at TEXT NOT NULL)`}, {SQL: `CREATE TABLE local_messages (id TEXT PRIMARY KEY, session_id TEXT, payload BLOB NOT NULL, recorded_at TEXT NOT NULL)`}, {SQL: `CREATE TABLE local_logs (id TEXT PRIMARY KEY, level TEXT NOT NULL, component TEXT NOT NULL, event TEXT NOT NULL, payload BLOB NOT NULL, recorded_at TEXT NOT NULL)`}, {SQL: `CREATE TABLE local_retention (name TEXT PRIMARY KEY, cutoff_at TEXT NOT NULL)`}}},
		{Version: 2, Name: legacyLocalInterSessionMessagesName, Statements: []upstream.Statement{{SQL: `CREATE TABLE local_inter_session_messages (id TEXT PRIMARY KEY, project_id TEXT NOT NULL, source_session_id TEXT NOT NULL, target_session_id TEXT NOT NULL, topic TEXT NOT NULL, body TEXT NOT NULL, tags BLOB NOT NULL, created_at TEXT NOT NULL, expires_at TEXT NOT NULL)`}}},
		{Version: 3, Name: legacyLocalHistoryIndexesName, Statements: []upstream.Statement{{SQL: `CREATE INDEX local_events_kind_idx ON local_events(kind,recorded_at DESC,id DESC)`}, {SQL: `CREATE INDEX local_events_recorded_idx ON local_events(recorded_at DESC,id DESC)`}, {SQL: `CREATE INDEX local_logs_filter_idx ON local_logs(level,component,recorded_at DESC,id DESC)`}, {SQL: `CREATE INDEX local_logs_recorded_idx ON local_logs(recorded_at DESC,id DESC)`}, {SQL: `CREATE INDEX local_inter_session_messages_expiry_idx ON local_inter_session_messages(expires_at)`}}},
		{Version: 4, Name: legacyLocalHistoryProjectIndexesName, Statements: []upstream.Statement{{SQL: `ALTER TABLE local_events ADD COLUMN project_id TEXT NOT NULL DEFAULT ''`}, {SQL: `ALTER TABLE local_logs ADD COLUMN project_id TEXT NOT NULL DEFAULT ''`}, {SQL: `CREATE INDEX local_events_project_idx ON local_events(project_id,recorded_at DESC,id DESC)`}, {SQL: `CREATE INDEX local_logs_project_idx ON local_logs(project_id,recorded_at DESC,id DESC)`}}},
		{Version: 5, Name: legacyLocalHistoryProjectBackfillName, Statements: []upstream.Statement{{SQL: `UPDATE local_events SET project_id='' WHERE project_id IS NULL`}, {SQL: `UPDATE local_logs SET project_id='' WHERE project_id IS NULL`}}},
	}
	return migrate.Apply(ctx, db, migrations, migrate.Options{})
}
