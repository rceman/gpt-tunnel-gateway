package service

import (
	"context"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestTaskListPageIsBoundedAndProjectsOnlyIndexFields(t *testing.T) {
	s, hubRevision, _ := testService(t)
	ctx := context.Background()
	for i := 0; i < 12; i++ {
		task, operation, err := s.TaskCreate(ctx, TaskCreateInput{
			ProjectID: "example", Slug: "index-" + strings.Repeat("x", i+1), Title: "Index task", Objective: "Not part of the index.",
			AcceptanceCriteria: []string{"bounded"}, OperationClass: "implementation", CreatedBy: "test",
			WriteOptions: WriteOptions{ExpectedHubRevision: hubRevision},
		})
		if err != nil {
			t.Fatal(err)
		}
		if task.ID == "" {
			t.Fatal("task ID was empty")
		}
		hubRevision = operation.Hub.After
	}
	first, err := s.TaskListPage(ctx, TaskListInput{ProjectID: "example"})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Tasks) != indexPageSize || first.Cursor == "" {
		t.Fatalf("unexpected first page: %#v", first)
	}
	concurrent, operation, err := s.TaskCreate(ctx, TaskCreateInput{
		ProjectID: "example", Slug: "concurrent", Title: "Concurrent task", Objective: "Must not enter the pinned page.", AcceptanceCriteria: []string{"bounded"}, OperationClass: "implementation", CreatedBy: "test",
		WriteOptions: WriteOptions{ExpectedHubRevision: hubRevision},
	})
	if err != nil || concurrent.ID == "" {
		t.Fatalf("concurrent task creation failed: %v", err)
	}
	hubRevision = operation.Hub.After
	second, err := s.TaskListPage(ctx, TaskListInput{ProjectID: "example", Cursor: first.Cursor})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Tasks) != 2 || second.Cursor != "" {
		t.Fatalf("unexpected second page: %#v", second)
	}
	seen := map[string]bool{}
	for _, page := range []TaskListPage{first, second} {
		for _, row := range page.Tasks {
			if row.ID == "" || row.Title == "" || row.Status == "" || row.UpdatedAt == "" {
				t.Fatalf("incomplete index row: %#v", row)
			}
			if seen[row.ID] {
				t.Fatalf("duplicate row across pages: %s", row.ID)
			}
			seen[row.ID] = true
		}
	}
	if seen[concurrent.ID] {
		t.Fatal("concurrent task leaked into pinned page")
	}
}

