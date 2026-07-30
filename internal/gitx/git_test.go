package gitx

import (
	"context"
	"os"
	"path/filepath"
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
