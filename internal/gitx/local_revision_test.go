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

func TestExactReadsPreferRegisteredManagedWorktree(t *testing.T) {
	_, root, base := testutil.RepoWithBareRemote(t)
	r := Runner{
		MaxReadBytes: 1 << 20,
		MaxDiffBytes: 1 << 20,
		MaxListItems: 100,
		StateDir:     t.TempDir(),
	}
	p := config.ProjectConfig{
		Root:              root,
		Mirror:            filepath.Join(t.TempDir(), "mirror.git"),
		Remote:            "origin",
		DefaultBranch:     "main",
		AirelaySessionKey: "gpt-tunnel-gateway_master",
	}
	ctx := context.Background()
	if err := r.Refresh(ctx, p); err != nil {
		t.Fatal(err)
	}

	taskID := "GTW-TSK999"
	laneRoot := filepath.Join(r.StateDir, "task-worktrees", "gpt-tunnel-gateway", taskID)
	if err := os.MkdirAll(filepath.Dir(laneRoot), 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, root, "worktree", "add", "-b", "task/"+taskID, laneRoot, base)
	trainRoot := laneRoot
	if err := os.WriteFile(filepath.Join(trainRoot, "local-only.txt"), []byte("local\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, trainRoot, "add", "local-only.txt")
	testutil.Git(t, trainRoot, "commit", "-m", "local managed checkpoint")
	checkpoint := strings.TrimSpace(testutil.Git(t, trainRoot, "rev-parse", "HEAD"))

	show, err := r.Show(ctx, p, checkpoint)
	if err != nil || !strings.Contains(show, "local managed checkpoint") {
		t.Fatalf("local show: err=%v output=%q", err, show)
	}
	content, err := r.ReadFile(ctx, p, checkpoint, "local-only.txt")
	if err != nil || content != "local\n" {
		t.Fatalf("local read: err=%v content=%q", err, content)
	}
	tree, err := r.Tree(ctx, p, checkpoint, "")
	if err != nil || !containsString(tree, "local-only.txt") {
		t.Fatalf("local tree: err=%v tree=%v", err, tree)
	}
	page, _, err := r.TreePage(ctx, p, checkpoint, "", 10, "")
	if err != nil || !containsString(page, "local-only.txt") {
		t.Fatalf("local tree page: err=%v tree=%v", err, page)
	}
	logs, _, err := r.LogPage(ctx, p, checkpoint, 10, "")
	if err != nil || len(logs) == 0 || logs[0].SHA != checkpoint {
		t.Fatalf("local log page: err=%v logs=%v", err, logs)
	}
	diff, err := r.Diff(ctx, p, base, checkpoint, nil)
	if err != nil || !strings.Contains(diff, "local-only.txt") {
		t.Fatalf("local diff: err=%v diff=%q", err, diff)
	}
	mergeBase, err := r.MergeBase(ctx, p, base, checkpoint)
	if err != nil || mergeBase != base {
		t.Fatalf("local merge base: err=%v base=%q", err, mergeBase)
	}
	comparison, err := r.Compare(ctx, p, base, checkpoint)
	if err != nil || comparison.MergeBase != base || comparison.RightOnly != 1 {
		t.Fatalf("local compare: err=%v comparison=%#v", err, comparison)
	}

	if _, err := r.ReadFile(ctx, p, "main", "local-only.txt"); err == nil {
		t.Fatal("symbolic main unexpectedly resolved local-only file")
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
