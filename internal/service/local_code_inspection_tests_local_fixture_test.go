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
		ProjectID: "example", Worktree: selector, Path: "tracked.txt", StartLine: 1,
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
		ProjectID: "example", Worktree: selector,
		Query: "needle", Paths: []string{"tracked.txt", "new.txt"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if search.PathsScanned != 2 || len(search.Matches) != 2 || search.Pagination != nil {
		t.Fatalf("search did not pack complete scope: %#v", search)
	}

	full, err := f.service.CodeDiff(ctx, CodeDiffInput{
		ProjectID: "example", Worktree: selector,
	})
	if err != nil {
		t.Fatal(err)
	}
	if full.Diff != "" || full.Pagination != nil {
		t.Fatalf("clean committed selector reported a diff: %#v", full)
	}
	tree, err := f.service.CodeTree(ctx, CodeTreeInput{ProjectID: "example", Worktree: selector})
	if err != nil || len(tree.Paths) < 2 || tree.Pagination != nil {
		t.Fatalf("expected tree to pack complete scope: %#v %v", tree, err)
	}
	scopedTree, err := f.service.CodeTree(ctx, CodeTreeInput{ProjectID: "example", Worktree: selector, Path: "search-a.txt"})
	if err != nil || len(scopedTree.Paths) != 1 || scopedTree.Paths[0] != "search-a.txt" || scopedTree.Pagination != nil {
		t.Fatalf("tree path scope was not applied: %#v %v", scopedTree, err)
	}

	if _, err := f.service.CodeRead(ctx, CodeReadInput{
		ProjectID: "example", Worktree: "WT-MAIN-" + f.unrelated[:8], Path: "tracked.txt",
	}); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("stale selector was accepted: %v", err)
	}
}

func TestLocalCodeReadSupportsExactBoundedRangesAndContinuation(t *testing.T) {
	f := newLocalCodeFixture(t)
	selector := "WT-MAIN-" + f.current[:8]
	immutableCount := 1
	immutable, err := f.service.CodeRead(context.Background(), CodeReadInput{
		ProjectID: "example", Worktree: selector, Path: "tracked.txt", StartLine: 1, LineCount: &immutableCount,
	})
	if err != nil || immutable.CurrentHead != f.current[:8] || immutable.StartLine != 1 || immutable.EndLine != 1 || immutable.Content != "candidate tracked content with needle" || immutable.Pagination != nil {
		t.Fatalf("live=false bounded read was not an immutable exact range: %#v %v", immutable, err)
	}
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(f.root, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Remove(filepath.Join(f.root, name)) })
	}
	write("range.txt", strings.Join([]string{
		"line-01", "line-02", "line-03", "line-04", "line-05", "line-06", "line-07", "line-08",
	}, "\n"))
	var wide strings.Builder
	for line := 1; line <= 120; line++ {
		fmt.Fprintf(&wide, "%03d %s\n", line, strings.Repeat("range-token ", 48))
	}
	write("wide.txt", wide.String())
	shortCount := 2
	short, err := f.service.CodeRead(context.Background(), CodeReadInput{
		ProjectID: "example", Worktree: selector, Path: "range.txt", StartLine: 3, LineCount: &shortCount, Live: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if short.CurrentHead != f.current[:8] || short.StartLine != 3 || short.EndLine != 4 || short.TotalLines != 8 || short.Content != "line-03\nline-04" || short.Pagination != nil {
		t.Fatalf("unexpected exact short range: %#v", short)
	}

	nearEOFCount := 5
	nearEOF, err := f.service.CodeRead(context.Background(), CodeReadInput{
		ProjectID: "example", Worktree: selector, Path: "range.txt", StartLine: 7, LineCount: &nearEOFCount, Live: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if nearEOF.StartLine != 7 || nearEOF.EndLine != 8 || nearEOF.Content != "line-07\nline-08" || nearEOF.Pagination != nil {
		t.Fatalf("near-EOF range was not clamped: %#v", nearEOF)
	}

	fileReads := 0
	f.service.codeFileReader = func(context.Context, localCodeTarget, string) (string, error) {
		fileReads++
		return "", nil
	}
	for _, invalidCount := range []int{0, -1} {
		_, err := f.service.CodeRead(context.Background(), CodeReadInput{
			ProjectID: "example", Worktree: selector, Path: "range.txt", LineCount: &invalidCount, Live: true,
		})
		if err == nil || !strings.Contains(err.Error(), "line_count") {
			t.Fatalf("invalid line_count %d was accepted: %v", invalidCount, err)
		}
	}
	if fileReads != 0 {
		t.Fatalf("invalid line_count triggered %d file reads", fileReads)
	}
	f.service.codeFileReader = nil
	outOfRangeCount := 1
	if _, err := f.service.CodeRead(context.Background(), CodeReadInput{
		ProjectID: "example", Worktree: selector, Path: "range.txt", StartLine: 9, LineCount: &outOfRangeCount, Live: true,
	}); err == nil || !strings.Contains(err.Error(), "start_line exceeds file") {
		t.Fatalf("out-of-range start_line was accepted: %v", err)
	}

	requestedCount := 100
	page, err := f.service.CodeRead(context.Background(), CodeReadInput{
		ProjectID: "example", Worktree: selector, Path: "wide.txt", StartLine: 10, LineCount: &requestedCount, Live: true,
	})
	if err != nil || page.Pagination == nil {
		t.Fatalf("oversized range did not continue: %#v %v", page, err)
	}
	if _, err := f.service.CodeRead(context.Background(), CodeReadInput{
		ProjectID: "example", Worktree: selector, Path: "range.txt", Cursor: page.Pagination.NextCursor, LineCount: &requestedCount, Live: true,
	}); err == nil || !strings.Contains(err.Error(), "cannot be combined") {
		t.Fatalf("cursor plus line_count was not rejected: %v", err)
	}
	wantStart, pages := 10, 0
	for {
		if page.CurrentHead != f.current[:8] || page.StartLine != wantStart || page.EndLine < page.StartLine || page.EndLine > 109 || !strings.HasPrefix(page.Content, fmt.Sprintf("%03d ", wantStart)) {
			t.Fatalf("range continuation was not exact: %#v", page)
		}
		pages++
		if page.Pagination == nil {
			if page.EndLine != 109 {
				t.Fatalf("range continuation ended at %d, want 109", page.EndLine)
			}
			break
		}
		if page.Pagination.NextCursor == "" {
			t.Fatal("range continuation omitted cursor")
		}
		conflictingCount := 1
		if _, err := f.service.CodeRead(context.Background(), CodeReadInput{
			ProjectID: "example", Worktree: selector, Path: "wide.txt", Cursor: page.Pagination.NextCursor, LineCount: &conflictingCount, Live: true,
		}); err == nil || !strings.Contains(err.Error(), "cannot be combined") {
			t.Fatalf("cursor plus line_count was not rejected: %v", err)
		}
		if pages > 100 {
			t.Fatal("range continuation did not terminate")
		}
		wantStart = page.EndLine + 1
		page, err = f.service.CodeRead(context.Background(), CodeReadInput{
			ProjectID: "example", Worktree: selector, Path: "wide.txt", Cursor: page.Pagination.NextCursor, Live: true,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
