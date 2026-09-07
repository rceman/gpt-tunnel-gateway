package gitx

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
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
	r := Runner{
		MaxReadBytes: 1 << 20,
		MaxDiffBytes: 1 << 20,
		MaxListItems: 100,
	}
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

func TestChangedWorkingFilesPreservesFirstPathCharacter(t *testing.T) {
	_, work, _ := testutil.RepoWithBareRemote(t)
	path := filepath.Join(work, "internal", "gates", "gates.go")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, work, "add", "internal/gates/gates.go")
	testutil.Git(t, work, "commit", "-m", "seed changed working file")
	if err := os.WriteFile(path, []byte("package main\nvar _ = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := Runner{
		MaxReadBytes: 1 << 20,
		MaxDiffBytes: 1 << 20,
		MaxListItems: 100,
	}
	paths, err := r.ChangedWorkingFiles(context.Background(), work)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(paths, []string{"internal/gates/gates.go"}) {
		t.Fatalf("changed paths=%v; want exact parser result", paths)
	}
}

func TestWorktreeFingerprintChangesWithWorkingTreeBytes(t *testing.T) {
	_, work, _ := testutil.RepoWithBareRemote(t)
	r := Runner{
		MaxReadBytes: 1 << 20,
		MaxDiffBytes: 1 << 20,
		MaxListItems: 100,
	}
	first, err := r.WorktreeFingerprint(context.Background(), work)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(work, "fingerprint.txt")
	if err := os.WriteFile(path, []byte("one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := r.WorktreeFingerprint(context.Background(), work)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("untracked file was omitted from worktree fingerprint")
	}
	if err := os.WriteFile(path, []byte("two\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	third, err := r.WorktreeFingerprint(context.Background(), work)
	if err != nil {
		t.Fatal(err)
	}
	if second == third {
		t.Fatal("working-tree byte change was omitted from fingerprint")
	}
}

func TestResolveMirrorRefStatusDistinguishesMissingBranch(t *testing.T) {
	_, work, _ := testutil.RepoWithBareRemote(t)
	p := config.ProjectConfig{Root: work, Mirror: filepath.Join(t.TempDir(), "mirror.git"), Remote: "origin", DefaultBranch: "main", AirelaySessionKey: "x_master"}
	r := Runner{
		MaxReadBytes: 1 << 20,
		MaxDiffBytes: 1 << 20,
		MaxListItems: 100,
	}
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
	r := Runner{
		MaxReadBytes: 1 << 20,
		MaxDiffBytes: 1 << 20,
		MaxListItems: 100,
	}
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
