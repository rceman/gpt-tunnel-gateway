package service

import (
	"context"
	"testing"
	"time"
)

func TestTaskSupersedeAsyncIsBoundedAndIdempotent(t *testing.T) {
	s, revision, _ := testService(t)
	_ = testServiceWithDurability(t, s)
	ctx := context.Background()
	original, operation, err := s.TaskCreate(ctx, TaskCreateInput{
		ProjectID:          "example",
		Slug:               "original-task",
		Title:              "Original task",
		Objective:          "Create a task to supersede.",
		AcceptanceCriteria: []string{"original"},
		OperationClass:     "implementation",
		CreatedBy:          "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: revision,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	in := TaskSupersedeInput{
		OldTaskID: original.ID,
		Task: TaskCreateInput{
			ProjectID:          "example",
			Slug:               "replacement-task",
			Title:              "Replacement task",
			Objective:          "Replace the original task.",
			AcceptanceCriteria: []string{"replacement"},
			OperationClass:     "implementation",
			CreatedBy:          "planner",
			WriteOptions: WriteOptions{
				ExpectedHubRevision: operation.Hub.After,
			},
		},
	}
	first, err := s.TaskSupersedeAsync(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.TaskSupersedeAsync(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if first.OperationID == "" || first.OperationID != second.OperationID {
		t.Fatalf("task/supersede initiation was not idempotent: first=%#v second=%#v", first, second)
	}

	deadline := time.Now().Add(10 * time.Second)
	var completed TaskSupersedeReceipt
	for time.Now().Before(deadline) {
		completed, err = s.TaskSupersedeOperationStatus(ctx, first.OperationID)
		if err != nil {
			t.Fatal(err)
		}
		if completed.Status == "completed" || completed.Status == "failed" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if completed.Status != "completed" || completed.Task == nil || completed.Task.Supersedes != original.ID {
		t.Fatalf("task/supersede worker did not complete: %#v", completed)
	}
}
