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

func TestSortCodeWorktreeCandidatesUsesKindThenNewestCreationAndCanonicalIDDescending(t *testing.T) {
	now := time.Now().UTC()
	candidates := []codeWorktreeCandidate{
		{localCodeTarget: localCodeTarget{
			CodeIdentity: CodeIdentity{
				Worktree: "task-old",
			},
			Kind: "task",
		}, CreatedAt: now.Add(-time.Hour), SortID: "GTW-TRN2"},
		{localCodeTarget: localCodeTarget{
			CodeIdentity: CodeIdentity{
				Worktree: "task-tie-b",
			},
			Kind: "task",
		}, CreatedAt: now, SortID: "b-fix"},
		{localCodeTarget: localCodeTarget{
			CodeIdentity: CodeIdentity{
				Worktree: "main",
			},
			Kind: "main",
		}, SortID: "main"},
		{localCodeTarget: localCodeTarget{
			CodeIdentity: CodeIdentity{
				Worktree: "task-new",
			},
			Kind: "task",
		}, CreatedAt: now, SortID: "GTW-TRN3"},
		{localCodeTarget: localCodeTarget{
			CodeIdentity: CodeIdentity{
				Worktree: "task-newer",
			},
			Kind: "task",
		}, CreatedAt: now.Add(time.Minute), SortID: "new-fix"},
		{localCodeTarget: localCodeTarget{
			CodeIdentity: CodeIdentity{
				Worktree: "task-tie-a",
			},
			Kind: "task",
		}, CreatedAt: now, SortID: "a-fix"},
	}
	sortCodeWorktreeCandidates(candidates)
	want := []string{"main", "task-newer", "task-tie-b", "task-tie-a", "task-new", "task-old"}
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
		items[index] = CodeWorktreeItem{
			Selector: fmt.Sprintf("WT-MAIN-%08x", index+1),
			Kind:     "main",
			Label:    strings.Repeat(fmt.Sprintf("item-%02d ", index), 40),
		}
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

func TestCodeWorktreePaginatesOverMoreThan100ManagedIdentitiesAndKeepsExactSelectors(t *testing.T) {
	f := newLocalCodeFixture(t)
	ctx := context.Background()
	const identityCount = 101
	now := time.Now().UTC().Truncate(time.Nanosecond)
	selectors := make([]string, 0, identityCount)
	for index := 1; index <= identityCount; index++ {
		taskID := fmt.Sprintf("EXM-TSK%d", index)
		lane := filepath.Join(f.service.Config.StateDir, "task-worktrees", "example", taskID)
		if err := os.MkdirAll(filepath.Dir(lane), 0o700); err != nil {
			t.Fatal(err)
		}
		branch := "task/" + taskID + "-lane"
		testutil.Git(t, f.root, "branch", branch, f.current)
		testutil.Git(t, f.root, "worktree", "add", lane, branch)
		t.Cleanup(func() {
			testutil.Git(t, f.root, "worktree", "remove", "--force", lane)
			testutil.Git(t, f.root, "branch", "-D", branch)
		})
		head := strings.TrimSpace(testutil.Git(t, lane, "rev-parse", "HEAD"))
		selector := fmt.Sprintf("WT-TSK%d-%s", index, strings.ToLower(head[:8]))
		selectors = append(selectors, selector)
		if err := f.service.Durability.CreateTaskExecutionState(ctx, model.TaskExecutionState{
			TaskID: taskID, ProjectID: "example", TaskRevision: 1, TaskRevisionSHA256: strings.Repeat("a", 64),
			Status: model.TaskExecutionInProgress, Stage: "code", Worktree: selector,
			BaseHead: f.base, Head: head, Branch: branch, Agent: "gtw-worker",
			ExecutionRevision: 1, UpdatedAt: now.Add(time.Duration(index) * time.Second),
		}); err != nil {
			t.Fatal(err)
		}
	}

	first, err := f.service.CodeWorktree(ctx, CodeWorktreeInput{ProjectID: "example"})
	if err != nil {
		t.Fatal(err)
	}
	if first.Pagination == nil || first.Pagination.NextCursor == "" || len(first.Items) < 2 || first.Items[0].Kind != "main" {
		t.Fatalf("first bounded page=%#v", first)
	}
	if _, err := f.service.CodeTree(ctx, CodeTreeInput{
		ProjectID: "example",
		Worktree:  selectors[identityCount-1],
	}); err != nil {
		t.Fatalf("exact selector failed alongside collection paging: %v", err)
	}

	seen := make(map[string]struct{}, identityCount+1)
	for _, item := range first.Items {
		if _, duplicate := seen[item.Selector]; duplicate {
			t.Fatalf("duplicate selector on first page: %q", item.Selector)
		}
		seen[item.Selector] = struct{}{}
	}
	pages := 1
	cursor := first.Pagination.NextCursor
	for cursor != "" {
		page, pageErr := f.service.CodeWorktree(ctx, CodeWorktreeInput{
			ProjectID: "example",
			Cursor:    cursor,
		})
		if pageErr != nil {
			t.Fatalf("page %d: %v", pages+1, pageErr)
		}
		pages++
		for _, item := range page.Items {
			if _, duplicate := seen[item.Selector]; duplicate {
				t.Fatalf("selector repeated across pages: %q", item.Selector)
			}
			seen[item.Selector] = struct{}{}
		}
		cursor = ""
		if page.Pagination != nil {
			cursor = page.Pagination.NextCursor
		}
		if pages > identityCount+1 {
			t.Fatal("worktree pagination did not terminate")
		}
	}
	if pages < 2 || len(seen) != identityCount+1 {
		t.Fatalf("paged identities=%d pages=%d want identities=%d and multiple pages", len(seen), pages, identityCount+1)
	}
}

func TestCodeWorktreeSkipsHistoricalHotfixLanesAndRecords(t *testing.T) {
	f := newLocalCodeFixture(t)
	now := time.Now().UTC()
	addHotfix := func(slug string, createdAt time.Time) {
		t.Helper()
		lane := filepath.Join(f.service.Config.StateDir, "hotfix-worktrees", "example", slug)
		if err := os.MkdirAll(filepath.Dir(lane), 0o700); err != nil {
			t.Fatal(err)
		}
		branch := "hotfix/" + slug
		testutil.Git(t, f.root, "branch", branch, f.current)
		testutil.Git(t, f.root, "worktree", "add", lane, branch)
		if err := os.WriteFile(filepath.Join(lane, slug+".txt"), []byte(slug+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		testutil.Git(t, lane, "add", slug+".txt")
		testutil.Git(t, lane, "commit", "-m", slug+" fixture")
		identityPath := filepath.Join(f.service.Config.StateDir, "hotfix-identities", "example", slug+".json")
		if err := os.MkdirAll(filepath.Dir(identityPath), 0o700); err != nil {
			t.Fatal(err)
		}
		payload := fmt.Sprintf(`{"project_id":"example","hotfix_ref":"refs/heads/%s","task_id":"EXM-TSK1","base_sha":%q,"created_at":%q}`, branch, f.current, createdAt.Format(time.RFC3339))
		if err := os.WriteFile(identityPath, []byte(payload), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			testutil.Git(t, f.root, "worktree", "remove", "--force", lane)
			testutil.Git(t, f.root, "branch", "-D", branch)
		})
	}
	addHotfix("new", now.Add(time.Minute))
	addHotfix("old", now)

	result, err := f.service.CodeWorktree(context.Background(), CodeWorktreeInput{ProjectID: "example"})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range result.Items {
		if item.Kind == "hotfix" || item.Label == "new" || item.Label == "old" {
			t.Fatalf("historical hotfix lane was enumerated as live: %#v", item)
		}
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
