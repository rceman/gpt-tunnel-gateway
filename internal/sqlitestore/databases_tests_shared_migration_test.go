package sqlitestore

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	upstream "github.com/rceman/go-sqlite-store/store"
)

func TestSharedMigrationHistoryPreservesReleasedVersionsBeforeCandidates(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	rows, err := db.Shared.Query(context.Background(), `SELECT version,name FROM schema_migrations ORDER BY version`)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		version int64
		name    string
	}{{sharedBaselineVersion, sharedBaselineName}, {sharedTaskSummaryMigrationVersion, sharedTaskSummaryMigrationName}, {sharedTaskSequenceMigrationVersion, sharedTaskSequenceMigrationName}}
	if len(rows.Rows) != len(want) {
		t.Fatalf("migration history length=%d, want=%d: %#v", len(rows.Rows), len(want), rows.Rows)
	}
	for i, entry := range want {
		if rows.Rows[i][0] != entry.version || rows.Rows[i][1] != entry.name {
			t.Fatalf("migration history[%d]=%#v, want version=%d name=%q", i, rows.Rows[i], entry.version, entry.name)
		}
	}
}

func BenchmarkSharedAndLocalTraffic(b *testing.B) {
	db, err := Open(filepath.Join(b.TempDir(), "state"))
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		now := time.Now().UTC().Format(time.RFC3339Nano)
		if _, err := db.Shared.Batch(ctx, []upstream.Statement{
			{SQL: `INSERT OR REPLACE INTO shared_tasks(id,revision,payload,updated_at) VALUES(?,?,?,?)`, Args: []any{"TSK-BENCH", int64(i + 1), []byte("shared"), now}},
			{SQL: `INSERT INTO hub_outbox(id,entity_type,entity_id,revision,kind,payload,created_at) VALUES(?,?,?,?,?,?,?)`, Args: []any{"OUT-BENCH-" + strconv.Itoa(i), "task", "TSK-BENCH", int64(i + 1), "update", []byte("shared"), now}},
		}); err != nil {
			b.Fatal(err)
		}
		if _, err := db.Local.Exec(ctx, `INSERT INTO local_logs(id,level,component,event,payload,recorded_at) VALUES(?,?,?,?,?,?)`, "LOG-BENCH-"+strconv.Itoa(i), "info", "bench", "local", []byte("local"), now); err != nil {
			b.Fatal(err)
		}
	}
}
