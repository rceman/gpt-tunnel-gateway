package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/pagination"
)

func TestTaskListQuerySearchStatusLimitAndCursor(t *testing.T) {
	s, revision, _ := testService(t)
	ctx := context.Background()
	clock := time.Date(2026, 8, 11, 15, 0, 0, 0, time.UTC)
	s.clock = func() time.Time {
		current := clock
		clock = clock.Add(time.Second)
		return current
	}
	type createdTask struct {
		task     model.Task
		revision string
	}
	created := make([]createdTask, 0, 3)
	for _, spec := range []struct {
		slug, title, objective string
		typ                    model.TaskType
	}{
		{"alpha-search", "Alpha planner task", "Find the durable alpha objective.", model.TaskTypeTask},
		{"beta-search", "Beta release task", "Find the durable beta objective.", model.TaskTypeBug},
		{"gamma-search", "Gamma review task", "Find the durable gamma objective.", model.TaskTypeChore},
	} {
		task, operation, err := s.TaskCreate(ctx, TaskCreateInput{
			ProjectID:          "example",
			Slug:               spec.slug,
			Type:               spec.typ,
			Title:              spec.title,
			Objective:          spec.objective,
			AcceptanceCriteria: []string{"bounded"},
			OperationClass:     "implementation",
			CreatedBy:          "planner",
			WriteOptions: WriteOptions{
				ExpectedHubRevision: revision,
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		revision = operation.Hub.After
		created = append(created, createdTask{
			task:     task,
			revision: revision,
		})
	}

	all, err := s.TaskListQuery(ctx, TaskListInput{ProjectID: "example"})
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Tasks) != 3 || all.HasMore || all.NextCursor != "" {
		t.Fatalf("default result=%#v", all)
	}
	if all.Tasks[0].Task.ID != created[2].task.ID || all.Tasks[2].Task.ID != created[0].task.ID {
		t.Fatalf("newest-first order=%v", []string{all.Tasks[0].Task.ID, all.Tasks[1].Task.ID, all.Tasks[2].Task.ID})
	}

	for _, query := range []string{"BETA-SEARCH", "release task", "durable beta objective", "bug", created[1].task.ID} {
		result, queryErr := s.TaskListQuery(ctx, TaskListInput{
			ProjectID: "example",
			Query:     query,
		})
		if queryErr != nil || len(result.Tasks) != 1 || result.Tasks[0].Task.ID != created[1].task.ID {
			t.Fatalf("query=%q result=%#v err=%v", query, result, queryErr)
		}
	}
	typed, err := s.TaskListQuery(ctx, TaskListInput{ProjectID: "example", Type: model.TaskTypeBug})
	if err != nil || len(typed.Tasks) != 1 || typed.Tasks[0].Task.ID != created[1].task.ID {
		t.Fatalf("type filter result=%#v err=%v", typed, err)
	}
	if _, err := s.TaskListQuery(ctx, TaskListInput{ProjectID: "example", Execution: model.TaskExecutionTrain}); err == nil || !strings.Contains(err.Error(), "execution filter is unavailable") {
		t.Fatalf("legacy execution filter was silently ignored: %v", err)
	}
	empty, err := s.TaskListQuery(ctx, TaskListInput{
		ProjectID: "example",
		Query:     "does-not-exist",
	})
	if err != nil || len(empty.Tasks) != 0 || empty.HasMore || empty.NextCursor != "" {
		t.Fatalf("empty result=%#v err=%v", empty, err)
	}

	state := model.TaskState{SchemaVersion: model.SchemaVersion, TaskID: created[1].task.ID, TaskSHA256: created[1].task.SHA256, Status: "ready", UpdatedAt: time.Now().UTC()}
	updated, err := s.Hub.Transact(ctx, revision, "test: mark task ready", func(worktree string) ([]string, error) {
		path := s.taskStatePath("example", created[1].task.ID)
		if err := hub.WriteJSON(worktree, path, state); err != nil {
			return nil, err
		}
		return []string{path}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	ready, err := s.TaskListQuery(ctx, TaskListInput{
		ProjectID: "example",
		Status:    "ready",
	})
	if err != nil || len(ready.Tasks) != 1 || ready.Tasks[0].Task.ID != created[1].task.ID {
		t.Fatalf("status result=%#v err=%v", ready, err)
	}
	if _, err := s.TaskListQuery(ctx, TaskListInput{
		ProjectID: "example",
		Status:    "not-a-status",
	}); err == nil {
		t.Fatal("invalid status accepted")
	}

	pageOne, err := s.TaskListQuery(ctx, TaskListInput{
		ProjectID: "example",
		Limit:     2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(pageOne.Tasks) != 2 || !pageOne.HasMore || pageOne.NextCursor == "" {
		t.Fatalf("page one=%#v", pageOne)
	}
	if len(pageOne.NextCursor) > pagination.CompactCursorLength || strings.ContainsAny(pageOne.NextCursor, "Il1O0+/=") {
		t.Fatalf("task cursor is not compact and agent-safe: %q", pageOne.NextCursor)
	}
	pageTwo, err := s.TaskListQuery(ctx, TaskListInput{
		ProjectID: "example",
		Limit:     2,
		Cursor:    pageOne.NextCursor,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(pageTwo.Tasks) != 1 || pageTwo.HasMore || pageTwo.NextCursor != "" || pageTwo.Tasks[0].Task.ID != created[0].task.ID {
		t.Fatalf("page two=%#v", pageTwo)
	}
	if pageOne.Tasks[0].Task.ID == pageTwo.Tasks[0].Task.ID || strings.TrimSpace(pageOne.NextCursor) == "" {
		t.Fatalf("cursor did not advance: page1=%#v page2=%#v", pageOne, pageTwo)
	}
	if _, err := s.TaskListQuery(ctx, TaskListInput{
		ProjectID: "example",
		Limit:     MaxTaskListLimit + 1,
	}); err == nil {
		t.Fatal("hard maximum was not enforced")
	}
	if limit, err := s.taskListLimit(0); err != nil || limit != 10 {
		t.Fatalf("default public page limit=%d err=%v", limit, err)
	}
	if _, err := s.TaskListQuery(ctx, TaskListInput{
		ProjectID: "example",
		Cursor:    "not-a-cursor",
	}); err == nil {
		t.Fatal("invalid cursor accepted")
	}
	if updated.After == "" {
		t.Fatal("state update did not produce a Hub revision")
	}
}
