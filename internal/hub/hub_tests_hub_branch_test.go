package hub

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
	"github.com/rceman/gpt-tunnel-gateway/internal/runtime_log"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestConcurrentRepositoryWorkersRegainLock(t *testing.T) {
	stateDir := t.TempDir()
	holder, err := lockfile.Acquire(filepath.Join(stateDir, "locks"), "hub-repository")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	acquired := make(chan *lockfile.Lock, 1)
	failed := make(chan error, 1)
	go func() {
		lock, acquireErr := acquireRepositoryLock(ctx, stateDir)
		if acquireErr != nil {
			failed <- acquireErr
			return
		}
		acquired <- lock
	}()
	time.Sleep(50 * time.Millisecond)
	if err := holder.Release(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-failed:
		t.Fatal(err)
	case lock := <-acquired:
		if err := lock.Release(); err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("worker did not regain repository lock")
	}
}

func TestRepositoryLockEventsCarryOperationAttribution(t *testing.T) {
	bare, _, base := testutil.RepoWithBareRemote(t)
	c := testConfig(t, bare, "gpt-tunnel/home_pc")
	ctx := runtime_log.WithAction(context.Background(), "task/create")
	ctx = runtime_log.WithOperationID(ctx, "task-create-lock-test")
	store := Store{Config: c}
	if _, err := store.Transact(ctx, base, "test: attributed transaction", func(worktree string) ([]string, error) {
		path := ProtocolRoot + "/attributed.json"
		return []string{path}, WriteJSON(worktree, path, map[string]any{"ok": true})
	}); err != nil {
		t.Fatal(err)
	}
	read, err := runtime_log.New(c.StateDir).Read(runtime_log.Filter{OperationID: "task-create-lock-test", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range read.Events {
		if event.Event == "hub_lock_acquired" && event.Action == "task/create" {
			return
		}
	}
	t.Fatalf("missing attributed lock event: %#v", read.Events)
}

func TestEnsurePreservesExistingWritableBranch(t *testing.T) {
	bare, work, base := testutil.RepoWithBareRemote(t)
	testutil.Git(t, work, "switch", "-c", "gpt-tunnel/home_pc")
	if err := os.WriteFile(filepath.Join(work, "hub.txt"), []byte("hub\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, work, "add", "hub.txt")
	testutil.Git(t, work, "commit", "-m", "hub")
	testutil.Git(t, work, "push", "-u", "origin", "gpt-tunnel/home_pc")
	branchHead := strings.TrimSpace(testutil.Git(t, work, "rev-parse", "HEAD"))
	if branchHead == base {
		t.Fatal("branch did not advance")
	}
	c := testConfig(t, bare, "gpt-tunnel/home_pc")
	store := Store{Config: c}
	if err := store.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := store.RemoteRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != branchHead {
		t.Fatalf("existing branch changed: got %s want %s", got, branchHead)
	}
}

func TestEnsureRejectsManagedCloneRepositoryMismatch(t *testing.T) {
	first, _, _ := testutil.RepoWithBareRemote(t)
	second, _, _ := testutil.RepoWithBareRemote(t)
	c := testConfig(t, first, "gpt-tunnel/home_pc")
	if err := (Store{Config: c}).Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	c.Hub.RepositoryURL = second
	if err := (Store{Config: c}).Ensure(context.Background()); err == nil {
		t.Fatal("repository URL mismatch accepted")
	}
}
