package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
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
		ProjectID: "example", Worktree: selector, Path: "tracked.txt", StartLine: 1, LineCount: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if read.CurrentHead != f.current || read.Content != "candidate tracked content with needle" {
		t.Fatalf("unexpected committed read: %#v", read)
	}
	var readProjection map[string]any
	encoded, err := json.Marshal(read)
	if err != nil || json.Unmarshal(encoded, &readProjection) != nil || readProjection["head"] != f.current {
		t.Fatalf("code identity did not expose full head: %s %#v", encoded, readProjection)
	}
	worktrees, err := f.service.CodeWorktree(ctx, CodeWorktreeInput{ProjectID: "example", Limit: 1})
	if err != nil || len(worktrees.Items) != 1 || worktrees.Items[0].Head != f.current {
		t.Fatalf("worktree item did not expose full head: %#v %v", worktrees, err)
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
	if len(tree.NextCursor) > 256 {
		t.Fatalf("tree cursor is not bounded: %d", len(tree.NextCursor))
	}
	secondTree, err := f.service.CodeTree(ctx, CodeTreeInput{ProjectID: "example", Worktree: selector, Limit: 1, Cursor: tree.NextCursor})
	if err != nil || len(secondTree.Paths) == 0 {
		t.Fatalf("tree cursor did not resume: %#v %v", secondTree, err)
	}
	scopedTree, err := f.service.CodeTree(ctx, CodeTreeInput{ProjectID: "example", Worktree: selector, Path: "search-a.txt", Limit: 1})
	if err != nil || len(scopedTree.Paths) != 1 || scopedTree.Paths[0] != "search-a.txt" || scopedTree.HasMore {
		t.Fatalf("tree path scope was not applied: %#v %v", scopedTree, err)
	}

	if _, err := f.service.CodeRead(ctx, CodeReadInput{
		ProjectID: "example", Worktree: "WT-MAIN-" + f.unrelated[:8], Path: "tracked.txt",
	}); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("stale selector was accepted: %v", err)
	}
}

