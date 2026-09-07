package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

type localCodeFixture struct {
	service   *Service
	root      string
	base      string
	current   string
	unrelated string
}

func newLocalCodeFixture(t *testing.T) localCodeFixture {
	t.Helper()
	_, root, _ := testutil.RepoWithBareRemote(t)
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("tracked.txt", "base tracked content\n")
	write("deleted.txt", "this file is deleted in the candidate\n")
	write("search-a.txt", "needle in the first file\n")
	testutil.Git(t, root, "add", ".")
	testutil.Git(t, root, "commit", "-m", "fixture base")
	base := strings.TrimSpace(testutil.Git(t, root, "rev-parse", "HEAD"))

	write("tracked.txt", "candidate tracked content with needle\n")
	write("new.txt", "candidate new file with needle\n")
	if err := os.Remove(filepath.Join(root, "deleted.txt")); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, root, "add", "-A")
	testutil.Git(t, root, "commit", "-m", "fixture candidate")
	current := strings.TrimSpace(testutil.Git(t, root, "rev-parse", "HEAD"))
	testutil.Git(t, root, "push", "origin", "main")

	// Create a genuinely unrelated commit, then return the configured main
	// worktree to the candidate. This is used only as a negative ancestry base.
	testutil.Git(t, root, "switch", "--orphan", "unrelated")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() == ".git" {
			continue
		}
		if err := os.RemoveAll(filepath.Join(root, entry.Name())); err != nil {
			t.Fatal(err)
		}
	}
	write("unrelated.txt", "not an ancestor\n")
	testutil.Git(t, root, "add", ".")
	testutil.Git(t, root, "commit", "-m", "unrelated root")
	unrelated := strings.TrimSpace(testutil.Git(t, root, "rev-parse", "HEAD"))
	testutil.Git(t, root, "switch", "main")

	stateDir := t.TempDir()
	c := config.Config{
		GatewayID:    "code-test",
		StateDir:     stateDir,
		MaxReadBytes: 1 << 20,
		MaxDiffBytes: 1 << 20,
		MaxListItems: 1,
		Hub: config.HubConfig{
			// Code inspection must remain local even when Hub is unavailable.
			RepositoryURL: "ssh://unreachable.invalid/gateway.git",
		},
		Projects: map[string]config.ProjectConfig{
			"example": {
				Root:              root,
				Mirror:            filepath.Join(t.TempDir(), "mirror.git"),
				Remote:            "origin",
				DefaultBranch:     "main",
				AirelaySessionKey: "code-test-agent",
			},
		},
	}
	db, err := sqlitestore.Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return localCodeFixture{
		service:   NewWithDurabilityDeferredWorkers(c, db),
		root:      root,
		base:      base,
		current:   current,
		unrelated: unrelated,
	}
}

func TestLocalCodeInspectionUsesCleanAncestorAndBoundedCommittedObjects(t *testing.T) {
	f := newLocalCodeFixture(t)
	ctx := context.Background()
	selector := "WT-MAIN-" + f.current[:8]

	read, err := f.service.CodeRead(ctx, CodeReadInput{
		ProjectID: "example",
		Worktree:  selector,
		Path:      "tracked.txt",
		StartLine: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if read.CurrentHead != f.current[:8] || read.Content != "candidate tracked content with needle\n" {
		t.Fatalf("unexpected committed read: %#v", read)
	}
	var readProjection map[string]any
	encoded, err := json.Marshal(read)
	if err != nil || json.Unmarshal(encoded, &readProjection) != nil || readProjection["head"] != f.current[:8] {
		t.Fatalf("code read did not expose the public 8-character head: %s %#v", encoded, readProjection)
	}
	worktrees, err := f.service.CodeWorktree(ctx, CodeWorktreeInput{ProjectID: "example"})
	if err != nil || len(worktrees.Items) != 1 || worktrees.Items[0].Head != f.current || worktrees.Pagination != nil {
		t.Fatalf("worktree item did not expose full head: %#v %v", worktrees, err)
	}

	search, err := f.service.CodeSearch(ctx, CodeSearchInput{
		ProjectID: "example",
		Worktree:  selector,
		Query:     "needle",
		Paths:     []string{"tracked.txt", "new.txt"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if search.PathsScanned != 2 || len(search.Matches) != 2 || search.Pagination != nil {
		t.Fatalf("search did not pack complete scope: %#v", search)
	}

	full, err := f.service.CodeDiff(ctx, CodeDiffInput{
		ProjectID: "example",
		Worktree:  selector,
	})
	if err != nil {
		t.Fatal(err)
	}
	if full.Diff != "" || full.Pagination != nil {
		t.Fatalf("clean committed selector reported a diff: %#v", full)
	}
	tree, err := f.service.CodeTree(ctx, CodeTreeInput{
		ProjectID: "example",
		Worktree:  selector,
	})
	if err != nil || len(tree.Paths) < 2 || tree.Pagination != nil {
		t.Fatalf("expected tree to pack complete scope: %#v %v", tree, err)
	}
	scopedTree, err := f.service.CodeTree(ctx, CodeTreeInput{
		ProjectID: "example",
		Worktree:  selector,
		Path:      "search-a.txt",
	})
	if err != nil || len(scopedTree.Paths) != 1 || scopedTree.Paths[0] != "search-a.txt" || scopedTree.Pagination != nil {
		t.Fatalf("tree path scope was not applied: %#v %v", scopedTree, err)
	}

	if _, err := f.service.CodeRead(ctx, CodeReadInput{
		ProjectID: "example",
		Worktree:  "WT-MAIN-" + f.unrelated[:8],
		Path:      "tracked.txt",
	}); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("stale selector was accepted: %v", err)
	}
}
