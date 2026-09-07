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
	r := Runner{
		MaxReadBytes: 1 << 20,
		MaxDiffBytes: 1 << 20,
		MaxListItems: 100,
	}
	if _, err := r.ReconcileManagedMirror(context.Background(), p, filepath.Join(filepath.Dir(work), "remote.git"), "main"); err == nil {
		t.Fatal("symlink managed mirror unexpectedly accepted")
	}
}

func TestReconcileManagedMirrorRejectsRepositoryURLConflict(t *testing.T) {
	bare, work, _ := testutil.RepoWithBareRemote(t)
	p := config.ProjectConfig{Root: work, Mirror: filepath.Join(t.TempDir(), "mirror.git"), Remote: "origin", DefaultBranch: "main", AirelaySessionKey: "x_master"}
	r := Runner{
		MaxReadBytes: 1 << 20,
		MaxDiffBytes: 1 << 20,
		MaxListItems: 100,
	}
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
	r := Runner{
		MaxReadBytes: 1 << 20,
		MaxDiffBytes: 1 << 20,
		MaxListItems: 100,
	}
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
	r := Runner{
		MaxReadBytes: 1 << 20,
		MaxDiffBytes: 1 << 20,
		MaxListItems: 100,
	}
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

func TestReconcileManagedMirrorFailedInitialCloneLeavesCanonicalAbsentForRetry(t *testing.T) {
	bare, work, _ := testutil.RepoWithBareRemote(t)
	mirror := filepath.Join(t.TempDir(), "mirror.git")
	p := config.ProjectConfig{Root: work, Mirror: mirror, Remote: "origin", DefaultBranch: "main", AirelaySessionKey: "x_master"}
	badURL := filepath.Join(t.TempDir(), "missing-remote.git")
	testutil.Git(t, work, "remote", "set-url", "origin", badURL)
	r := Runner{
		MaxReadBytes: 1 << 20,
		MaxDiffBytes: 1 << 20,
		MaxListItems: 100,
	}
	if _, err := r.ReconcileManagedMirror(context.Background(), p, badURL, "main"); err == nil {
		t.Fatal("failed initial clone unexpectedly succeeded")
	}
	if _, err := os.Lstat(mirror); !os.IsNotExist(err) {
		t.Fatalf("canonical mirror after failed clone: err=%v", err)
	}
	if matches, err := filepath.Glob(filepath.Join(filepath.Dir(mirror), "."+filepath.Base(mirror)+".onboarding-*")); err != nil || len(matches) != 0 {
		t.Fatalf("temporary mirrors remain: matches=%v err=%v", matches, err)
	}
	testutil.Git(t, work, "remote", "set-url", "origin", bare)
	verification, err := r.ReconcileManagedMirror(context.Background(), p, bare, "main")
	if err != nil {
		t.Fatal(err)
	}
	if !verification.Created || verification.Path != mirror {
		t.Fatalf("retry verification = %#v", verification)
	}
}

func TestReconcileManagedMirrorRejectsSymlinkParentWithoutExternalMutation(t *testing.T) {
	bare, work, _ := testutil.RepoWithBareRemote(t)
	root := t.TempDir()
	external := filepath.Join(t.TempDir(), "external")
	if err := os.Mkdir(external, 0o700); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(root, "git-mirrors")
	if err := os.Symlink(external, parent); err != nil {
		t.Fatal(err)
	}
	mirror := filepath.Join(parent, "project.git")
	p := config.ProjectConfig{Root: work, Mirror: mirror, Remote: "origin", DefaultBranch: "main", AirelaySessionKey: "x_master"}
	r := Runner{
		MaxReadBytes: 1 << 20,
		MaxDiffBytes: 1 << 20,
		MaxListItems: 100,
	}
	if _, err := r.ReconcileManagedMirror(context.Background(), p, bare, "main"); err == nil {
		t.Fatal("symlink mirror parent unexpectedly accepted")
	}
	entries, err := os.ReadDir(external)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("external symlink target was modified: %#v", entries)
	}
	info, err := os.Lstat(parent)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("mirror parent symlink changed: info=%v err=%v", info, err)
	}
}
