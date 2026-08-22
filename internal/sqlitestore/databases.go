// Package sqlitestore owns GPT Tunnel's embedded SQLite durability boundary.
// Shared is syncable Hub authority; Local is operational state and never enters
// the Hub outbox.
package sqlitestore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/rceman/go-sqlite-store/migrate"
	"github.com/rceman/go-sqlite-store/store"
)

const (
	UpstreamSourceRevision = "f0df65dfff2f648cbb75e759c30b2aec45524c30"
	UpstreamVersion        = "0.2.0"
	defaultReaders         = 2
	defaultBatchSize       = 8
	defaultQueueDepth      = 4096
	defaultBatchWindow     = 250 * time.Microsecond
	defaultCacheKiB        = 8192
	defaultWALCheckpoint   = 2000
)

type Config struct {
	StateDir        string
	Readers         int
	WriteQueueDepth int
	BatchSize       int
	BatchWindow     time.Duration
	Synchronous     string
	CacheKiB        int
	WALAutoCheck    int
}

type Databases struct {
	Shared     *store.Store
	Local      *store.Store
	sharedPath string
	localPath  string
}

// OpenError preserves the bounded startup diagnostic boundary without
// changing the store's ownership or durability behavior.
type OpenError struct {
	Stage    string
	Database string
	Path     string
	Err      error
}

func (e *OpenError) Error() string {
	return fmt.Sprintf("sqlite %s %s (%s): %v", e.Stage, e.Database, e.Path, e.Err)
}

func (e *OpenError) Unwrap() error { return e.Err }

func Paths(stateDir string) (shared, local string) {
	root := filepath.Join(filepath.Clean(stateDir), "databases")
	return filepath.Join(root, "shared.db"), filepath.Join(root, "local.db")
}

func Open(stateDir string) (*Databases, error) { return OpenWithConfig(Config{StateDir: stateDir}) }

// OpenWithObserver is the same durable open path as OpenWithConfig, with
// bounded phase notifications for daemon startup diagnostics.
func OpenWithObserver(stateDir string, observe func(string)) (*Databases, error) {
	return openWithConfig(Config{StateDir: stateDir}, observe)
}

func OpenWithConfig(cfg Config) (*Databases, error) {
	return openWithConfig(cfg, nil)
}

func openWithConfig(cfg Config, observe func(string)) (*Databases, error) {
	notify := func(phase string) {
		if observe != nil {
			observe(phase)
		}
	}
	if cfg.StateDir == "" || !filepath.IsAbs(cfg.StateDir) {
		return nil, &OpenError{
			Stage:    "directory_prepare",
			Database: "state",
			Err:      fmt.Errorf("sqlite store state directory must be absolute"),
		}
	}
	notify("SQLITE_DIRECTORY_PREPARE")
	sharedPath, localPath := Paths(cfg.StateDir)
	if err := os.MkdirAll(filepath.Dir(sharedPath), 0o700); err != nil {
		return nil, &OpenError{
			Stage:    "directory_prepare",
			Database: "state",
			Path:     filepath.Dir(sharedPath),
			Err:      err,
		}
	}
	engine := engineConfig(cfg)
	notify("SQLITE_SHARED_OPEN")
	shared, err := store.Open(engine(sharedPath))
	if err != nil {
		return nil, &OpenError{
			Stage:    openStage(err),
			Database: "shared",
			Path:     sharedPath,
			Err:      err,
		}
	}
	notify("SQLITE_LOCAL_OPEN")
	local, err := store.Open(engine(localPath))
	if err != nil {
		_ = shared.Close()
		return nil, &OpenError{
			Stage:    openStage(err),
			Database: "local",
			Path:     localPath,
			Err:      err,
		}
	}
	db := &Databases{
		Shared:     shared,
		Local:      local,
		sharedPath: sharedPath,
		localPath:  localPath,
	}
	if err := applyMigrations(context.Background(), db, notify); err != nil {
		_ = db.Close()
		return nil, err
	}
	notify("SQLITE_READY")
	return db, nil
}

func openStage(err error) string {
	if errors.Is(err, store.ErrAlreadyOpen) {
		return "lock_acquisition"
	}
	return "database_open"
}

func engineConfig(cfg Config) func(string) store.Config {
	readers := cfg.Readers
	if readers <= 0 {
		readers = defaultReaders
	}
	queueDepth := cfg.WriteQueueDepth
	if queueDepth <= 0 {
		queueDepth = defaultQueueDepth
	}
	batchSize := cfg.BatchSize
	if batchSize <= 0 {
		batchSize = defaultBatchSize
	}
	batchWindow := cfg.BatchWindow
	if batchWindow <= 0 {
		batchWindow = defaultBatchWindow
	}
	synchronous := cfg.Synchronous
	if synchronous == "" {
		synchronous = "FULL"
	}
	cacheKiB := cfg.CacheKiB
	if cacheKiB <= 0 {
		cacheKiB = defaultCacheKiB
	}
	checkpoint := cfg.WALAutoCheck
	if checkpoint <= 0 {
		checkpoint = defaultWALCheckpoint
	}
	return func(path string) store.Config {
		return store.Config{Path: path, Readers: readers, WriteQueueDepth: queueDepth, BatchSize: batchSize, BatchWindow: batchWindow, Synchronous: synchronous, CacheKiB: cacheKiB, WALAutoCheckpoint: checkpoint}
	}
}

