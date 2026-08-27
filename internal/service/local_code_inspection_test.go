package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
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
	return localCodeFixture{
		service:   NewWithDurabilityDeferredWorkers(c, nil),
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
		ProjectID: "example", Worktree: selector, Path: "tracked.txt", StartLine: 1, LineCount: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if read.CurrentHead != f.current || read.Content != "candidate tracked content with needle" {
		t.Fatalf("unexpected committed read: %#v", read)
	}

	search, err := f.service.CodeSearch(ctx, CodeSearchInput{
		ProjectID: "example", Worktree: selector,
		Query: "needle", Paths: []string{"tracked.txt", "new.txt"}, Limit: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if search.PathsScanned != 2 || len(search.Matches) != 1 || !search.Truncated {
		t.Fatalf("search pagination did not scan complete scope: %#v", search)
	}

	full, err := f.service.CodeDiff(ctx, CodeDiffInput{
		ProjectID: "example", Worktree: selector,
	})
	if err != nil {
		t.Fatal(err)
	}
	if full.Diff != "" || full.HasMore {
		t.Fatalf("clean committed selector reported a diff: %#v", full)
	}
	tree, err := f.service.CodeTree(ctx, CodeTreeInput{ProjectID: "example", Worktree: selector, Limit: 1})
	if err != nil || len(tree.Paths) != 1 || !tree.HasMore || tree.NextCursor == "" {
		t.Fatalf("expected tree pagination beyond Git helper item cap: %#v %v", tree, err)
	}

	if _, err := f.service.CodeRead(ctx, CodeReadInput{
		ProjectID: "example", Worktree: "WT-MAIN-" + f.unrelated[:8], Path: "tracked.txt",
	}); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("stale selector was accepted: %v", err)
	}
}

func TestLocalCodeSearchContinuesFromExactScanPosition(t *testing.T) {
	f := newLocalCodeFixture(t)
	selector := "WT-MAIN-" + f.current[:8]
	first, err := f.service.CodeSearch(context.Background(), CodeSearchInput{
		ProjectID: "example", Worktree: selector, Query: "needle", Paths: []string{"new.txt", "tracked.txt"}, Limit: 1,
	})
	if err != nil || len(first.Matches) != 1 || !first.HasMore || first.NextCursor == "" {
		t.Fatalf("expected first bounded search page: %#v %v", first, err)
	}
	second, err := f.service.CodeSearch(context.Background(), CodeSearchInput{
		ProjectID: "example", Worktree: selector, Query: "needle", Paths: []string{"new.txt", "tracked.txt"}, Limit: 1, Cursor: first.NextCursor,
	})
	if err != nil || len(second.Matches) != 1 || second.Matches[0].Path == first.Matches[0].Path {
		t.Fatalf("expected exact continuation without duplicate: %#v %v", second, err)
	}
	if second.HasMore || second.NextCursor != "" {
		t.Fatalf("terminal result page exposed an empty continuation: %#v", second)
	}
}

func TestLocalCodeInspectionRejectsDirtyWorktree(t *testing.T) {
	f := newLocalCodeFixture(t)
	selector := "WT-MAIN-" + f.current[:8]
	path := filepath.Join(f.root, "tracked.txt")
	if err := os.WriteFile(path, []byte("uncommitted content\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.root, "live-untracked.txt"), []byte("untracked needle\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		testutil.Git(t, f.root, "restore", "tracked.txt")
		_ = os.Remove(filepath.Join(f.root, "live-untracked.txt"))
	})

	_, err := f.service.CodeRead(context.Background(), CodeReadInput{
		ProjectID: "example", Worktree: selector, Path: "tracked.txt",
	})
	if err == nil || !strings.Contains(err.Error(), "dirty") {
		t.Fatalf("dirty worktree was not rejected: %v", err)
	}
	live, err := f.service.CodeDiff(context.Background(), CodeDiffInput{ProjectID: "example", Worktree: selector, Live: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"uncommitted content", "live-untracked.txt", "untracked needle"} {
		if !strings.Contains(live.Diff, want) {
			t.Fatalf("live diff omitted %q: %s", want, live.Diff)
		}
	}
}
