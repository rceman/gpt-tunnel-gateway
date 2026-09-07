package sqlitestore

import (
	"errors"

	"github.com/rceman/go-sqlite-store/migrate"
	"github.com/rceman/go-sqlite-store/store"
)

var sharedMigrations = []migrate.Migration{{
	Version: 1,
	Name:    "gpt_tunnel_shared_authority_v1",
	Statements: []store.Statement{
		{SQL: `CREATE TABLE IF NOT EXISTS shared_tasks (id TEXT PRIMARY KEY, revision INTEGER NOT NULL, payload BLOB NOT NULL, updated_at TEXT NOT NULL)`},
		{SQL: `CREATE TABLE IF NOT EXISTS shared_trains (id TEXT PRIMARY KEY, revision INTEGER NOT NULL, payload BLOB NOT NULL, updated_at TEXT NOT NULL)`},
		{SQL: `CREATE TABLE IF NOT EXISTS shared_adrs (id TEXT PRIMARY KEY, revision INTEGER NOT NULL, payload BLOB NOT NULL, updated_at TEXT NOT NULL)`},
		{SQL: `CREATE TABLE IF NOT EXISTS shared_rules (id TEXT PRIMARY KEY, revision INTEGER NOT NULL, payload BLOB NOT NULL, updated_at TEXT NOT NULL)`},
		{SQL: `CREATE TABLE IF NOT EXISTS shared_journals (id TEXT PRIMARY KEY, revision INTEGER NOT NULL, payload BLOB NOT NULL, updated_at TEXT NOT NULL)`},
		{SQL: `CREATE TABLE IF NOT EXISTS shared_replication (entity_type TEXT NOT NULL, entity_id TEXT NOT NULL, last_revision INTEGER NOT NULL, last_synced_at TEXT, PRIMARY KEY(entity_type, entity_id))`},
		{SQL: `CREATE TABLE IF NOT EXISTS hub_outbox (id TEXT PRIMARY KEY, entity_type TEXT NOT NULL, entity_id TEXT NOT NULL, revision INTEGER NOT NULL, kind TEXT NOT NULL, payload BLOB NOT NULL, created_at TEXT NOT NULL, published_at TEXT)`},
		{SQL: `CREATE INDEX IF NOT EXISTS hub_outbox_pending_idx ON hub_outbox(published_at, created_at)`},
	},
}, {
	Version: 2,
	Name:    sharedReplicationMigrationName,
	Statements: []store.Statement{
		{SQL: `CREATE TABLE IF NOT EXISTS replication_state (
			id INTEGER PRIMARY KEY CHECK(id = 1),
			status TEXT NOT NULL,
			cursor TEXT NOT NULL DEFAULT '',
			last_success_at TEXT,
			oldest_pending_at TEXT,
			pending_count INTEGER NOT NULL DEFAULT 0,
			failed_count INTEGER NOT NULL DEFAULT 0,
			last_error_code TEXT,
			attempt INTEGER NOT NULL DEFAULT 0,
			next_attempt_at TEXT,
			updated_at TEXT NOT NULL
		)`},
		{SQL: `INSERT OR IGNORE INTO replication_state(id,status,updated_at) VALUES(1,'synced',CURRENT_TIMESTAMP)`},
		{SQL: `ALTER TABLE hub_outbox ADD COLUMN attempt INTEGER NOT NULL DEFAULT 0`},
		{SQL: `ALTER TABLE hub_outbox ADD COLUMN last_error_code TEXT`},
		{SQL: `ALTER TABLE hub_outbox ADD COLUMN next_attempt_at TEXT`},
		{SQL: `CREATE INDEX IF NOT EXISTS hub_outbox_due_idx ON hub_outbox(published_at,next_attempt_at,created_at,id)`},
	},
}, {
	Version: 3,
	Name:    sharedCutoverMigrationName,
	Statements: []store.Statement{
		{SQL: `CREATE TABLE IF NOT EXISTS shared_authority (
			id INTEGER PRIMARY KEY CHECK(id = 1),
			mode TEXT NOT NULL,
			baseline_revision TEXT NOT NULL,
			baseline_digest TEXT NOT NULL,
			cutover_at TEXT NOT NULL
		)`},
		{SQL: `CREATE TABLE IF NOT EXISTS shared_operations (
			operation_id TEXT PRIMARY KEY,
			entity_type TEXT NOT NULL,
			entity_id TEXT NOT NULL,
			revision INTEGER NOT NULL,
			request_sha256 TEXT NOT NULL,
			result_payload BLOB NOT NULL,
			created_at TEXT NOT NULL
		)`},
		{SQL: `ALTER TABLE hub_outbox ADD COLUMN operation_id TEXT NOT NULL DEFAULT ''`},
		{SQL: `ALTER TABLE hub_outbox ADD COLUMN request_sha256 TEXT NOT NULL DEFAULT ''`},
		{SQL: `CREATE INDEX IF NOT EXISTS shared_operations_entity_idx ON shared_operations(entity_type,entity_id,revision)`},
		{SQL: `CREATE INDEX IF NOT EXISTS hub_outbox_operation_idx ON hub_outbox(operation_id)`},
	},
}, {
	Version: 4,
	Name:    "gpt_tunnel_shared_project_identifiers_v1",
	Statements: []store.Statement{
		{SQL: `CREATE TABLE IF NOT EXISTS shared_project_identifiers (
			project_id TEXT PRIMARY KEY,
			project_code TEXT NOT NULL,
			next_task_number INTEGER NOT NULL,
			next_adr_number INTEGER NOT NULL,
			next_rule_number INTEGER NOT NULL,
			next_journal_number INTEGER NOT NULL,
			next_train_number INTEGER NOT NULL
		)`},
	},
}, {
	Version: 5,
	Name:    "gpt_tunnel_shared_train_admission_v1",
	Statements: []store.Statement{
		{SQL: `CREATE TABLE IF NOT EXISTS shared_train_task_admissions (
			project_id TEXT NOT NULL,
			task_id TEXT NOT NULL,
			train_id TEXT NOT NULL,
			created_at TEXT NOT NULL,
			PRIMARY KEY(project_id, task_id)
		)`},
		{SQL: `CREATE INDEX IF NOT EXISTS shared_train_task_admissions_train_idx ON shared_train_task_admissions(project_id,train_id)`},
		{SQL: `CREATE TRIGGER IF NOT EXISTS shared_train_task_admission_conflict
				BEFORE INSERT ON shared_train_task_admissions
				WHEN EXISTS (SELECT 1 FROM shared_train_task_admissions WHERE project_id=NEW.project_id AND task_id=NEW.task_id AND train_id<>NEW.train_id)
				BEGIN SELECT RAISE(ABORT,'task is already admitted to another Shared Train'); END`},
	},
}, {
	Version: 6,
	Name:    "gpt_tunnel_shared_train_admission_update_guard_v1",
	Statements: []store.Statement{
		{SQL: `CREATE TRIGGER IF NOT EXISTS shared_train_task_admission_update_conflict
			BEFORE UPDATE ON shared_train_task_admissions
			WHEN OLD.train_id<>NEW.train_id
			BEGIN SELECT RAISE(ABORT,'task is already admitted to another Shared Train'); END`},
	},
}, {
	Version: 7,
	Name:    sharedTaskSequenceMigrationName,
	Statements: []store.Statement{
		{SQL: `CREATE TABLE IF NOT EXISTS shared_task_sequences (project_id TEXT PRIMARY KEY, project_code TEXT NOT NULL, next_task_number INTEGER NOT NULL)`},
	},
}, {
	Version: 8,
	Name:    sharedIntegrationCurrentMigrationName,
	Statements: []store.Statement{
		{SQL: `CREATE TABLE IF NOT EXISTS shared_integration_receipts (id TEXT PRIMARY KEY, revision INTEGER NOT NULL, payload BLOB NOT NULL, updated_at TEXT NOT NULL)`},
	},
}, {
	Version: 9,
	Name:    sharedBootstrapMigrationName,
	Statements: []store.Statement{
		{SQL: `CREATE TABLE IF NOT EXISTS shared_bootstrap_markers (project_id TEXT PRIMARY KEY, hub_revision TEXT NOT NULL, completed_at TEXT NOT NULL)`},
	},
}, {
	Version: 10,
	Name:    sharedADROutboxMigrationName,
	Statements: []store.Statement{
		{SQL: `CREATE TABLE IF NOT EXISTS shared_adr_sequences (project_id TEXT PRIMARY KEY, project_code TEXT NOT NULL, next_adr_number INTEGER NOT NULL)`},
		{SQL: `ALTER TABLE hub_outbox ADD COLUMN attempts INTEGER NOT NULL DEFAULT 0`},
		{SQL: `ALTER TABLE hub_outbox ADD COLUMN last_error TEXT NOT NULL DEFAULT ''`},
		{SQL: `CREATE INDEX IF NOT EXISTS hub_outbox_retry_idx ON hub_outbox(published_at, next_attempt_at, created_at)`},
	},
}, {
	Version: 11,
	Name:    sharedProjectConfigurationMigrationName,
	Statements: []store.Statement{
		{SQL: `CREATE TABLE IF NOT EXISTS shared_project_configurations (id TEXT PRIMARY KEY, revision INTEGER NOT NULL, payload BLOB NOT NULL, updated_at TEXT NOT NULL)`},
	},
}, sharedLifecycleMigration}

