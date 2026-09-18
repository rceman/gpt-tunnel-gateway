//go:build liveperformance

package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestLocalCodeInspectionPerformanceProfile(t *testing.T) {
	f := newLocalCodeFixture(t)
	project := f.service.Config.Projects["example"]
	project.ProjectCode = "EXM"
	f.service.Config.Projects["example"] = project

	var content strings.Builder
	for line := 0; line < 512; line++ {
		fmt.Fprintf(&content, "line %d needle\n", line)
	}
	if err := os.WriteFile(filepath.Join(f.root, "large.txt"), []byte(content.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, f.root, "add", "large.txt")
	testutil.Git(t, f.root, "commit", "-m", "performance fixture")
	f.current = strings.TrimSpace(testutil.Git(t, f.root, "rev-parse", "HEAD"))
	testutil.Git(t, f.root, "push", "origin", "main")

	stateDir := f.service.Config.StateDir
	taskPath := filepath.Join(stateDir, "task-worktrees", "example", "EXM-TSK1")
	if err := os.MkdirAll(filepath.Dir(taskPath), 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, f.root, "branch", "task/EXM-TSK1-lane", f.current)
	t.Cleanup(func() {
		testutil.Git(t, f.root, "worktree", "remove", "--force", taskPath)
		testutil.Git(t, f.root, "branch", "-D", "task/EXM-TSK1-lane")
	})
	testutil.Git(t, f.root, "worktree", "add", taskPath, "task/EXM-TSK1-lane")

	db := f.service.Durability
	if db == nil {
		t.Fatal("performance fixture requires Shared durability")
	}
	f.service.Durability = db
	taskHead := strings.TrimSpace(testutil.Git(t, taskPath, "rev-parse", "HEAD"))
	if taskHead == "" {
		taskHead = f.current
	}
	if err := db.CreateTaskExecutionState(context.Background(), model.TaskExecutionState{
		TaskID: "EXM-TSK1", ProjectID: "example", TaskRevision: 1, TaskRevisionSHA256: strings.Repeat("a", 64),
		Status: model.TaskExecutionInProgress, Stage: "code", Worktree: "WT-TSK1-" + strings.ToLower(taskHead[:8]),
		BaseHead: f.base, Head: taskHead, Branch: "task/EXM-TSK1-lane", Agent: "gtw-worker",
		ExecutionRevision: 1, UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(f.root, "tracked.txt"), []byte(content.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	mainSelector := "WT-MAIN-" + f.current[:8]
	measure := func(name string, call func() error) {
		t.Helper()
		started := time.Now()
		err := call()
		elapsed := time.Since(started)
		t.Logf("%s: %dms", name, elapsed.Milliseconds())
		if err != nil {
			t.Fatalf("%s failed: %v", name, err)
		}
	}

	measure("code/worktree first", func() error {
		result, callErr := f.service.CodeWorktree(context.Background(), CodeWorktreeInput{ProjectID: "example"})
		if callErr == nil && (len(result.Items) == 0 || result.Items[0].Head != f.current) {
			callErr = fmt.Errorf("unexpected worktree result: %#v", result)
		}
		return callErr
	})

	measure("code/tree first", func() error {
		result, callErr := f.service.CodeTree(context.Background(), CodeTreeInput{
			ProjectID: "example",
			Worktree:  mainSelector,
			Live:      true,
		})
		if callErr == nil && len(result.Paths) == 0 {
			callErr = fmt.Errorf("unexpected tree result: %#v", result)
		}
		return callErr
	})

	var searchCursor string
	measure("code/search first", func() error {
		result, callErr := f.service.CodeSearch(context.Background(), CodeSearchInput{
			ProjectID: "example",
			Worktree:  mainSelector,
			Live:      true,
			Query:     "needle",
		})
		if callErr == nil && (len(result.Matches) == 0 || result.Pagination == nil || result.Pagination.NextCursor == "") {
			callErr = fmt.Errorf("expected paginated search result: %#v", result)
		}
		if result.Pagination != nil {
			searchCursor = result.Pagination.NextCursor
		}
		return callErr
	})
	measure("code/search continuation", func() error {
		result, callErr := f.service.CodeSearch(context.Background(), CodeSearchInput{
			ProjectID: "example",
			Worktree:  mainSelector,
			Live:      true,
			Query:     "needle",
			Cursor:    searchCursor,
		})
		if callErr == nil && len(result.Matches) == 0 {
			callErr = fmt.Errorf("expected search continuation match: %#v", result)
		}
		return callErr
	})

	var readCursor string
	measure("code/read first", func() error {
		result, callErr := f.service.CodeRead(context.Background(), CodeReadInput{
			ProjectID: "example",
			Worktree:  mainSelector,
			Live:      true,
			Path:      "tracked.txt",
		})
		if callErr == nil && (result.Pagination == nil || result.Pagination.NextCursor == "") {
			callErr = fmt.Errorf("expected paginated read result: %#v", result)
		}
		if result.Pagination != nil {
			readCursor = result.Pagination.NextCursor
		}
		return callErr
	})
	measure("code/read continuation", func() error {
		result, callErr := f.service.CodeRead(context.Background(), CodeReadInput{
			ProjectID: "example",
			Worktree:  mainSelector,
			Live:      true,
			Path:      "tracked.txt",
			Cursor:    readCursor,
		})
		if callErr == nil && result.StartLine <= 1 {
			callErr = fmt.Errorf("expected read continuation to advance: %#v", result)
		}
		return callErr
	})

	var diffCursor string
	measure("code/diff first", func() error {
		result, callErr := f.service.CodeDiff(context.Background(), CodeDiffInput{
			ProjectID: "example",
			Worktree:  mainSelector,
			Live:      true,
			Paths:     []string{"tracked.txt"},
		})
		if callErr == nil && (result.Pagination == nil || result.Pagination.NextCursor == "") {
			callErr = fmt.Errorf("expected paginated diff result: %#v", result)
		}
		if result.Pagination != nil {
			diffCursor = result.Pagination.NextCursor
		}
		return callErr
	})
	measure("code/diff continuation", func() error {
		result, callErr := f.service.CodeDiff(context.Background(), CodeDiffInput{
			ProjectID: "example",
			Worktree:  mainSelector,
			Live:      true,
			Paths:     []string{"tracked.txt"},
			Cursor:    diffCursor,
		})
		if callErr == nil && result.Diff == "" {
			callErr = fmt.Errorf("expected diff continuation bytes: %#v", result)
		}
		return callErr
	})
}
