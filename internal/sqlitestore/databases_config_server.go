package sqlitestore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

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
	if err := applySharedMigrations(ctx, db.Shared); err != nil {
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
	if err := applyLocalMigrations(ctx, db.Local); err != nil {
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
	sharedBaselineVersion                        int64 = 202609080503
	sharedBaselineName                                 = "create shared baseline"
	sharedTaskSummaryMigrationVersion            int64 = 202609090934
	sharedTaskSummaryMigrationName                     = "migrate retained task summaries"
	sharedTaskSequenceMigrationVersion           int64 = 202609091757
	sharedTaskSequenceMigrationName                    = "reconcile retained task sequences"
	sharedTaskExecutionMigrationVersion          int64 = 202609100915
	sharedTaskExecutionMigrationName                   = "create task execution state"
	sharedTaskExecutionAuthorityMigrationVersion int64 = 202609100916
	sharedTaskExecutionAuthorityMigrationName          = "add task execution authority"
	localBaselineVersion                         int64 = 202609080505
	localBaselineName                                  = "create local baseline"
)
