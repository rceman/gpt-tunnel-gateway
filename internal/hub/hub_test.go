package hub

import (
	"context"
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