var localMigrations = []migrate.Migration{{
	Version: 1, Name: localOperationalMigrationName,
	Statements: []store.Statement{
		{SQL: `CREATE TABLE IF NOT EXISTS local_events (id TEXT PRIMARY KEY, kind TEXT NOT NULL, payload BLOB NOT NULL, recorded_at TEXT NOT NULL)`},
		{SQL: `CREATE TABLE IF NOT EXISTS local_messages (id TEXT PRIMARY KEY, session_id TEXT, payload BLOB NOT NULL, recorded_at TEXT NOT NULL)`},
		{SQL: `CREATE TABLE IF NOT EXISTS local_logs (id TEXT PRIMARY KEY, level TEXT NOT NULL, component TEXT NOT NULL, event TEXT NOT NULL, payload BLOB NOT NULL, recorded_at TEXT NOT NULL)`},
		{SQL: `CREATE TABLE IF NOT EXISTS local_retention (name TEXT PRIMARY KEY, cutoff_at TEXT NOT NULL)`},
	},
}, {
	// Versions 2 through 5 are owned by released Local history migrations.
	// Keep those applied identities intact and use a UTC timestamp ID for the
	// callback migration instead of allocating another sequence number.
	Version: localCallbackEpochsMigrationVersion, Name: localCallbackEpochsMigrationDescription,
	Statements: []store.Statement{
		{SQL: `CREATE TABLE IF NOT EXISTS local_callback_epochs (
			epoch_id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL,
			agent_id TEXT NOT NULL DEFAULT '',
			session_key TEXT NOT NULL,
			armed_at TEXT NOT NULL,
			busy_seen INTEGER NOT NULL DEFAULT 0,
			idle_observations INTEGER NOT NULL DEFAULT 0,
			emitted_at TEXT
		)`},
		{SQL: `CREATE INDEX IF NOT EXISTS local_callback_epochs_pending_idx ON local_callback_epochs(emitted_at, armed_at, epoch_id)`},
	},
}, {
	Version: localAgentRegistryMigrationVersion, Name: localAgentRegistryMigrationDescription,
	Statements: []store.Statement{
		{SQL: `CREATE TABLE IF NOT EXISTS local_agents (
			project_id TEXT NOT NULL,
			agent_id TEXT NOT NULL,
			payload BLOB NOT NULL,
			updated_at TEXT NOT NULL,
			PRIMARY KEY(project_id,agent_id)
		)`},
		{SQL: `CREATE INDEX IF NOT EXISTS local_agents_project_idx ON local_agents(project_id,agent_id)`},
	},
}, {
	Version: localSessionStoreMigrationVersion, Name: localSessionStoreMigrationDescription,
	Statements: []store.Statement{
		{SQL: `CREATE TABLE IF NOT EXISTS local_sessions (
			session_id TEXT PRIMARY KEY,
			payload BLOB NOT NULL,
			updated_at TEXT NOT NULL,
			status TEXT NOT NULL CHECK(status IN ('active','ended'))
		)`},
		{SQL: `CREATE INDEX IF NOT EXISTS local_sessions_updated_idx ON local_sessions(updated_at,session_id)`},
	},
}}

func (d *Databases) SharedPath() string { return d.sharedPath }

func (d *Databases) LocalPath() string { return d.localPath }

func (d *Databases) Close() error {
	if d == nil {
		return nil
	}
	var localErr, sharedErr error
	if d.Local != nil {
		localErr = d.Local.Close()
	}
	if d.Shared != nil {
		sharedErr = d.Shared.Close()
	}
	return errors.Join(localErr, sharedErr)
}
