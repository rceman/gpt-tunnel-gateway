package gitx

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func hotfixTestRunner(stateDir string) Runner {
	return Runner{MaxReadBytes: 1 << 20, MaxDiffBytes: 1 << 20, MaxListItems: 100, StateDir: stateDir}
}

func hotfixTestProject(work, mirror string) config.ProjectConfig {
	return config.ProjectConfig{Root: work, Mirror: mirror, Remote: "origin", DefaultBranch: "main", AirelaySessionKey: "test_master"}
}

func TestHotfixIdentityIsCreateOnceAndServerOwned(t *testing.T) {
	stateDir := t.TempDir()
	r := hotfixTestRunner(stateDir)
	identity := HotfixIdentity{ProjectID: "example", HotfixRef: "refs/heads/hotfix/repair", TaskID: "EXM-TSK1", BaseSHA: "0123456789012345678901234567890123456789"}
	if err := r.RecordHotfixIdentity(stateDir, identity); err != nil {
		t.Fatal(err)
	}
	got, err := r.ReadHotfixIdentity(stateDir, identity.ProjectID, identity.HotfixRef)
	if err != nil {
		t.Fatal(err)
	}
	if got != identity {
		t.Fatalf("identity=%#v want %#v", got, identity)
	}
	if err := r.RecordHotfixIdentity(stateDir, HotfixIdentity{ProjectID: identity.ProjectID, HotfixRef: identity.HotfixRef, TaskID: identity.TaskID, BaseSHA: "abcdefabcdefabcdefabcdefabcdefabcdefabcd"}); err == nil {
		t.Fatal("existing hotfix identity was overwritten")
	}
}

