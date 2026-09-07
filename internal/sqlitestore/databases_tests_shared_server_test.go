package sqlitestore

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	upstream "github.com/rceman/go-sqlite-store/store"
)

func TestLocalCallbackMigrationUsesUTCTimestampIdentityAndDescription(t *testing.T) {
	version := strconv.FormatInt(localCallbackEpochsMigrationVersion, 10)
	if len(version) != len("200601021504") {
		t.Fatalf("callback migration version=%q, want UTC YYYYMMDDHHMM", version)
	}
	if _, err := time.ParseInLocation("200601021504", version, time.UTC); err != nil {
		t.Fatalf("callback migration version=%q is not a UTC timestamp ID: %v", version, err)
	}
	if localCallbackEpochsMigrationDescription == "" || strings.Contains(localCallbackEpochsMigrationDescription, "_v") {
		t.Fatalf("callback migration description=%q must be separate from a version suffix", localCallbackEpochsMigrationDescription)
	}
	for _, migration := range localMigrations {
		if migration.Version == localCallbackEpochsMigrationVersion && migration.Name != localCallbackEpochsMigrationDescription {
			t.Fatalf("timestamp migration identity=%d/%q, want description %q", migration.Version, migration.Name, localCallbackEpochsMigrationDescription)
		}
	}
}

const (
	historicalLocalInterSessionMessagesMigrationName   = "gpt_tunnel_local_inter_session_messages_v1"
	historicalLocalHistoryIndexesMigrationName         = "gpt_tunnel_local_history_indexes_v1"
	historicalLocalHistoryProjectIndexesMigrationName  = "gpt_tunnel_local_history_project_indexes_v1"
	historicalLocalHistoryProjectBackfillMigrationName = "gpt_tunnel_local_history_project_backfill_v1"
)

func TestOpenMigratesTwoIndependentStoresAndSharedCASIsAtomic(t *testing.T) {
	stateDir := t.TempDir()
	db, err := Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	sharedPath, localPath := Paths(stateDir)
	if db.SharedPath() != sharedPath || db.LocalPath() != localPath || sharedPath == localPath {
		t.Fatalf("database paths shared=%q local=%q", db.SharedPath(), db.LocalPath())
	}
	ctx := context.Background()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := db.Shared.Exec(ctx, `INSERT INTO shared_tasks(id,revision,payload,updated_at) VALUES(?,?,?,?)`, "TSK-1", 1, []byte("v1"), now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Local.Exec(ctx, `INSERT INTO local_logs(id,level,component,event,payload,recorded_at) VALUES(?,?,?,?,?,?)`, "LOG-1", "info", "test", "local", []byte("local"), now); err != nil {
		t.Fatal(err)
	}
	_, err = db.Shared.Batch(ctx, []upstream.Statement{
		{SQL: `UPDATE shared_tasks SET revision=?, payload=? WHERE id=? AND revision=?`, Args: []any{2, []byte("v2"), "TSK-1", 0}, RequireRowsAffected: 1},
		{SQL: `INSERT INTO hub_outbox(id,entity_type,entity_id,revision,kind,payload,created_at) VALUES(?,?,?,?,?,?,?)`, Args: []any{"OUT-1", "task", "TSK-1", 2, "update", []byte("v2"), now}, RequireRowsAffected: 1},
	})
	if !errors.Is(err, upstream.ErrRowsAffectedMismatch) {
		t.Fatalf("stale shared CAS error = %v", err)
	}
	row, err := db.Shared.Query(ctx, `SELECT revision, payload, (SELECT COUNT(*) FROM hub_outbox) FROM shared_tasks WHERE id=?`, "TSK-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(row.Rows) != 1 || row.Rows[0][0] != int64(1) || string(row.Rows[0][1].([]byte)) != "v1" || row.Rows[0][2] != int64(0) {
		t.Fatalf("stale CAS was not atomic: %#v", row.Rows)
	}
	localOutbox, err := db.Local.Query(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name='hub_outbox'`)
	if err != nil {
		t.Fatal(err)
	}
	if len(localOutbox.Rows) != 0 {
		t.Fatalf("local store contains Hub outbox schema: %#v", localOutbox.Rows)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(localPath); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	row, err = reopened.Shared.Query(ctx, `SELECT revision, payload FROM shared_tasks WHERE id=?`, "TSK-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(row.Rows) != 1 || row.Rows[0][0] != int64(1) || string(row.Rows[0][1].([]byte)) != "v1" {
		t.Fatalf("shared correctness was lost after local recreation: %#v", row.Rows)
	}
}

func TestOpenRejectsSecondOwner(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = upstream.Open(upstream.Config{Path: db.SharedPath()})
	if !errors.Is(err, upstream.ErrAlreadyOpen) {
		t.Fatalf("second shared owner error = %v", err)
	}
}

func TestOpenReportsLockAcquisitionStage(t *testing.T) {
	stateDir := t.TempDir()
	owned, err := Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer owned.Close()

	_, err = Open(stateDir)
	var openErr *OpenError
	if !errors.As(err, &openErr) {
		t.Fatalf("open error type=%T value=%v", err, err)
	}
	if openErr.Stage != "lock_acquisition" || openErr.Database != "shared" || openErr.Path == "" {
		t.Fatalf("open error=%#v", openErr)
	}
	if !errors.Is(err, upstream.ErrAlreadyOpen) {
		t.Fatalf("open error does not preserve ownership sentinel: %v", err)
	}
}

func TestOpenObserverReportsSQLiteStartupPhases(t *testing.T) {
	var phases []string
	db, err := OpenWithObserver(t.TempDir(), func(phase string) { phases = append(phases, phase) })
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	want := []string{
		"SQLITE_DIRECTORY_PREPARE",
		"SQLITE_SHARED_OPEN",
		"SQLITE_LOCAL_OPEN",
		"SQLITE_SHARED_MIGRATION",
		"SQLITE_LOCAL_MIGRATION",
		"SQLITE_READY",
	}
	if len(phases) != len(want) {
		t.Fatalf("startup phases=%v, want=%v", phases, want)
	}
	for i := range want {
		if phases[i] != want[i] {
			t.Fatalf("startup phases=%v, want=%v", phases, want)
		}
	}
}
