package hub

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func testConfig(t *testing.T, repositoryURL, branch string) config.Config {
	t.Helper()
	return config.Config{
		StateDir: t.TempDir(), MaxReadBytes: 1 << 20, MaxListItems: 100,
		Hub: config.HubConfig{RepositoryURL: repositoryURL, Branch: branch, AuthorName: "Gateway", AuthorEmail: "gateway@example.invalid"},
	}
}

func TestEnsureCreatesManagedCloneAndMissingBranch(t *testing.T) {
	bare, work, base := testutil.RepoWithBareRemote(t)
	c := testConfig(t, bare, "gpt-tunnel/home_pc")
	store := Store{Config: c}
	if err := store.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	root := ManagedRoot(c)
	if root == work || !strings.HasPrefix(root, c.StateDir+string(filepath.Separator)) {
		t.Fatalf("unexpected managed root: %s", root)
	}
	if got := strings.TrimSpace(testutil.Git(t, root, "remote", "get-url", RemoteName)); got != bare {
		t.Fatalf("unexpected remote URL: %q", got)
	}
	if got := strings.TrimSpace(testutil.Git(t, bare, "rev-parse", "refs/heads/gpt-tunnel/home_pc")); got != base {
		t.Fatalf("created branch at %s, want %s", got, base)
	}
	if got := strings.TrimSpace(testutil.Git(t, bare, "rev-parse", "refs/heads/main")); got != base {
		t.Fatalf("main changed: %s", got)
	}
	if status := testutil.Git(t, root, "status", "--porcelain"); status != "" {
		t.Fatalf("managed clone dirty: %q", status)
	}
	if status := testutil.Git(t, work, "status", "--porcelain"); status != "" {
		t.Fatalf("user clone dirty: %q", status)
	}
}

func TestEnsureObserverReportsBoundedSubphases(t *testing.T) {
	bare, _, _ := testutil.RepoWithBareRemote(t)
	c := testConfig(t, bare, "gpt-tunnel/home_pc")
	var phases []string
	if err := (Store{Config: c}).EnsureWithObserver(context.Background(), func(phase string) {
		phases = append(phases, phase)
	}); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"HUB_ENSURE_LOCK_ACQUIRE_START",
		"HUB_ENSURE_LOCK_ACQUIRE_DONE",
		"HUB_ENSURE_MANAGED_ROOT_START",
		"HUB_ENSURE_MANAGED_ROOT_DONE",
		"HUB_ENSURE_REMOTE_FETCH_START",
		"HUB_ENSURE_REMOTE_FETCH_DONE",
		"HUB_ENSURE_BRANCH_RECONCILE_START",
		"HUB_ENSURE_BRANCH_RECONCILE_DONE",
		"HUB_ENSURE_DONE",
	}
	if strings.Join(phases, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("phases=%q want=%q", phases, want)
	}
}

