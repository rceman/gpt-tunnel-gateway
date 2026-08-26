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

	read, err := f.service.CodeRead(ctx, CodeReadInput{
		ProjectID: "example", WorktreeRef: "refs/heads/main", BaseSHA: f.base,
		Path: "tracked.txt", MaxBytes: 8,
	})
	if err != nil {
		t.Fatal(err)
	}
	if read.CurrentHead != f.current || read.Content != "candidat" || !read.Truncated || read.TotalBytes == 0 {
		t.Fatalf("unexpected bounded committed read: %#v", read)
	}

	search, err := f.service.CodeSearch(ctx, CodeSearchInput{
		ProjectID: "example", WorktreeRef: "refs/heads/main", BaseSHA: f.base,
		Query: "needle", Paths: []string{"tracked.txt", "new.txt"}, Limit: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if search.PathsScanned != 1 || len(search.Matches) != 1 || !search.Truncated {
		t.Fatalf("search bound/early stop not enforced: %#v", search)
	}

	full, err := f.service.CodeDiff(ctx, CodeDiffInput{
		ProjectID: "example", WorktreeRef: "refs/heads/main", BaseSHA: f.base,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"tracked.txt", "deleted.txt", "new.txt"} {
		if !strings.Contains(full.Diff, want) {
			t.Fatalf("full diff omitted %q: %s", want, full.Diff)
		}
	}

	deleted, err := f.service.CodeDiff(ctx, CodeDiffInput{
		ProjectID: "example", WorktreeRef: "refs/heads/main", BaseSHA: f.base,
		Paths: []string{"deleted.txt"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(deleted.Diff, "deleted.txt") || !strings.Contains(deleted.Diff, "deleted file") {
		t.Fatalf("deleted base path was not diffable: %s", deleted.Diff)
	}

	if _, err := f.service.CodeRead(ctx, CodeReadInput{
		ProjectID: "example", WorktreeRef: "refs/heads/main", BaseSHA: f.unrelated, Path: "tracked.txt",
	}); err == nil || !strings.Contains(err.Error(), "not an ancestor") {
		t.Fatalf("unrelated base was accepted: %v", err)
	}
}

func TestLocalCodeInspectionRejectsDirtyWorktree(t *testing.T) {
	f := newLocalCodeFixture(t)
	path := filepath.Join(f.root, "tracked.txt")
	if err := os.WriteFile(path, []byte("uncommitted content\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { testutil.Git(t, f.root, "restore", "tracked.txt") })

	_, err := f.service.CodeRead(context.Background(), CodeReadInput{
		ProjectID: "example", WorktreeRef: "refs/heads/main", BaseSHA: f.base, Path: "tracked.txt",
	})
	if err == nil || !strings.Contains(err.Error(), "dirty") {
		t.Fatalf("dirty worktree was not rejected: %v", err)
	}
}
