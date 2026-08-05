package service

import (
	"context"
	"testing"
)

func TestTaskListSeparatesImmutableTaskAndState(t *testing.T) {
	s, hubRev, _ := testService(t)
	task, _, err := s.TaskCreate(context.Background(), TaskCreateInput{
		ProjectID: "example", Title: "List task", Objective: "Verify task listing.",
		Slug: "list", CreatedBy: "gpt",
		WriteOptions: WriteOptions{ExpectedHubRevision: hubRev},
	})
	if err != nil {
		t.Fatal(err)
	}
	items, err := s.TaskList(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Task.ID != task.ID || items[0].Task.Status != "created" || items[0].State.Status != "created" {
		t.Fatalf("%#v", items)
	}
}