func TestReadDoesNotCreateCloneBranchOrLockArtifacts(t *testing.T) {
	bare, _, _ := testutil.RepoWithBareRemote(t)
	c := testConfig(t, bare, "gpt-tunnel/home_pc")
	store := Store{Config: c}
	if _, err := store.ReadFile(context.Background(), ProtocolRoot+"/missing.json"); err == nil {
		t.Fatal("read succeeded without an initialized managed hub")
	}
	if _, err := os.Stat(ManagedRoot(c)); !os.IsNotExist(err) {
		t.Fatalf("read created managed root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(c.StateDir, "locks")); !os.IsNotExist(err) {
		t.Fatalf("read created lock directory: %v", err)
	}
}

func TestReadFileRefreshesStaleRemoteTrackingRef(t *testing.T) {
	bare, work, base := testutil.RepoWithBareRemote(t)
	c := testConfig(t, bare, "gpt-tunnel/home_pc")
	store := Store{Config: c}
	if err := store.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(work, ProtocolRoot, "fresh.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"fresh":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, work, "add", filepath.ToSlash(filepath.Join(ProtocolRoot, "fresh.json")))
	testutil.Git(t, work, "commit", "-m", "advance hub for freshness test")
	testutil.Git(t, work, "push", "origin", "HEAD:refs/heads/gpt-tunnel/home_pc")
	remote := strings.TrimSpace(testutil.Git(t, bare, "rev-parse", "refs/heads/gpt-tunnel/home_pc"))
	if remote == base {
		t.Fatal("remote branch did not advance")
	}
	data, err := store.ReadFile(context.Background(), ProtocolRoot+"/fresh.json")
	if err != nil || string(data) != `{"fresh":true}` {
		t.Fatalf("fresh read=%q err=%v", data, err)
	}
	if got, err := store.RemoteRevision(context.Background()); err != nil || got != remote {
		t.Fatalf("fresh revision=%q want=%q err=%v", got, remote, err)
	}
	if _, err := store.ReadFile(context.Background(), ProtocolRoot+"/missing-after-refresh.json"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing path error=%v, want canonical not-found", err)
	}
}

func TestReadSnapshotUsesOneRevisionAndEnforcesPerFileAndAggregateBounds(t *testing.T) {
	bare, _, base := testutil.RepoWithBareRemote(t)
	c := testConfig(t, bare, "gpt-tunnel/home_pc")
	c.MaxReadBytes = 4
	c.MaxListItems = 3
	store := Store{Config: c}
	tx, err := store.Transact(context.Background(), base, "test: snapshot bounds", func(worktree string) ([]string, error) {
		paths := []string{ProtocolRoot + "/one.json", ProtocolRoot + "/two.json", ProtocolRoot + "/three.json"}
		if err := os.MkdirAll(filepath.Join(worktree, ProtocolRoot), 0o700); err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Join(worktree, "other"), 0o700); err != nil {
			return nil, err
		}
		for _, path := range paths {
			if err := os.WriteFile(filepath.Join(worktree, filepath.FromSlash(path)), []byte("1234"), 0o600); err != nil {
				return nil, err
			}
		}
		if err := os.WriteFile(filepath.Join(worktree, "other", "extra.json"), []byte("1234"), 0o600); err != nil {
			return nil, err
		}
		if err := os.WriteFile(filepath.Join(worktree, "other", "too-large.json"), []byte("12345"), 0o600); err != nil {
			return nil, err
		}
		if err := os.WriteFile(filepath.Join(worktree, "other", "malformed.json"), []byte("{}x"), 0o600); err != nil {
			return nil, err
		}
		return append(paths, "other/extra.json", "other/too-large.json", "other/malformed.json"), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.ReadSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	if snapshot.Revision() != tx.After {
		t.Fatalf("snapshot revision = %s, want %s", snapshot.Revision(), tx.After)
	}
	paths, err := snapshot.List(context.Background(), ProtocolRoot, ".json")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 3 {
		t.Fatalf("snapshot list length = %d, want 3", len(paths))
	}
	if _, err := snapshot.ReadFile(context.Background(), paths[0]); err != nil {
		t.Fatal(err)
	}
	snapshot.readBytes = maxSnapshotAggregateReadBytes - 3
	if _, err := snapshot.ReadFile(context.Background(), paths[1]); err == nil {
		t.Fatal("aggregate snapshot bound was not enforced")
	}
	if _, err := snapshot.ReadFile(context.Background(), "other/too-large.json"); err == nil {
		t.Fatal("per-file snapshot bound was not enforced")
	}
	var object map[string]any
	if err := snapshot.ReadJSON(context.Background(), "other/malformed.json", &object); err == nil {
		t.Fatal("malformed trailing JSON unexpectedly succeeded")
	}
	if _, err := snapshot.ReadFile(context.Background(), ProtocolRoot+"/missing.json"); err == nil {
		t.Fatal("missing snapshot file unexpectedly succeeded")
	}
}

func TestTransactionPushesThroughManagedClone(t *testing.T) {
	bare, work, base := testutil.RepoWithBareRemote(t)
	c := testConfig(t, bare, "gpt-tunnel/home_pc")
	store := Store{Config: c}
	tx, err := store.Transact(context.Background(), base, "test: write", func(w string) ([]string, error) {
		path := ProtocolRoot + "/test.json"
		return []string{path}, WriteJSON(w, path, map[string]any{"ok": true})
	})
	if err != nil {
		t.Fatal(err)
	}
	if tx.After == base || tx.Branch != "gpt-tunnel/home_pc" || tx.Remote != RemoteName {
		t.Fatalf("unexpected transaction: %#v", tx)
	}
	data, err := store.ReadFile(context.Background(), ProtocolRoot+"/test.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Fatal("empty")
	}
	if status := testutil.Git(t, work, "status", "--porcelain"); status != "" {
		t.Fatalf("user clone dirtied: %q", status)
	}
	if _, err := os.Stat(filepath.Join(work, "gpt-tunnel")); !os.IsNotExist(err) {
		t.Fatalf("user clone modified")
	}
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
