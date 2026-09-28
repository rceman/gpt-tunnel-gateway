package sqlitestore

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	upstream "github.com/rceman/go-sqlite-store/store"
)

func TestSnapshotDatabasesIsOnlineConsistentAndRestorable(t *testing.T) {
	ctx := context.Background()
	sourceDir := t.TempDir()
	db, err := Open(sourceDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, store := range []*upstream.Store{db.Shared, db.Local} {
		if _, err := store.Exec(ctx, `CREATE TABLE snapshot_probe (id INTEGER PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
			t.Fatal(err)
		}
	}
	writerReady := make(chan error, 1)
	writerDone := make(chan error, 1)
	go func() {
		ready := false
		for index := 0; index < 100; index++ {
			for _, store := range []*upstream.Store{db.Shared, db.Local} {
				if _, err := store.Exec(ctx, `INSERT INTO snapshot_probe(id,value) VALUES(?,?)`, index+1, fmt.Sprintf("value-%03d", index+1)); err != nil {
					if !ready {
						writerReady <- err
					}
					writerDone <- err
					return
				}
			}
			if index == 0 {
				writerReady <- nil
				ready = true
			}
			time.Sleep(time.Millisecond)
		}
		writerDone <- nil
	}()
	if err := <-writerReady; err != nil {
		t.Fatal(err)
	}
	snapshotDir := t.TempDir()
	if err := os.Chmod(snapshotDir, 0o700); err != nil {
		t.Fatal(err)
	}
	snapshotCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := SnapshotDatabases(snapshotCtx, sourceDir, snapshotDir); err != nil {
		t.Fatal(err)
	}
	if err := <-writerDone; err != nil {
		t.Fatal(err)
	}
	candidateStateDir := t.TempDir()
	if err := os.Chmod(candidateStateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := SnapshotDatabases(snapshotCtx, snapshotDir, candidateStateDir); err != nil {
		t.Fatal("copy durable snapshot for candidate:", err)
	}
	copyDB, err := Open(candidateStateDir)
	if err != nil {
		t.Fatal("open candidate snapshot:", err)
	}
	counts := make([]int64, 0, 2)
	for _, store := range []*upstream.Store{copyDB.Shared, copyDB.Local} {
		rows, err := store.Query(ctx, `SELECT COUNT(*) FROM snapshot_probe`)
		if err != nil || len(rows.Rows) != 1 || len(rows.Rows[0]) != 1 {
			t.Fatalf("snapshot row count rows=%#v err=%v", rows, err)
		}
		count, ok := rows.Rows[0][0].(int64)
		if !ok || count <= 0 || count > 100 {
			t.Fatalf("snapshot count=%#v", rows.Rows[0][0])
		}
		counts = append(counts, count)
	}
	if err := copyDB.Close(); err != nil {
		t.Fatal(err)
	}
	for _, store := range []*upstream.Store{db.Shared, db.Local} {
		if _, err := store.Exec(ctx, `INSERT INTO snapshot_probe(id,value) VALUES(101,'post-snapshot')`); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := RestoreDatabases(snapshotDir, sourceDir); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(sourceDir)
	if err != nil {
		t.Fatal("open restored state:", err)
	}
	defer restored.Close()
	for index, store := range []*upstream.Store{restored.Shared, restored.Local} {
		rows, err := store.Query(ctx, `SELECT COUNT(*) FROM snapshot_probe`)
		if err != nil || len(rows.Rows) != 1 || rows.Rows[0][0] != counts[index] {
			t.Fatalf("restored count=%#v err=%v want=%d", rows, err, counts[index])
		}
	}
}

func TestSnapshotDatabasesRejectsOversizedDatabase(t *testing.T) {
	sourceDir := t.TempDir()
	sharedPath, _ := Paths(sourceDir)
	if err := os.MkdirAll(filepath.Dir(sharedPath), 0o700); err != nil {
		t.Fatal(err)
	}
	database, err := os.Create(sharedPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Truncate(databaseSnapshotMaxBytes + 1); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	snapshotDir := t.TempDir()
	if err := os.Chmod(snapshotDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := SnapshotDatabases(context.Background(), sourceDir, snapshotDir); err == nil || !strings.Contains(err.Error(), "size limit") {
		t.Fatalf("oversized database error=%v", err)
	}
}

func TestSnapshotDatabasesRejectsSymlinkedSourceSidecar(t *testing.T) {
	sourceDir := t.TempDir()
	db, err := Open(sourceDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	sharedPath, _ := Paths(sourceDir)
	if err := os.Remove(sharedPath + "-wal"); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "external-wal")
	if err := os.WriteFile(target, []byte("external"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, sharedPath+"-wal"); err != nil {
		t.Fatal(err)
	}
	snapshotDir := t.TempDir()
	if err := os.Chmod(snapshotDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := SnapshotDatabases(context.Background(), sourceDir, snapshotDir); err == nil || !strings.Contains(err.Error(), "sidecars") {
		t.Fatalf("symlinked source sidecar error=%v", err)
	}
}
