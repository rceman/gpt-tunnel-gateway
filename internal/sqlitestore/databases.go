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

func Paths(stateDir string) (shared, local string) {
	root := filepath.Join(filepath.Clean(stateDir), "databases")
	return filepath.Join(root, "shared.db"), filepath.Join(root, "local.db")
}

func Open(stateDir string) (*Databases, error) { return OpenWithConfig(Config{StateDir: stateDir}) }

func OpenWithConfig(cfg Config) (*Databases, error) {
	if cfg.StateDir == "" || !filepath.IsAbs(cfg.StateDir) {
		return nil, fmt.Errorf("sqlite store state directory must be absolute")
	}
	sharedPath, localPath := Paths(cfg.StateDir)
	if err := os.MkdirAll(filepath.Dir(sharedPath), 0o700); err != nil {
		return nil, fmt.Errorf("create sqlite store directory: %w", err)
	}
	engine := engineConfig(cfg)
	shared, err := store.Open(engine(sharedPath))
	if err != nil {
		return nil, fmt.Errorf("open shared sqlite store: %w", err)
	}
	local, err := store.Open(engine(localPath))
	if err != nil {
		_ = shared.Close()
		return nil, fmt.Errorf("open local sqlite store: %w", err)
	}
	db := &Databases{
		Shared:     shared,
		Local:      local,
		sharedPath: sharedPath,
		localPath:  localPath,
	}
	if err := applyMigrations(context.Background(), db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
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

func applyMigrations(ctx context.Context, db *Databases) error {
	if err := migrate.Apply(ctx, db.Shared, sharedMigrations, migrate.Options{}); err != nil {
		return fmt.Errorf("migrate shared sqlite store: %w", err)
	}
	if err := migrate.Apply(ctx, db.Local, localMigrations, migrate.Options{}); err != nil {
		return fmt.Errorf("migrate local sqlite store: %w", err)
	}
	return nil
}

var sharedMigrations = []migrate.Migration{{
	Version: 1, Name: "gpt_tunnel_shared_authority_v1",
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
}}

var localMigrations = []migrate.Migration{{
	Version: 1, Name: "gpt_tunnel_local_operational_v1",
	Statements: []store.Statement{
		{SQL: `CREATE TABLE IF NOT EXISTS local_events (id TEXT PRIMARY KEY, kind TEXT NOT NULL, payload BLOB NOT NULL, recorded_at TEXT NOT NULL)`},
		{SQL: `CREATE TABLE IF NOT EXISTS local_messages (id TEXT PRIMARY KEY, session_id TEXT, payload BLOB NOT NULL, recorded_at TEXT NOT NULL)`},
		{SQL: `CREATE TABLE IF NOT EXISTS local_logs (id TEXT PRIMARY KEY, level TEXT NOT NULL, component TEXT NOT NULL, event TEXT NOT NULL, payload BLOB NOT NULL, recorded_at TEXT NOT NULL)`},
		{SQL: `CREATE TABLE IF NOT EXISTS local_retention (name TEXT PRIMARY KEY, cutoff_at TEXT NOT NULL)`},
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
