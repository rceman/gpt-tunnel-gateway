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

func TestMirrorReadsAllRefsWithoutSwitchingWorktree(t *testing.T) {
	_, work, base := testutil.RepoWithBareRemote(t)
	testutil.Git(t, work, "switch", "-c", "feature/x")
	if err := os.WriteFile(filepath.Join(work, "feature.txt"), []byte("feature\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, work, "add", "feature.txt")
	testutil.Git(t, work, "commit", "-m", "feature")
	testutil.Git(t, work, "push", "-u", "origin", "feature/x")
	testutil.Git(t, work, "switch", "main")
	p := config.ProjectConfig{Root: work, Mirror: filepath.Join(t.TempDir(), "mirror.git"), Remote: "origin", DefaultBranch: "main", AirelaySessionKey: "x_master"}
	r := Runner{MaxReadBytes: 1 << 20, MaxDiffBytes: 1 << 20, MaxListItems: 100}
	ctx := context.Background()
	if err := r.Refresh(ctx, p); err != nil {
		t.Fatal(err)
	}
	refs, err := r.Refs(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ref := range refs {
		if ref.Name == "refs/heads/feature/x" {
			found = true
		}
	}
	if !found {
		t.Fatalf("feature ref missing: %#v", refs)
	}
	content, err := r.ReadFile(ctx, p, "feature/x", "feature.txt")
	if err != nil {
		t.Fatal(err)
	}
	if content != "feature\n" {
		t.Fatalf("%q", content)
	}
	status, err := r.WorktreeStatus(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if status.Branch != "main" || status.Head != base {
		t.Fatalf("worktree changed: %#v", status)
	}
}

func TestResolveMirrorRefStatusDistinguishesMissingBranch(t *testing.T) {
	_, work, _ := testutil.RepoWithBareRemote(t)
	p := config.ProjectConfig{Root: work, Mirror: filepath.Join(t.TempDir(), "mirror.git"), Remote: "origin", DefaultBranch: "main", AirelaySessionKey: "x_master"}
	r := Runner{MaxReadBytes: 1 << 20, MaxDiffBytes: 1 << 20, MaxListItems: 100}
	if err := r.Refresh(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	sha, exists, err := r.ResolveMirrorRefStatus(context.Background(), p, "refs/remotes/origin/no-such-branch")
	if err != nil || exists || sha != "" {
		t.Fatalf("missing ref result sha=%q exists=%v err=%v", sha, exists, err)
	}
}

func TestMirrorBranchHeadRejectsUnresolvableListedBranch(t *testing.T) {
	_, work, _ := testutil.RepoWithBareRemote(t)
	p := config.ProjectConfig{Root: work, Mirror: filepath.Join(t.TempDir(), "mirror.git"), Remote: "origin", DefaultBranch: "main", AirelaySessionKey: "x_master"}
	r := Runner{MaxReadBytes: 1 << 20, MaxDiffBytes: 1 << 20, MaxListItems: 100}
	ctx := context.Background()
	if err := r.Refresh(ctx, p); err != nil {
		t.Fatal(err)
	}
	refPath := filepath.Join(p.Mirror, "refs", "heads", "feature", "broken")
	if err := os.MkdirAll(filepath.Dir(refPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(refPath, []byte(strings.Repeat("f", 40)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.MirrorBranchHead(ctx, p, "feature/broken"); err == nil {
		t.Fatal("unresolvable listed branch was treated as absent")
	}
}

func TestReconcileManagedMirrorRejectsSymlink(t *testing.T) {
	_, work, _ := testutil.RepoWithBareRemote(t)
	mirrorRoot := t.TempDir()
	target := filepath.Join(mirrorRoot, "target.git")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	mirror := filepath.Join(mirrorRoot, "mirror.git")
	if err := os.Symlink(target, mirror); err != nil {
		t.Fatal(err)
	}
	p := config.ProjectConfig{Root: work, Mirror: mirror, Remote: "origin", DefaultBranch: "main", AirelaySessionKey: "x_master"}
	r := Runner{MaxReadBytes: 1 << 20, MaxDiffBytes: 1 << 20, MaxListItems: 100}
	if _, err := r.ReconcileManagedMirror(context.Background(), p, filepath.Join(filepath.Dir(work), "remote.git"), "main"); err == nil {
		t.Fatal("symlink managed mirror unexpectedly accepted")
	}
}

func TestReconcileManagedMirrorRejectsRepositoryURLConflict(t *testing.T) {
	bare, work, _ := testutil.RepoWithBareRemote(t)
	p := config.ProjectConfig{Root: work, Mirror: filepath.Join(t.TempDir(), "mirror.git"), Remote: "origin", DefaultBranch: "main", AirelaySessionKey: "x_master"}
	r := Runner{MaxReadBytes: 1 << 20, MaxDiffBytes: 1 << 20, MaxListItems: 100}
	ctx := context.Background()
	if err := r.EnsureMirror(ctx, p); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, p.Mirror, "remote", "set-url", "origin", "git@example.invalid:other/repo.git")
	if _, err := r.ReconcileManagedMirror(ctx, p, bare, "main"); err == nil {
		t.Fatal("managed mirror with conflicting repository URL unexpectedly accepted")
	}
}

func TestReconcileManagedMirrorRefreshesAndReturnsCanonicalHead(t *testing.T) {
	bare, work, base := testutil.RepoWithBareRemote(t)
	p := config.ProjectConfig{Root: work, Mirror: filepath.Join(t.TempDir(), "mirror.git"), Remote: "origin", DefaultBranch: "main", AirelaySessionKey: "x_master"}
	r := Runner{MaxReadBytes: 1 << 20, MaxDiffBytes: 1 << 20, MaxListItems: 100}
	verification, err := r.ReconcileManagedMirror(context.Background(), p, bare, "main")
	if err != nil {
		t.Fatal(err)
	}
	if !verification.Created || verification.Path != filepath.Clean(p.Mirror) || verification.RepositoryURL != bare || verification.Head != base {
		t.Fatalf("verification = %#v", verification)
	}
}

func TestReconcileManagedMirrorRejectsMissingDefaultBranchWithoutChangingSource(t *testing.T) {
	bare, work, beforeHead := testutil.RepoWithBareRemote(t)
	p := config.ProjectConfig{Root: work, Mirror: filepath.Join(t.TempDir(), "mirror.git"), Remote: "origin", DefaultBranch: "missing", AirelaySessionKey: "x_master"}
	r := Runner{MaxReadBytes: 1 << 20, MaxDiffBytes: 1 << 20, MaxListItems: 100}
	beforeStatus := testutil.Git(t, work, "status", "--porcelain")
	if _, err := r.ReconcileManagedMirror(context.Background(), p, bare, p.DefaultBranch); err == nil {
		t.Fatal("missing default branch unexpectedly reconciled")
	}
	afterHead := strings.TrimSpace(testutil.Git(t, work, "rev-parse", "HEAD"))
	afterStatus := testutil.Git(t, work, "status", "--porcelain")
	if afterHead != beforeHead || afterStatus != beforeStatus {
		t.Fatalf("source worktree changed: before head/status=%s/%q after=%s/%q", beforeHead, beforeStatus, afterHead, afterStatus)
	}
}
