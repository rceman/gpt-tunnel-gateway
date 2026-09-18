package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestCodeWorktreeIgnoresHistoricalHotfixLane(t *testing.T) {
	f := newLocalCodeFixture(t)
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

	worktrees, err := f.service.CodeWorktree(context.Background(), CodeWorktreeInput{ProjectID: "example"})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range worktrees.Items {
		if item.Kind == "hotfix" || item.Label == slug {
			t.Fatalf("historical hotfix lane was enumerated as a live lane: %#v", item)
		}
	}
}

func TestCodeSelectorsRemainDistinctWhenHeadsMatch(t *testing.T) {
	head := strings.Repeat("a", 40)
	mainSelector, err := codeSelector(head)
	if err != nil {
		t.Fatal(err)
	}
	taskSelector := "WT-TSK1-" + strings.ToLower(head[:8])
	if mainSelector == taskSelector {
		t.Fatalf("main and task selectors collided: %q", mainSelector)
	}
}

func TestCodeActionsResolveDirtyManagedTaskLive(t *testing.T) {
	f := newLocalCodeFixture(t)
	taskID := "EXM-TSK7"
	branch := "task/" + taskID + "-lane"
	lane := filepath.Join(f.service.Config.StateDir, "task-worktrees", "example", taskID)
	if err := os.MkdirAll(filepath.Dir(lane), 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, f.root, "branch", branch, f.current)
	testutil.Git(t, f.root, "worktree", "add", lane, branch)
	t.Cleanup(func() {
		testutil.Git(t, f.root, "worktree", "remove", "--force", lane)
		testutil.Git(t, f.root, "branch", "-D", branch)
	})
	if err := os.WriteFile(filepath.Join(lane, "committed.txt"), []byte("managed task\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, lane, "add", "committed.txt")
	testutil.Git(t, lane, "commit", "-m", "managed task")
	head := strings.TrimSpace(testutil.Git(t, lane, "rev-parse", "HEAD"))
	selector := "WT-TSK7-" + strings.ToLower(head[:8])
	if err := f.service.Durability.CreateTaskExecutionState(context.Background(), model.TaskExecutionState{
		TaskID: taskID, ProjectID: "example", TaskRevision: 1, TaskRevisionSHA256: strings.Repeat("a", 64),
		Status: model.TaskExecutionInProgress, Stage: "code", Worktree: selector,
		BaseHead: f.base, Head: head, Branch: branch, Agent: "gtw-worker",
		ExecutionRevision: 1, UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lane, "dirty.txt"), []byte("live-task-marker\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lane, "untracked.txt"), []byte("live-untracked-marker\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	read, err := f.service.CodeRead(context.Background(), CodeReadInput{
		ProjectID: "example",
		Worktree:  selector,
		Path:      "dirty.txt",
		Live:      true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if read.CurrentHead != head[:8] || read.Content != "live-task-marker\n" {
		t.Fatalf("CodeRead resolved a different live target: %#v", read)
	}
	search, err := f.service.CodeSearch(context.Background(), CodeSearchInput{
		ProjectID: "example",
		Worktree:  selector,
		Query:     "live-task-marker",
		Paths:     []string{"dirty.txt"},
		Live:      true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if search.CurrentHead != head || len(search.Matches) != 1 || search.Matches[0].Path != "dirty.txt" {
		t.Fatalf("CodeSearch resolved a different live target: %#v", search)
	}
	tree, err := f.service.CodeTree(context.Background(), CodeTreeInput{
		ProjectID: "example",
		Worktree:  selector,
		Live:      true,
	})
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
	diff, err := f.service.CodeDiff(context.Background(), CodeDiffInput{
		ProjectID: "example",
		Worktree:  selector,
		Paths:     []string{"dirty.txt", "untracked.txt"},
		Live:      true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if diff.CurrentHead != head || !strings.Contains(diff.Diff, "live-task-marker") || !strings.Contains(diff.Diff, "live-untracked-marker") {
		t.Fatalf("CodeDiff did not expose the same live target: %#v", diff)
	}
	if _, err := f.service.CodeRead(context.Background(), CodeReadInput{
		ProjectID: "example",
		Worktree:  selector,
		Path:      "dirty.txt",
	}); err == nil {
		t.Fatal("CodeRead live=false unexpectedly accepted dirty managed task")
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
	installLocalCodeBehaviorDouble(t, f, map[string]string{"many-matches.txt": content.String()})
	t.Cleanup(func() { _ = os.Remove(pathName) })
	selector := "WT-MAIN-" + f.current[:8]
	first, err := f.service.CodeSearch(context.Background(), CodeSearchInput{
		ProjectID: "example",
		Worktree:  selector,
		Query:     "needle",
		Paths:     []string{"many-matches.txt"},
		Live:      true,
	})
	if err != nil || len(first.Matches) < 2 || first.Pagination == nil || first.Pagination.NextCursor == "" {
		t.Fatalf("expected first bounded search page: %#v %v", first, err)
	}
	if len(first.Pagination.NextCursor) > 256 {
		t.Fatalf("search cursor is not bounded: %d", len(first.Pagination.NextCursor))
	}
	second, err := f.service.CodeSearch(context.Background(), CodeSearchInput{
		ProjectID: "example",
		Worktree:  selector,
		Query:     "needle",
		Paths:     []string{"many-matches.txt"},
		Live:      true,
		Cursor:    first.Pagination.NextCursor,
	})
	if err != nil || len(second.Matches) == 0 || second.Matches[0].Line <= first.Matches[len(first.Matches)-1].Line {
		t.Fatalf("expected exact continuation without duplicate: %#v %v", second, err)
	}
}

func TestLocalCodeSearchReturnsBoundedContextLines(t *testing.T) {
	f := newLocalCodeFixture(t)
	pathName := filepath.Join(f.root, "context.txt")
	content := "before\nneedle\nafter\n"
	if err := os.WriteFile(pathName, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	installLocalCodeBehaviorDouble(t, f, map[string]string{"context.txt": content})
	t.Cleanup(func() { _ = os.Remove(pathName) })
	selector := "WT-MAIN-" + f.current[:8]
	result, err := f.service.CodeSearch(context.Background(), CodeSearchInput{
		ProjectID:    "example",
		Worktree:     selector,
		Query:        "needle",
		Paths:        []string{"context.txt"},
		ContextLines: 1,
		Live:         true,
	})
	if err != nil || len(result.Matches) != 1 || result.Matches[0].Snippet != "before\nneedle\nafter" {
		t.Fatalf("context search result = %#v, err=%v", result, err)
	}
	zero, err := f.service.CodeSearch(context.Background(), CodeSearchInput{
		ProjectID: "example",
		Worktree:  selector,
		Query:     "needle",
		Paths:     []string{"context.txt"},
		Live:      true,
	})
	if err != nil || len(zero.Matches) != 1 || zero.Matches[0].Snippet != "needle" || len(zero.Matches[0].Snippet) > 240 {
		t.Fatalf("context_lines=0 result = %#v, err=%v", zero, err)
	}
	longBefore := strings.Repeat("before-context ", 30)
	content = longBefore + "\nneedle\nafter\n"
	if err := os.WriteFile(pathName, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	installLocalCodeBehaviorDouble(t, f, map[string]string{"context.txt": content})
	result, err = f.service.CodeSearch(context.Background(), CodeSearchInput{
		ProjectID:    "example",
		Worktree:     selector,
		Query:        "needle",
		Paths:        []string{"context.txt"},
		ContextLines: 1,
		Live:         true,
	})
	if err != nil || len(result.Matches) != 1 || !strings.Contains(result.Matches[0].Snippet, "needle") || len(result.Matches[0].Snippet) > 240 {
		t.Fatalf("long preceding context removed matched line: %#v, err=%v", result, err)
	}
	for _, contextLines := range []int{-1, 4} {
		_, err := f.service.CodeSearch(context.Background(), CodeSearchInput{
			ProjectID:    "example",
			Worktree:     selector,
			Query:        "needle",
			Paths:        []string{"context.txt"},
			ContextLines: contextLines,
			Live:         true,
		})
		if err == nil || !strings.Contains(err.Error(), "context_lines") {
			t.Fatalf("context_lines=%d was accepted: %v", contextLines, err)
		}
	}
}
