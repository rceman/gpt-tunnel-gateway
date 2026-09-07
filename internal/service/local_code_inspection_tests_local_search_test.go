package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/gitx"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestCodeWorktreeKeepsOldBaseHotfixVisibleAndCodeReadResolvesIt(t *testing.T) {
	f := newLocalCodeFixture(t)
	runner := gitx.Runner{StateDir: f.service.Config.StateDir, MaxReadBytes: 1 << 20, MaxDiffBytes: 1 << 20, MaxListItems: 100}
	slug := "old-base"
	branch := "hotfix/" + slug
	lane := filepath.Join(f.service.Config.StateDir, "hotfix-worktrees", "example", slug)
	if err := os.MkdirAll(filepath.Dir(lane), 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, f.root, "branch", branch, f.base)
	testutil.Git(t, f.root, "worktree", "add", lane, branch)
	t.Cleanup(func() {
		testutil.Git(t, f.root, "worktree", "remove", "--force", lane)
		testutil.Git(t, f.root, "branch", "-D", branch)
	})
	if err := runner.RecordHotfixIdentity(f.service.Config.StateDir, gitx.HotfixIdentity{
		ProjectID: "example", HotfixRef: "refs/heads/" + branch, TaskID: "EXM-TSK1", BaseSHA: f.base, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	worktrees, err := f.service.CodeWorktree(context.Background(), CodeWorktreeInput{ProjectID: "example"})
	if err != nil {
		t.Fatal(err)
	}
	var selector string
	for _, item := range worktrees.Items {
		if item.Kind == "hotfix" && item.Label == slug {
			selector = item.Selector
			if item.Head != f.base {
				t.Fatalf("old-base hotfix head=%q, want %q", item.Head, f.base)
			}
		}
	}
	if selector == "" {
		t.Fatalf("old-base hotfix was omitted: %#v", worktrees.Items)
	}
	read, err := f.service.CodeRead(context.Background(), CodeReadInput{ProjectID: "example", Worktree: selector, Path: "tracked.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if read.CurrentHead != strings.ToLower(f.base[:8]) || read.Content != "base tracked content\n" {
		t.Fatalf("old-base code/read=%#v", read)
	}
}

func TestCodeSelectorsRemainDistinctWhenHeadsMatch(t *testing.T) {
	head := strings.Repeat("a", 40)
	mainSelector, err := codeSelector("", head)
	if err != nil {
		t.Fatal(err)
	}
	hotfixSelector, err := codeHotfixSelector("same-head", head)
	if err != nil {
		t.Fatal(err)
	}
	if mainSelector == hotfixSelector {
		t.Fatalf("main and hotfix selectors collided: %q", mainSelector)
	}
}

func TestCodeActionsResolveDirtyManagedHotfixLive(t *testing.T) {
	f := newLocalCodeFixture(t)
	runner := gitx.Runner{StateDir: f.service.Config.StateDir, MaxReadBytes: 1 << 20, MaxDiffBytes: 1 << 20, MaxListItems: 100}
	slug := "agent-live"
	branch := "hotfix/" + slug
	lane := filepath.Join(f.service.Config.StateDir, "hotfix-worktrees", "example", slug)
	if err := os.MkdirAll(filepath.Dir(lane), 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, f.root, "branch", branch, f.current)
	testutil.Git(t, f.root, "worktree", "add", lane, branch)
	t.Cleanup(func() {
		testutil.Git(t, f.root, "worktree", "remove", "--force", lane)
		testutil.Git(t, f.root, "branch", "-D", branch)
	})
	if err := os.WriteFile(filepath.Join(lane, "committed.txt"), []byte("managed hotfix\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, lane, "add", "committed.txt")
	testutil.Git(t, lane, "commit", "-m", "managed hotfix")
	if err := runner.RecordHotfixIdentity(f.service.Config.StateDir, gitx.HotfixIdentity{
		ProjectID: "example", HotfixRef: "refs/heads/" + branch, TaskID: "EXM-TSK1", BaseSHA: f.current, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lane, "dirty.txt"), []byte("live-hotfix-marker\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lane, "untracked.txt"), []byte("live-untracked-marker\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	worktrees, err := f.service.CodeWorktree(context.Background(), CodeWorktreeInput{ProjectID: "example"})
	if err != nil {
		t.Fatal(err)
	}
	var selector, head string
	for _, item := range worktrees.Items {
		if item.Kind == "hotfix" && item.Label == slug {
			selector, head = item.Selector, item.Head
			if !item.Dirty {
				t.Fatal("managed hotfix was not reported dirty")
			}
		}
	}
	if selector == "" {
		t.Fatalf("managed hotfix %q not present in CodeWorktree result: %#v", slug, worktrees.Items)
	}

	read, err := f.service.CodeRead(context.Background(), CodeReadInput{ProjectID: "example", Worktree: selector, Path: "dirty.txt", Live: true})
	if err != nil {
		t.Fatal(err)
	}
	if read.CurrentHead != head[:8] || read.Content != "live-hotfix-marker\n" {
		t.Fatalf("CodeRead resolved a different live target: %#v", read)
	}
	search, err := f.service.CodeSearch(context.Background(), CodeSearchInput{ProjectID: "example", Worktree: selector, Query: "live-hotfix-marker", Paths: []string{"dirty.txt"}, Live: true})
	if err != nil {
		t.Fatal(err)
	}
	if search.CurrentHead != head || len(search.Matches) != 1 || search.Matches[0].Path != "dirty.txt" {
		t.Fatalf("CodeSearch resolved a different live target: %#v", search)
	}
	tree, err := f.service.CodeTree(context.Background(), CodeTreeInput{ProjectID: "example", Worktree: selector, Live: true})
	if err != nil {
		t.Fatal(err)
	}
	foundUntracked := false
	for _, pathName := range tree.Paths {
		if pathName == "untracked.txt" {
			foundUntracked = true
		}
	}
	if tree.CurrentHead != head || !foundUntracked {
		t.Fatalf("CodeTree did not expose the same live target: %#v", tree)
	}
	diff, err := f.service.CodeDiff(context.Background(), CodeDiffInput{ProjectID: "example", Worktree: selector, Paths: []string{"dirty.txt", "untracked.txt"}, Live: true})
	if err != nil {
		t.Fatal(err)
	}
	if diff.CurrentHead != head || !strings.Contains(diff.Diff, "live-hotfix-marker") || !strings.Contains(diff.Diff, "live-untracked-marker") {
		t.Fatalf("CodeDiff did not expose the same live target: %#v", diff)
	}
	if _, err := f.service.CodeRead(context.Background(), CodeReadInput{ProjectID: "example", Worktree: selector, Path: "dirty.txt"}); err == nil {
		t.Fatal("CodeRead live=false unexpectedly accepted dirty managed hotfix")
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

func TestLocalCodeSearchReturnsBoundedContextLines(t *testing.T) {
	f := newLocalCodeFixture(t)
	pathName := filepath.Join(f.root, "context.txt")
	if err := os.WriteFile(pathName, []byte("before\nneedle\nafter\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(pathName) })
	selector := "WT-MAIN-" + f.current[:8]
	result, err := f.service.CodeSearch(context.Background(), CodeSearchInput{
		ProjectID: "example", Worktree: selector, Query: "needle", Paths: []string{"context.txt"}, ContextLines: 1, Live: true,
	})
	if err != nil || len(result.Matches) != 1 || result.Matches[0].Snippet != "before\nneedle\nafter" {
		t.Fatalf("context search result = %#v, err=%v", result, err)
	}
	zero, err := f.service.CodeSearch(context.Background(), CodeSearchInput{
		ProjectID: "example", Worktree: selector, Query: "needle", Paths: []string{"context.txt"}, Live: true,
	})
	if err != nil || len(zero.Matches) != 1 || zero.Matches[0].Snippet != "needle" || len(zero.Matches[0].Snippet) > 240 {
		t.Fatalf("context_lines=0 result = %#v, err=%v", zero, err)
	}
	longBefore := strings.Repeat("before-context ", 30)
	if err := os.WriteFile(pathName, []byte(longBefore+"\nneedle\nafter\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err = f.service.CodeSearch(context.Background(), CodeSearchInput{
		ProjectID: "example", Worktree: selector, Query: "needle", Paths: []string{"context.txt"}, ContextLines: 1, Live: true,
	})
	if err != nil || len(result.Matches) != 1 || !strings.Contains(result.Matches[0].Snippet, "needle") || len(result.Matches[0].Snippet) > 240 {
		t.Fatalf("long preceding context removed matched line: %#v, err=%v", result, err)
	}
	for _, contextLines := range []int{-1, 4} {
		_, err := f.service.CodeSearch(context.Background(), CodeSearchInput{
			ProjectID: "example", Worktree: selector, Query: "needle", Paths: []string{"context.txt"}, ContextLines: contextLines, Live: true,
		})
		if err == nil || !strings.Contains(err.Error(), "context_lines") {
			t.Fatalf("context_lines=%d was accepted: %v", contextLines, err)
		}
	}
}