func TestTaskListPageRejectsWrongQueryCursor(t *testing.T) {
	s, hubRevision, _ := testService(t)
	for _, slug := range []string{"cursor-a", "cursor-b"} {
		task, operation, err := s.TaskCreate(context.Background(), TaskCreateInput{
			ProjectID: "example", Slug: slug, Title: "Cursor", Objective: "Cursor validation", AcceptanceCriteria: []string{"cursor"}, OperationClass: "implementation", CreatedBy: "test",
			WriteOptions: WriteOptions{ExpectedHubRevision: hubRevision},
		})
		if err != nil || task.ID == "" {
			t.Fatal(err)
		}
		hubRevision = operation.Hub.After
	}
	page, err := s.TaskListPage(context.Background(), TaskListInput{ProjectID: "example", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if page.Cursor == "" {
		t.Fatal("expected a cursor for the second task")
	}
	if _, err := s.TaskListPage(context.Background(), TaskListInput{ProjectID: "example", Status: "cancelled", Limit: 1, Cursor: page.Cursor}); err == nil {
		t.Fatal("cursor was accepted for a different filter")
	}
	if _, err := s.TaskListPage(context.Background(), TaskListInput{ProjectID: "example", Limit: 2, Cursor: page.Cursor}); err == nil {
		t.Fatal("cursor was accepted for a different page size")
	}
}

func TestTaskNextSelectsCreatedTaskDeterministically(t *testing.T) {
	s, hubRevision, _ := testService(t)
	ctx := context.Background()
	for _, slug := range []string{"next-a", "next-b"} {
		_, operation, err := s.TaskCreate(ctx, TaskCreateInput{
			ProjectID: "example", Slug: slug, Title: slug, Objective: "Next", AcceptanceCriteria: []string{"next"}, OperationClass: "implementation", CreatedBy: "test",
			WriteOptions: WriteOptions{ExpectedHubRevision: hubRevision},
		})
		if err != nil {
			t.Fatal(err)
		}
		hubRevision = operation.Hub.After
	}
	one, err := s.TaskNext(ctx, "example")
	if err != nil || one.Task == nil {
		t.Fatalf("missing next task: %#v %v", one, err)
	}
	two, err := s.TaskNext(ctx, "example")
	if err != nil || two.Task == nil || two.Task.ID != one.Task.ID {
		t.Fatalf("non-deterministic next task: %#v %#v %v", one, two, err)
	}
	if one.Task.LatestRunID != nil || one.Task.LatestRunStatus != nil {
		t.Fatalf("missing latest run must be nullable: %#v", one.Task)
	}
}

func TestTaskNextSkipsIneligibleIndexRowsAndAcceptsReady(t *testing.T) {
	s, hubRevision, _ := testService(t)
	ctx := context.Background()
	for _, slug := range []string{"ineligible", "eligible"} {
		_, operation, err := s.TaskCreate(ctx, TaskCreateInput{
			ProjectID: "example", Slug: slug, Title: slug, Objective: "Next", AcceptanceCriteria: []string{"next"}, OperationClass: "implementation", CreatedBy: "test",
			WriteOptions: WriteOptions{ExpectedHubRevision: hubRevision},
		})
		if err != nil {
			t.Fatal(err)
		}
		hubRevision = operation.Hub.After
	}
	items, err := s.TaskList(ctx, "example")
	if err != nil {
		t.Fatal(err)
	}
	var ineligibleID string
	var eligibleState model.TaskState
	var eligiblePath string
	for _, item := range items {
		if item.Task.Title == "ineligible" {
			ineligibleID = item.Task.ID
		} else if item.Task.Title == "eligible" {
			eligibleState = item.State
			eligiblePath = s.taskStatePath(item.Task.ProjectID, item.Task.ID)
		}
	}
	if ineligibleID == "" {
		t.Fatal("missing ineligible task")
	}
	cancelResult, err := s.TaskCancel(ctx, ineligibleID, hubRevision)
	if err != nil {
		t.Fatal(err)
	}
	hubRevision = cancelResult.Hub.After
	eligibleState.Status = "ready"
	tx, err := s.Hub.Transact(ctx, hubRevision, "test: make task ready", func(worktree string) ([]string, error) {
		return []string{eligiblePath}, hub.WriteJSON(worktree, eligiblePath, eligibleState)
	})
	if err != nil {
		t.Fatal(err)
	}
	hubRevision = tx.After
	result, err := s.TaskNext(ctx, "example")
	if err != nil || result.Task == nil || result.Task.ID == ineligibleID || result.Task.Title != "eligible" {
		t.Fatalf("task_next selected the wrong row: %#v %v", result, err)
	}
}

func TestGitLogPageReturnsCompactRows(t *testing.T) {
	s, _, head := testService(t)
	page, err := s.GitLogPage(context.Background(), GitLogInput{ProjectID: "example", Revision: head, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Commits) == 0 {
		t.Fatal("git log page was empty")
	}
	for _, row := range page.Commits {
		if len(row.ShortSHA) != 10 || strings.Trim(row.ShortSHA, "0123456789abcdef") != "" || row.Commit == "" || row.Date == "" {
			t.Fatalf("invalid compact row: %#v", row)
		}
	}
}

func TestBoundedCursorRejectsTampering(t *testing.T) {
	cursor, err := encodeBoundedIndexCursor(boundedIndexCursor{Version: 1, Kind: "task_list", ProjectID: "example", Root: strings.Repeat("a", 40), Limit: 10, Offset: 10})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeBoundedIndexCursor(cursor); err != nil {
		t.Fatal(err)
	}
	if _, err := decodeBoundedIndexCursor(cursor + "x"); err == nil {
		t.Fatal("tampered cursor was accepted")
	}
}