func TestLegacyHotfixIdentityWithoutTaskBindingIsSkippedByInventory(t *testing.T) {
	stateDir := t.TempDir()
	r := hotfixTestRunner(stateDir)
	dir := filepath.Join(stateDir, "hotfix-identities", "example")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	legacy := `{"project_id":"example","hotfix_ref":"refs/heads/hotfix/legacy","base_sha":"0123456789012345678901234567890123456789","created_at":"2026-09-01T00:00:00Z"}`
	if err := os.WriteFile(filepath.Join(dir, "legacy.json"), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	identities, err := r.ListHotfixIdentities(stateDir, "example")
	if err != nil {
		t.Fatalf("ListHotfixIdentities() error = %v", err)
	}
	if len(identities) != 0 {
		t.Fatalf("legacy identity was returned: %#v", identities)
	}
	if err := r.RecordHotfixIdentity(stateDir, HotfixIdentity{
		ProjectID: "example", HotfixRef: "refs/heads/hotfix/new", BaseSHA: "0123456789012345678901234567890123456789",
	}); err == nil {
		t.Fatal("new hotfix identity without TaskID was accepted")
	}
}

func TestMaterializeMirrorCommitWhenSourceLacksCanonicalHead(t *testing.T) {
	bare, work, oldHead := testutil.RepoWithBareRemote(t)
	writer := filepath.Join(t.TempDir(), "writer")
	testutil.Git(t, filepath.Dir(writer), "clone", bare, writer)
	testutil.Git(t, writer, "config", "user.email", "test@example.invalid")
	testutil.Git(t, writer, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(writer, "remote.txt"), []byte("remote\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, writer, "add", "remote.txt")
	testutil.Git(t, writer, "commit", "-m", "remote main")
	testutil.Git(t, writer, "push", "origin", "main")

	stateDir := t.TempDir()
	p := hotfixTestProject(work, filepath.Join(stateDir, "mirror.git"))
	r := hotfixTestRunner(stateDir)
	canonical, err := r.RefreshDefaultBranch(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if canonical == oldHead {
		t.Fatal("remote main did not advance")
	}
	if _, err := r.Resolve(context.Background(), work, canonical); err == nil {
		t.Fatal("stale source unexpectedly already contained canonical remote head")
	}
	if err := r.MaterializeMirrorCommit(context.Background(), p, "main", canonical); err != nil {
		t.Fatal(err)
	}
	resolved, err := r.Resolve(context.Background(), work, canonical)
	if err != nil || resolved != canonical {
		t.Fatalf("materialized head=%q err=%v want %q", resolved, err, canonical)
	}
	status, err := r.WorktreeStatus(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if status.Branch != "main" || status.Head != oldHead {
		t.Fatalf("local main moved: %#v", status)
	}
}

func TestHotfixRollbackLeavesDirtyLaneUntouched(t *testing.T) {
	_, work, base := testutil.RepoWithBareRemote(t)
	stateDir := t.TempDir()
	p := hotfixTestProject(work, filepath.Join(stateDir, "mirror.git"))
	r := hotfixTestRunner(stateDir)
	lane, err := r.CreateHotfixWorktree(context.Background(), p, stateDir, "example", "repair", base)
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(lane.Root, "dirty.txt")
	if err := os.WriteFile(marker, []byte("must remain\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.RemoveHotfixWorktree(context.Background(), p, stateDir, "example", "repair", base); err == nil {
		t.Fatal("dirty hotfix lane was rolled back")
	}
	if _, err := os.Stat(lane.Root); err != nil {
		t.Fatalf("dirty lane was not left untouched: %v", err)
	}
	if _, err := os.Stat(filepath.Join(work, ".git", "refs", "heads", "hotfix", "repair")); err != nil {
		// Packed refs are valid; Resolve verifies the branch remains present.
		if _, resolveErr := r.Resolve(context.Background(), work, "refs/heads/hotfix/repair"); resolveErr != nil {
			t.Fatalf("hotfix branch was removed: %v", resolveErr)
		}
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	if err := r.RemoveHotfixWorktree(context.Background(), p, stateDir, "example", "repair", base); err != nil {
		t.Fatal(err)
	}
}

func TestHotfixRollbackLeavesUnexpectedHeadUntouched(t *testing.T) {
	_, work, base := testutil.RepoWithBareRemote(t)
	stateDir := t.TempDir()
	p := hotfixTestProject(work, filepath.Join(stateDir, "mirror.git"))
	r := hotfixTestRunner(stateDir)
	lane, err := r.CreateHotfixWorktree(context.Background(), p, stateDir, "example", "repair", base)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lane.Root, "unexpected.txt"), []byte("unexpected\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, lane.Root, "add", "unexpected.txt")
	testutil.Git(t, lane.Root, "commit", "-m", "unexpected head")
	unexpectedHead := strings.TrimSpace(testutil.Git(t, lane.Root, "rev-parse", "HEAD"))
	if err := r.RemoveHotfixWorktree(context.Background(), p, stateDir, "example", "repair", base); err == nil {
		t.Fatal("unexpected-head hotfix lane was rolled back")
	}
	if _, err := os.Stat(lane.Root); err != nil {
		t.Fatalf("unexpected-head lane was not left untouched: %v", err)
	}
	if got, err := r.Resolve(context.Background(), work, "refs/heads/hotfix/repair"); err != nil || got != unexpectedHead {
		t.Fatalf("unexpected-head branch changed: got=%q err=%v want=%q", got, err, unexpectedHead)
	}
	testutil.Git(t, lane.Root, "reset", "--hard", base)
	if err := r.RemoveHotfixWorktree(context.Background(), p, stateDir, "example", "repair", base); err != nil {
		t.Fatal(err)
	}
}

func TestHotfixRefRuntimeValidationMatchesSchemaBounds(t *testing.T) {
	valid := "refs/heads/hotfix/" + strings.Repeat("a", 80)
	if _, err := hotfixSlugFromRef(valid); err != nil {
		t.Fatalf("80-character hotfix slug rejected: %v", err)
	}
	for _, ref := range []string{
		"refs/heads/hotfix/" + strings.Repeat("a", 81),
		"refs/heads/hotfix/a_b",
		"refs/heads/hotfix/a/b",
		"refs/heads/main/a",
	} {
		if _, err := hotfixSlugFromRef(ref); err == nil {
			t.Fatalf("invalid hotfix_ref accepted: %q", ref)
		}
	}
}