func applyMigrations(ctx context.Context, db *Databases, notify func(string)) error {
	if notify != nil {
		notify("SQLITE_SHARED_MIGRATION")
	}
	if err := migrate.Apply(ctx, db.Shared, sharedMigrations, migrate.Options{}); err != nil {
		return &OpenError{
			Stage:    "migration",
			Database: "shared",
			Path:     db.sharedPath,
			Err:      err,
		}
	}
	if notify != nil {
		notify("SQLITE_LOCAL_MIGRATION")
	}
	if err := migrate.Apply(ctx, db.Local, localMigrations, migrate.Options{}); err != nil {
		return &OpenError{
			Stage:    "migration",
			Database: "local",
			Path:     db.localPath,
			Err:      err,
		}
	}
	return nil
}

const (
	sharedReplicationMigrationName          = "gpt_tunnel_shared_replication_v1"
	sharedCutoverMigrationName              = "gpt_tunnel_shared_cutover_v1"
	sharedTaskSequenceMigrationName         = "gpt_tunnel_shared_task_sequences_v7"
	sharedIntegrationCurrentMigrationName   = "gpt_tunnel_shared_integration_receipts_v8"
	sharedBootstrapMigrationName            = "gpt_tunnel_shared_bootstrap_markers_v9"
	sharedADROutboxMigrationName            = "gpt_tunnel_shared_adr_outbox_retry_v10"
	sharedProjectConfigurationMigrationName = "gpt_tunnel_shared_project_configurations_v11"
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
}}

var localMigrations = []migrate.Migration{{
	Version: 1, Name: "gpt_tunnel_local_operational_v1",
	Statements: []store.Statement{
		{SQL: `CREATE TABLE IF NOT EXISTS local_events (id TEXT PRIMARY KEY, kind TEXT NOT NULL, payload BLOB NOT NULL, recorded_at TEXT NOT NULL)`},
		{SQL: `CREATE TABLE IF NOT EXISTS local_messages (id TEXT PRIMARY KEY, session_id TEXT, payload BLOB NOT NULL, recorded_at TEXT NOT NULL)`},
		{SQL: `CREATE TABLE IF NOT EXISTS local_logs (id TEXT PRIMARY KEY, level TEXT NOT NULL, component TEXT NOT NULL, event TEXT NOT NULL, payload BLOB NOT NULL, recorded_at TEXT NOT NULL)`},
		{SQL: `CREATE TABLE IF NOT EXISTS local_retention (name TEXT PRIMARY KEY, cutoff_at TEXT NOT NULL)`},
	},
}, {
	Version: 2, Name: "gpt_tunnel_local_pmt_v1",
	Statements: []store.Statement{
		{SQL: `CREATE TABLE IF NOT EXISTS local_pmt_sequences (project_id TEXT PRIMARY KEY, project_code TEXT NOT NULL, next_number INTEGER NOT NULL)`},
		{SQL: `CREATE TABLE IF NOT EXISTS local_pmts (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL,
			project_code TEXT NOT NULL,
			title TEXT NOT NULL,
			instruction BLOB NOT NULL,
			planner_session_id TEXT NOT NULL,
			target_session_id TEXT NOT NULL DEFAULT '',
			target_airelay_session_key TEXT NOT NULL,
			target_agent_id TEXT NOT NULL,
			train_id TEXT NOT NULL DEFAULT '',
			item_position INTEGER NOT NULL DEFAULT 0,
			task_id TEXT NOT NULL DEFAULT '',
			attempt_number INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL,
			state TEXT NOT NULL,
			first_fetched_at TEXT,
			delivered_at TEXT,
			last_fetched_at TEXT,
			cancelled_at TEXT,
			superseded_by TEXT NOT NULL DEFAULT '',
			reference TEXT NOT NULL,
			reference_submitted_at TEXT,
			read_count INTEGER NOT NULL DEFAULT 0,
			expires_at TEXT
		)`},
		{SQL: `CREATE INDEX IF NOT EXISTS local_pmts_pending_idx ON local_pmts(project_id,target_airelay_session_key,state,created_at,id)`},
	},
}}

func (d *Databases) SharedPath() string { return d.sharedPath }
func (d *Databases) LocalPath() string  { return d.localPath }

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
