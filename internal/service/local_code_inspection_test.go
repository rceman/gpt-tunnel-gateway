package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/gitx"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/pagination"
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
		ProjectID: "example", Worktree: selector, Path: "tracked.txt", StartLine: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if read.CurrentHead != f.current || read.Content != "candidate tracked content with needle\n" {
		t.Fatalf("unexpected committed read: %#v", read)
	}
	var readProjection map[string]any
	encoded, err := json.Marshal(read)
	if err != nil || json.Unmarshal(encoded, &readProjection) != nil || readProjection["head"] != f.current {
		t.Fatalf("code identity did not expose full head: %s %#v", encoded, readProjection)
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

func TestLocalCodeInspectionRequiresSharedDurabilityForWorktreeDiscovery(t *testing.T) {
	f := newLocalCodeFixture(t)
	f.service.Durability = nil
	if _, err := f.service.CodeWorktree(context.Background(), CodeWorktreeInput{ProjectID: "example"}); err == nil || !strings.Contains(err.Error(), "Shared durability unavailable") {
		t.Fatalf("missing Shared durability was not fail-closed: %v", err)
	}
}

func TestLocalCodeSearchContinuesFromExactScanPosition(t *testing.T) {
	f := newLocalCodeFixture(t)
	var content strings.Builder
	for index := 0; index < 256; index++ {
		content.WriteString("needle tokenized search line\n")
	}
	pathName := filepath.Join(f.root, "many-matches.txt")
	if err := os.WriteFile(pathName, []byte(content.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(pathName) })
	selector := "WT-MAIN-" + f.current[:8]
	first, err := f.service.CodeSearch(context.Background(), CodeSearchInput{
		ProjectID: "example", Worktree: selector, Query: "needle", Paths: []string{"many-matches.txt"}, Live: true,
	})
	if err != nil || len(first.Matches) < 2 || first.Pagination == nil || first.Pagination.NextCursor == "" {
		t.Fatalf("expected first bounded search page: %#v %v", first, err)
	}
	if len(first.Pagination.NextCursor) > 256 {
		t.Fatalf("search cursor is not bounded: %d", len(first.Pagination.NextCursor))
	}
	second, err := f.service.CodeSearch(context.Background(), CodeSearchInput{
		ProjectID: "example", Worktree: selector, Query: "needle", Paths: []string{"many-matches.txt"}, Live: true, Cursor: first.Pagination.NextCursor,
	})
	if err != nil || len(second.Matches) == 0 || second.Matches[0].Line <= first.Matches[len(first.Matches)-1].Line {
		t.Fatalf("expected exact continuation without duplicate: %#v %v", second, err)
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
	paths := []string{"a-before.txt", "b-match.txt", "c-match.txt"}
	target, err := f.service.resolveLocalCodeTarget(context.Background(), "example", selector, true)
	if err != nil {
		t.Fatal(err)
	}
	kind := codeCursorKind("code-search", target, "needle|"+strings.Join(paths, "\x00")+"|||true")
	cursor := pagination.EncodeSearchCursor(kind, "a-before.txt", 0)
	result, err := f.service.CodeSearch(context.Background(), CodeSearchInput{
		ProjectID: "example", Worktree: selector, Live: true,
		Query: "needle", Paths: paths, Cursor: cursor,
	})
	if err != nil || len(result.Matches) != 2 || result.Pagination != nil {
		t.Fatalf("pre-cursor search result was not complete: %#v %v", result, err)
	}
	if reads["a-before.txt"] != 0 || reads["b-match.txt"] != 1 || reads["c-match.txt"] != 1 {
		t.Fatalf("pre-cursor file contents were reread: %#v", reads)
	}
}

func TestLocalCodeScanSafetyFailsClosedWithoutPagination(t *testing.T) {
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
	tree, treeErr := f.service.CodeTree(context.Background(), CodeTreeInput{
		ProjectID: "example", Worktree: selector, Query: "absent-tree",
	})
	if treeErr == nil || !strings.Contains(treeErr.Error(), "scan exceeded bounded work") {
		t.Fatalf("zero-match tree scan did not fail closed: %#v %v", tree, treeErr)
	}
	_, err := f.service.CodeSearch(context.Background(), CodeSearchInput{
		ProjectID: "example", Worktree: selector, Query: "absent-query",
	})
	if err == nil || !strings.Contains(err.Error(), "scan exceeded bounded work") {
		t.Fatalf("rare-match scan did not fail closed: %v", err)
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

func TestSortCodeWorktreeCandidatesUsesKindThenNewestCreationAndCanonicalIDDescending(t *testing.T) {
	now := time.Now().UTC()
	candidates := []codeWorktreeCandidate{
		{localCodeTarget: localCodeTarget{CodeIdentity: CodeIdentity{Worktree: "train-old"}, Kind: "train"}, CreatedAt: now.Add(-time.Hour), SortID: "GTW-TRN2"},
		{localCodeTarget: localCodeTarget{CodeIdentity: CodeIdentity{Worktree: "hotfix-tie-b"}, Kind: "hotfix"}, CreatedAt: now, SortID: "b-fix"},
		{localCodeTarget: localCodeTarget{CodeIdentity: CodeIdentity{Worktree: "main"}, Kind: "main"}, SortID: "main"},
		{localCodeTarget: localCodeTarget{CodeIdentity: CodeIdentity{Worktree: "train-new"}, Kind: "train"}, CreatedAt: now, SortID: "GTW-TRN3"},
		{localCodeTarget: localCodeTarget{CodeIdentity: CodeIdentity{Worktree: "hotfix-new"}, Kind: "hotfix"}, CreatedAt: now.Add(time.Minute), SortID: "new-fix"},
		{localCodeTarget: localCodeTarget{CodeIdentity: CodeIdentity{Worktree: "hotfix-tie-a"}, Kind: "hotfix"}, CreatedAt: now, SortID: "a-fix"},
	}
	sortCodeWorktreeCandidates(candidates)
	want := []string{"main", "hotfix-new", "hotfix-tie-b", "hotfix-tie-a", "train-new", "train-old"}
	got := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		got = append(got, candidate.CodeIdentity.Worktree)
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("candidate order=%v want=%v", got, want)
	}
}

func TestCodeWorktreePagePacksByTokensAndPreservesOrder(t *testing.T) {
	items := make([]CodeWorktreeItem, 40)
	for index := range items {
		items[index] = CodeWorktreeItem{Selector: fmt.Sprintf("WT-MAIN-%08x", index+1), Kind: "main", Label: strings.Repeat(fmt.Sprintf("item-%02d ", index), 40)}
	}
	kind := "code-worktree|example|"
	cursor := ""
	got := make([]string, 0, len(items))
	for pageNumber := 0; ; pageNumber++ {
		if pageNumber > len(items) {
			t.Fatal("worktree pagination did not terminate")
		}
		page, nextCursor, err := codeWorktreePage(kind, items, cursor)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range page {
			got = append(got, item.Selector)
		}
		if nextCursor == "" {
			break
		}
		cursor = nextCursor
	}
	if len(got) != len(items) {
		t.Fatalf("paged %d items, want %d", len(got), len(items))
	}
	for index, item := range items {
		if got[index] != item.Selector {
			t.Fatalf("item %d=%q want %q", index, got[index], item.Selector)
		}
	}
}

func TestCodeWorktreeOrdersMainThenUnmergedHotfixesAndSkipsLegacy(t *testing.T) {
	f := newLocalCodeFixture(t)
	runner := gitx.Runner{StateDir: f.service.Config.StateDir, MaxReadBytes: 1 << 20, MaxDiffBytes: 1 << 20, MaxListItems: 100}
	now := time.Now().UTC()
	addHotfix := func(slug, content string, createdAt time.Time) {
		t.Helper()
		lane := filepath.Join(f.service.Config.StateDir, "hotfix-worktrees", "example", slug)
		if err := os.MkdirAll(filepath.Dir(lane), 0o700); err != nil {
			t.Fatal(err)
		}
		branch := "hotfix/" + slug
		testutil.Git(t, f.root, "branch", branch, f.current)
		testutil.Git(t, f.root, "worktree", "add", lane, branch)
		if err := os.WriteFile(filepath.Join(lane, slug+".txt"), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		testutil.Git(t, lane, "add", slug+".txt")
		testutil.Git(t, lane, "commit", "-m", slug+" fixture")
		if err := runner.RecordHotfixIdentity(f.service.Config.StateDir, gitx.HotfixIdentity{ProjectID: "example", HotfixRef: "refs/heads/" + branch, TaskID: "EXM-TSK1", BaseSHA: f.current, CreatedAt: createdAt}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			testutil.Git(t, f.root, "worktree", "remove", "--force", lane)
			testutil.Git(t, f.root, "branch", "-D", branch)
		})
	}
	addHotfix("new", "new\n", now.Add(time.Minute))
	addHotfix("old", "old\n", now)
	legacyLane := filepath.Join(f.service.Config.StateDir, "hotfix-worktrees", "example", "legacy")
	testutil.Git(t, f.root, "branch", "hotfix/legacy", f.current)
	testutil.Git(t, f.root, "worktree", "add", legacyLane, "hotfix/legacy")
	if err := runner.RecordHotfixIdentity(f.service.Config.StateDir, gitx.HotfixIdentity{ProjectID: "example", HotfixRef: "refs/heads/hotfix/legacy", TaskID: "EXM-TSK1", BaseSHA: f.current}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		testutil.Git(t, f.root, "worktree", "remove", "--force", legacyLane)
		testutil.Git(t, f.root, "branch", "-D", "hotfix/legacy")
	})

	result, err := f.service.CodeWorktree(context.Background(), CodeWorktreeInput{ProjectID: "example"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 3 || result.Items[0].Kind != "main" || result.Items[1].Label != "new" || result.Items[2].Label != "old" {
		t.Fatalf("unexpected ordered worktrees: %#v", result.Items)
	}
	if strings.Contains(fmt.Sprint(result.Items), "legacy") {
		t.Fatalf("legacy hotfix was exposed: %#v", result.Items)
	}
}

func TestCodeWorktreeIgnoresStaleTrainAndOrphanHotfixRegistrations(t *testing.T) {
	f := newLocalCodeFixture(t)
	staleTrain := filepath.Join(t.TempDir(), "GTW-TRN7")
	testutil.Git(t, f.root, "branch", "train/GTW-TRN7", f.current)
	testutil.Git(t, f.root, "worktree", "add", staleTrain, "train/GTW-TRN7")
	t.Cleanup(func() {
		testutil.Git(t, f.root, "worktree", "remove", "--force", staleTrain)
		testutil.Git(t, f.root, "branch", "-D", "train/GTW-TRN7")
	})
	orphanHotfix := filepath.Join(t.TempDir(), "orphan-hotfix")
	testutil.Git(t, f.root, "branch", "hotfix/orphan", f.current)
	testutil.Git(t, f.root, "worktree", "add", orphanHotfix, "hotfix/orphan")
	t.Cleanup(func() {
		testutil.Git(t, f.root, "worktree", "remove", "--force", orphanHotfix)
		testutil.Git(t, f.root, "branch", "-D", "hotfix/orphan")
	})

	result, err := f.service.CodeWorktree(context.Background(), CodeWorktreeInput{ProjectID: "example"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 1 || result.Items[0].Kind != "main" {
		t.Fatalf("stale/orphan Git registrations were exposed: %#v", result.Items)
	}
}

func TestCodeWorktreeFailsClosedForAuthoritativeHotfixWithoutManagedBinding(t *testing.T) {
	f := newLocalCodeFixture(t)
	runner := gitx.Runner{StateDir: f.service.Config.StateDir, MaxReadBytes: 1 << 20, MaxDiffBytes: 1 << 20, MaxListItems: 100}
	if err := runner.RecordHotfixIdentity(f.service.Config.StateDir, gitx.HotfixIdentity{
		ProjectID: "example", HotfixRef: "refs/heads/hotfix/missing", TaskID: "EXM-TSK1", BaseSHA: f.current, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.CodeWorktree(context.Background(), CodeWorktreeInput{ProjectID: "example"}); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("authoritative hotfix without a managed binding was not rejected: %v", err)
	}
}

func TestCodeWorktreeFailsClosedForAuthoritativeHotfixWithUnexpectedBinding(t *testing.T) {
	f := newLocalCodeFixture(t)
	runner := gitx.Runner{StateDir: f.service.Config.StateDir, MaxReadBytes: 1 << 20, MaxDiffBytes: 1 << 20, MaxListItems: 100}
	branch := "hotfix/unexpected"
	lane := filepath.Join(t.TempDir(), "unexpected-hotfix")
	testutil.Git(t, f.root, "branch", branch, f.current)
	testutil.Git(t, f.root, "worktree", "add", lane, branch)
	t.Cleanup(func() {
		testutil.Git(t, f.root, "worktree", "remove", "--force", lane)
		testutil.Git(t, f.root, "branch", "-D", branch)
	})
	if err := runner.RecordHotfixIdentity(f.service.Config.StateDir, gitx.HotfixIdentity{
		ProjectID: "example", HotfixRef: "refs/heads/" + branch, TaskID: "EXM-TSK1", BaseSHA: f.current, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.CodeWorktree(context.Background(), CodeWorktreeInput{ProjectID: "example"}); err == nil || !strings.Contains(err.Error(), "server-owned") {
		t.Fatalf("authoritative hotfix with an unexpected binding was not rejected: %v", err)
	}
}

func TestCodeWorktreeEnumeratesGitWorktreesOnceForMultipleAuthoritativeLanes(t *testing.T) {
	f := newLocalCodeFixture(t)
	runner := gitx.Runner{StateDir: f.service.Config.StateDir, MaxReadBytes: 1 << 20, MaxDiffBytes: 1 << 20, MaxListItems: 100}
	project := f.service.Config.Projects["example"]
	project.ProjectCode = "EXM"
	f.service.Config.Projects["example"] = project
	now := time.Now().UTC()
	addLane := func(kind, id string) string {
		t.Helper()
		branch := kind + "/" + id
		lane := filepath.Join(f.service.Config.StateDir, "hotfix-worktrees", "example", id)
		if kind == "hotfix" {
			if err := os.MkdirAll(filepath.Dir(lane), 0o700); err != nil {
				t.Fatal(err)
			}
		}
		testutil.Git(t, f.root, "branch", branch, f.current)
		testutil.Git(t, f.root, "worktree", "add", lane, branch)
		name := id + ".txt"
		if err := os.WriteFile(filepath.Join(lane, name), []byte(id+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		testutil.Git(t, lane, "add", name)
		testutil.Git(t, lane, "commit", "-m", id+" fixture")
		t.Cleanup(func() {
			testutil.Git(t, f.root, "worktree", "remove", "--force", lane)
			testutil.Git(t, f.root, "branch", "-D", branch)
		})
		return strings.TrimSpace(testutil.Git(t, lane, "rev-parse", "HEAD"))
	}
	for _, id := range []string{"fix-a", "fix-b"} {
		head := addLane("hotfix", id)
		if err := runner.RecordHotfixIdentity(f.service.Config.StateDir, gitx.HotfixIdentity{ProjectID: "example", HotfixRef: "refs/heads/hotfix/" + id, TaskID: "EXM-TSK1", BaseSHA: f.current, CreatedAt: now}); err != nil {
			t.Fatal(err)
		}
		if head == "" {
			t.Fatal("hotfix fixture has no head")
		}
	}
	for _, id := range []string{"EXM-TRN1", "EXM-TRN2"} {
		lane, err := trainv2.CompactWorktreePath(f.service.Config.StateDir, "EXM", id)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(lane), 0o700); err != nil {
			t.Fatal(err)
		}
		branch := "train/" + id
		testutil.Git(t, f.root, "branch", branch, f.current)
		testutil.Git(t, f.root, "worktree", "add", lane, branch)
		name := id + ".txt"
		if err := os.WriteFile(filepath.Join(lane, name), []byte(id+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		testutil.Git(t, lane, "add", name)
		testutil.Git(t, lane, "commit", "-m", id+" fixture")
		t.Cleanup(func() {
			testutil.Git(t, f.root, "worktree", "remove", "--force", lane)
			testutil.Git(t, f.root, "branch", "-D", branch)
		})
		finished := now.Add(time.Minute)
		train := model.TrainV2{SchemaVersion: model.TrainV2SchemaVersion, ID: id, ProjectID: "example", Revision: 1, Status: model.TrainV2ReadyForIntegration, CreatedBy: "inventory-regression", CreatedAt: now, UpdatedAt: now, Items: []model.TrainV2Item{{Position: 0, TaskID: "EXM-TSK1", TaskRevision: 1, TaskRevisionSHA256: strings.Repeat("a", 64), Status: model.TrainV2ItemFinalized, AddedAt: now, Attempts: []model.TrainV2Attempt{{Number: 1, Status: model.TrainV2AttemptSucceeded, AgentID: "agent-" + id, AirelaySessionKey: "session-" + id, GatewayID: "gateway-" + id, StartHead: f.base, StartedAt: now, FinishedAt: &finished}}}}}
		payload, err := json.Marshal(train)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.service.Durability.CommitSharedMutation(context.Background(), sqlitestore.SharedMutation{OperationID: "OPR-CODE-INVENTORY-" + id, EntityType: "train", EntityID: id, Revision: 1, Kind: "inventory-regression", Payload: payload, Create: true}); err != nil {
			t.Fatal(err)
		}
		runtime := trainv2.RuntimeBinding{SchemaVersion: 1, ProjectID: "example", ProjectCode: "EXM", TrainID: id, WorktreePath: lane, AgentID: "agent-" + id, SessionKey: "session-" + id, TaskID: "EXM-TSK1", AttemptNumber: 1, StartedAt: now}
		runtimeBytes, err := json.Marshal(runtime)
		if err != nil {
			t.Fatal(err)
		}
		runtimePath := trainv2.RuntimePath(f.service.Config.StateDir, "example", id)
		if err := os.MkdirAll(filepath.Dir(runtimePath), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(runtimePath, runtimeBytes, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	binDir := t.TempDir()
	counter := filepath.Join(t.TempDir(), "worktree-list-count")
	if err := os.WriteFile(counter, []byte("0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	wrapper := filepath.Join(binDir, "git")
	script := fmt.Sprintf("#!/bin/sh\ncase \" $* \" in *\" worktree list \"*) n=$(awk 'NR==1 {print $1}' %q); echo $((n+1)) > %q;; esac\nexec %q \"$@\"\n", counter, counter, realGit)
	if err := os.WriteFile(wrapper, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if _, err := f.service.CodeWorktree(context.Background(), CodeWorktreeInput{ProjectID: "example"}); err != nil {
		t.Fatal(err)
	}
	countBytes, err := os.ReadFile(counter)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(countBytes)); got != "1" {
		t.Fatalf("Git worktree inventory enumerated %s times, want once", got)
	}
}

func seedCodeTrainWithLegacyRuntime(t *testing.T, f localCodeFixture, status string) string {
	t.Helper()
	project := f.service.Config.Projects["example"]
	project.ProjectCode = "EXM"
	f.service.Config.Projects["example"] = project
	trainID := "EXM-TRN2"
	trainPath, err := trainv2.CompactWorktreePath(f.service.Config.StateDir, project.ProjectCode, trainID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(trainPath), 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, f.root, "branch", "train/"+trainID, f.current)
	testutil.Git(t, f.root, "worktree", "add", trainPath, "train/"+trainID)
	if err := os.WriteFile(filepath.Join(trainPath, "train-only.txt"), []byte("train lane\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, trainPath, "add", "train-only.txt")
	testutil.Git(t, trainPath, "commit", "-m", "train lane fixture")
	t.Cleanup(func() {
		testutil.Git(t, f.root, "worktree", "remove", "--force", trainPath)
		testutil.Git(t, f.root, "branch", "-D", "train/"+trainID)
	})
	now := time.Now().UTC()
	train := model.TrainV2{
		SchemaVersion: model.TrainV2SchemaVersion,
		ID:            trainID,
		ProjectID:     "example",
		Revision:      1,
		Status:        status,
		CreatedBy:     "runtime-regression",
		CreatedAt:     now,
		UpdatedAt:     now,
		Items: []model.TrainV2Item{{
			Position:           0,
			TaskID:             "EXM-TSK1",
			TaskRevision:       1,
			TaskRevisionSHA256: strings.Repeat("a", 64),
			Status:             model.TrainV2ItemQueued,
			AddedAt:            now,
		}},
	}
	payload, err := json.Marshal(train)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Durability.CommitSharedMutation(context.Background(), sqlitestore.SharedMutation{
		OperationID: "OPR-LEGACY-RUNTIME-" + strings.ToLower(status),
		EntityType:  "train",
		EntityID:    trainID,
		Revision:    1,
		Kind:        "runtime-regression",
		Payload:     payload,
		Create:      true,
	}); err != nil {
		t.Fatal(err)
	}
	runtimePath := trainv2.RuntimePath(f.service.Config.StateDir, "example", trainID)
	if err := os.MkdirAll(filepath.Dir(runtimePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(runtimePath, []byte(`{"schema_version":1,"project_id":"example","train_id":"EXM-TRN2","run_id":"legacy-run"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return trainID
}

func TestCodeWorktreeSkipsInactiveTrainBeforeRuntimeDecode(t *testing.T) {
	f := newLocalCodeFixture(t)
	trainID := seedCodeTrainWithLegacyRuntime(t, f, model.TrainV2Planned)
	result, err := f.service.CodeWorktree(context.Background(), CodeWorktreeInput{ProjectID: "example"})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range result.Items {
		if item.TrainID == trainID {
			t.Fatalf("inactive Train with obsolete runtime was exposed: %#v", result)
		}
	}
	if len(result.Items) != 1 || result.Items[0].Kind != "main" {
		t.Fatalf("inactive Train changed the code/worktree result: %#v", result)
	}
}

func TestCodeWorktreeFailsClosedForRunningTrainWithInvalidRuntime(t *testing.T) {
	f := newLocalCodeFixture(t)
	trainID := seedCodeTrainWithLegacyRuntime(t, f, model.TrainV2Running)
	if _, err := f.service.CodeWorktree(context.Background(), CodeWorktreeInput{ProjectID: "example"}); err == nil || !strings.Contains(err.Error(), trainID) {
		t.Fatalf("running Train with obsolete runtime was not rejected: %v", err)
	}
}

func TestCodeTrainBaseUsesCanonicalStartBaseForMultiItemTrain(t *testing.T) {
	f := newLocalCodeFixture(t)
	train := model.TrainV2{
		ID: "GTW-TRN63", ProjectID: "example",
		Items: []model.TrainV2Item{
			{TaskID: "GTW-TSK1", SuccessfulAttemptNumber: 1, Attempts: []model.TrainV2Attempt{{StartHead: f.base, Status: model.TrainV2AttemptSucceeded}}},
			{TaskID: "GTW-TSK2", SuccessfulAttemptNumber: 1, Attempts: []model.TrainV2Attempt{{StartHead: f.current, Status: model.TrainV2AttemptSucceeded}}},
		},
		Status: model.TrainV2ReadyForIntegration,
	}
	base, err := codeTrainBase(train, f.current)
	if err != nil {
		t.Fatal(err)
	}
	if base != f.base {
		t.Fatalf("multi-item Train base=%q want canonical start base %q", base, f.base)
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
	allDiff := live.Diff
	for pages := 0; live.Pagination != nil; pages++ {
		if pages > 100 {
			t.Fatal("live diff pagination did not terminate")
		}
		live, err = f.service.CodeDiff(context.Background(), CodeDiffInput{ProjectID: "example", Worktree: selector, Live: true, Cursor: live.Pagination.NextCursor})
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

func TestCodeDiffRejectsOversizedSemanticLineWithoutPagination(t *testing.T) {
	f := newLocalCodeFixture(t)
	f.service.Git.MaxDiffBytes = 64
	if err := os.WriteFile(filepath.Join(f.root, "oversized.txt"), []byte(strings.Repeat("x", 128)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	selector := "WT-MAIN-" + f.current[:8]
	_, err := f.service.CodeDiff(context.Background(), CodeDiffInput{
		ProjectID: "example", Worktree: selector, Live: true, Paths: []string{"oversized.txt"},
	})
	if err == nil || !strings.Contains(err.Error(), "internal byte safety limit") {
		t.Fatalf("oversized semantic line was not rejected without pagination: %v", err)
	}
}