func TestLocalCodeInspectionRequiresSharedDurabilityForWorktreeDiscovery(t *testing.T) {
	f := newLocalCodeFixture(t)
	f.service.Durability = nil
	if _, err := f.service.CodeWorktree(context.Background(), CodeWorktreeInput{ProjectID: "example", Limit: 1}); err == nil || !strings.Contains(err.Error(), "Shared durability unavailable") {
		t.Fatalf("missing Shared durability was not fail-closed: %v", err)
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
	if len(first.NextCursor) > 256 {
		t.Fatalf("search cursor is not bounded: %d", len(first.NextCursor))
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

func TestLocalCodeSearchSkipsPreCursorFileContents(t *testing.T) {
	f := newLocalCodeFixture(t)
	for name, content := range map[string]string{
		"a-before.txt": "ordinary content\n",
		"b-match.txt":  "needle first\n",
		"c-match.txt":  "needle second\n",
	} {
		if err := os.WriteFile(filepath.Join(f.root, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		for _, name := range []string{"a-before.txt", "b-match.txt", "c-match.txt"} {
			_ = os.Remove(filepath.Join(f.root, name))
		}
	})
	reads := make(map[string]int)
	f.service.codeFileReader = func(ctx context.Context, target localCodeTarget, pathName string) (string, error) {
		reads[pathName]++
		return f.service.Git.ReadWorkingFile(ctx, target.ProjectWorktree, pathName)
	}
	selector := "WT-MAIN-" + f.current[:8]
	first, err := f.service.CodeSearch(context.Background(), CodeSearchInput{
		ProjectID: "example", Worktree: selector, Live: true,
		Query: "needle", Paths: []string{"a-before.txt", "b-match.txt", "c-match.txt"}, Limit: 1,
	})
	if err != nil || len(first.Matches) != 1 || first.Matches[0].Path != "b-match.txt" || !first.HasMore {
		t.Fatalf("failed to establish live cursor: %#v %v", first, err)
	}
	if reads["a-before.txt"] != 1 || reads["b-match.txt"] != 1 || reads["c-match.txt"] != 1 {
		t.Fatalf("unexpected first-page file reads: %#v", reads)
	}
	reads = make(map[string]int)
	if err := os.WriteFile(filepath.Join(f.root, "a-before.txt"), []byte(strings.Repeat("x", LocalCodeMaxBytes+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := f.service.CodeSearch(context.Background(), CodeSearchInput{
		ProjectID: "example", Worktree: selector, Live: true,
		Query: "needle", Paths: []string{"a-before.txt", "b-match.txt", "c-match.txt"}, Limit: 1, Cursor: first.NextCursor,
	})
	if err != nil || len(second.Matches) != 1 || second.Matches[0].Path != "c-match.txt" {
		t.Fatalf("pre-cursor file was reread instead of skipped: %#v %v", second, err)
	}
	if reads["a-before.txt"] != 0 || reads["b-match.txt"] != 1 || reads["c-match.txt"] != 1 {
		t.Fatalf("unexpected resumed-page file reads: %#v", reads)
	}
}

func TestLocalCodeSearchBoundsRareMatchesAndResumes(t *testing.T) {
	f := newLocalCodeFixture(t)
	for index := 0; index < LocalCodeScanLookahead; index++ {
		name := filepath.Join(f.root, "scan", strconv.Itoa(index)+".txt")
		if err := os.MkdirAll(filepath.Dir(name), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte("ordinary content\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	testutil.Git(t, f.root, "add", "scan")
	testutil.Git(t, f.root, "commit", "-m", "scan bound fixture")
	selector := "WT-MAIN-" + strings.TrimSpace(testutil.Git(t, f.root, "rev-parse", "HEAD"))[:8]
	first, err := f.service.CodeSearch(context.Background(), CodeSearchInput{
		ProjectID: "example", Worktree: selector, Query: "absent-query", Limit: 1,
	})
	if err != nil || len(first.Matches) != 0 || first.PathsScanned != LocalCodeScanLookahead || !first.HasMore || first.NextCursor == "" {
		t.Fatalf("rare-match scan was not bounded: %#v %v", first, err)
	}
	cursor := first.NextCursor
	for page := 0; ; page++ {
		if page >= 3 {
			t.Fatal("zero-match continuation did not terminate")
		}
		next, nextErr := f.service.CodeSearch(context.Background(), CodeSearchInput{
			ProjectID: "example", Worktree: selector, Query: "absent-query", Limit: 1, Cursor: cursor,
		})
		if nextErr != nil || len(next.Matches) != 0 || next.PathsScanned > LocalCodeScanLookahead+1 {
			t.Fatalf("zero-match continuation page %d exceeded its scan bound: %#v %v", page, next, nextErr)
		}
		if !next.HasMore {
			if next.NextCursor != "" {
				t.Fatalf("terminal zero-match page exposed a cursor: %#v", next)
			}
			break
		}
		if next.NextCursor == "" {
			t.Fatalf("nonterminal zero-match page omitted its cursor: %#v", next)
		}
		cursor = next.NextCursor
	}
}

func TestCodeTrainWorktreePathUsesProjectCodeForCompactLane(t *testing.T) {
	stateDir := t.TempDir()
	project := config.ProjectConfig{ProjectCode: "GTW"}
	want := filepath.Join(stateDir, "work", "GTW", "TRN63")
	path, err := codeTrainWorktreePath(stateDir, "gpt-tunnel-gateway", project, "GTW-TRN63", nil)
	if err != nil || path != want {
		t.Fatalf("unexpected compact Train path: %q %v", path, err)
	}
	runtime := &trainv2.RuntimeBinding{ProjectID: "gpt-tunnel-gateway", ProjectCode: "GTW", TrainID: "GTW-TRN63", WorktreePath: want}
	if path, err := codeTrainWorktreePath(stateDir, "gpt-tunnel-gateway", project, "GTW-TRN63", runtime); err != nil || path != want {
		t.Fatalf("runtime compact Train path was rejected: %q %v", path, err)
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
	live, err := f.service.CodeDiff(context.Background(), CodeDiffInput{ProjectID: "example", Worktree: selector, Live: true, MaxBytes: 8})
	if err != nil {
		t.Fatal(err)
	}
	allDiff := live.Diff
	for pages := 0; live.HasMore; pages++ {
		if pages > 100 {
			t.Fatal("live diff pagination did not terminate")
		}
		live, err = f.service.CodeDiff(context.Background(), CodeDiffInput{ProjectID: "example", Worktree: selector, Live: true, MaxBytes: 8, Cursor: live.NextCursor})
		if err != nil {
			t.Fatal(err)
		}
		if live.Diff == "" {
			t.Fatal("live diff continuation returned an empty page")
		}
		allDiff += live.Diff
	}
	for _, want := range []string{"uncommitted content", "live-untracked.txt", "untracked needle"} {
		if !strings.Contains(allDiff, want) {
			t.Fatalf("live diff omitted %q: %s", want, allDiff)
		}
	}
}
