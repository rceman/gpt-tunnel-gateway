package gitx

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestLogPageRejectsCursorFromDifferentRevisionOrMirror(t *testing.T) {
	_, work, _ := testutil.RepoWithBareRemote(t)
	if err := os.WriteFile(filepath.Join(work, "second.txt"), []byte("second\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, work, "add", "second.txt")
	testutil.Git(t, work, "commit", "-m", "second")
	testutil.Git(t, work, "push", "origin", "main")
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
	_, page, err := r.LogPage(ctx, p, "main", 1, "")
	if err != nil || !page.HasMore {
		t.Fatalf("initial log page = %#v, %v", page, err)
	}
	if _, _, err := r.LogPage(ctx, p, "HEAD^", 1, page.NextCursor); err == nil {
		t.Fatal("log cursor from another revision was accepted")
	}
	other := p
	other.Mirror = filepath.Join(t.TempDir(), "other.git")
	if err := r.Refresh(ctx, other); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.LogPage(ctx, other, "main", 1, page.NextCursor); err == nil {
		t.Fatal("log cursor from another mirror was accepted")
	}
}

func TestTreePageRejectsCursorFromDifferentRevisionOrMirror(t *testing.T) {
	_, work, _ := testutil.RepoWithBareRemote(t)
	for _, name := range []string{"tree-a.txt", "tree-b.txt"} {
		if err := os.WriteFile(filepath.Join(work, name), []byte(name+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	testutil.Git(t, work, "add", "tree-a.txt", "tree-b.txt")
	testutil.Git(t, work, "commit", "-m", "tree")
	testutil.Git(t, work, "push", "origin", "main")
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
	_, page, err := r.TreePage(ctx, p, "main", "", 1, "")
	if err != nil || !page.HasMore {
		t.Fatalf("initial tree page = %#v, %v", page, err)
	}
	if _, _, err := r.TreePage(ctx, p, "HEAD^", "", 1, page.NextCursor); err == nil {
		t.Fatal("tree cursor from another revision was accepted")
	}
	other := p
	other.Mirror = filepath.Join(t.TempDir(), "other.git")
	if err := r.Refresh(ctx, other); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.TreePage(ctx, other, "main", "", 1, page.NextCursor); err == nil {
		t.Fatal("tree cursor from another mirror was accepted")
	}
}
