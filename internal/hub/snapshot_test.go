package hub

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

// TestReadSnapshotServesReadsWithoutRefetch proves that every Hub read routed
// through WithReadSnapshot reuses the pinned revision and lock already held
// by FreshReadSnapshot: once the remote becomes unreachable mid-attempt,
// pinned reads still succeed while any fresh fetch fails. This is the
// mechanism that keeps one convergence attempt at one fetch plus one lock
// acquisition instead of one per read phase.
func TestReadSnapshotServesReadsWithoutRefetch(t *testing.T) {
	bare, work, _ := testutil.RepoWithBareRemote(t)
	c := testConfig(t, bare, "main")
	store := Store{Config: c}
	ctx := context.Background()
	if err := store.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	marker := ProtocolRoot + "/marker.txt"
	if err := os.MkdirAll(filepath.Join(work, ProtocolRoot), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, marker), []byte("pinned\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, work, "add", marker)
	testutil.Git(t, work, "commit", "-m", "add marker")
	testutil.Git(t, work, "push", "origin", "main")

	snapshot, err := store.FreshReadSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	pinned := snapshot.Revision()
	snapCtx := WithReadSnapshot(ctx, snapshot)

	// Make the remote unreachable: any read needing a fresh fetch must now
	// fail, while pinned reads stay served.
	if _, err := command(ctx, ManagedRoot(c), "remote", "set-url", RemoteName, filepath.Join(t.TempDir(), "gone.git")); err != nil {
		t.Fatal(err)
	}
	if revision, err := store.RemoteRevision(snapCtx); err != nil || revision != pinned {
		t.Fatalf("pinned remote revision=%q err=%v want %q", revision, err, pinned)
	}
	data, err := store.ReadFile(snapCtx, marker)
	if err != nil || string(data) != "pinned\n" {
		t.Fatalf("pinned read=%q err=%v", data, err)
	}
	paths, err := store.List(snapCtx, ProtocolRoot, ".txt")
	if err != nil || len(paths) != 1 || paths[0] != marker {
		t.Fatalf("pinned list=%v err=%v", paths, err)
	}
	if err := snapshot.Close(); err != nil {
		t.Fatal(err)
	}
	fetchCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if _, err := store.FreshReadSnapshot(fetchCtx); err == nil {
		t.Fatal("fresh fetch succeeded against an unreachable remote")
	}
}
