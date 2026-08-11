package service

import (
	"context"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func createTrainTask(t *testing.T, s *Service, revision, slug string) (model.Task, string) {
	t.Helper()
	task, operation, err := s.TaskCreate(context.Background(), TaskCreateInput{
		ProjectID:          "example",
		Title:              "Train task " + slug,
		Objective:          "Exercise one explicit ordered train task.",
		Slug:               slug,
		AcceptanceCriteria: []string{"bounded"},
		OperationClass:     "implementation",
		CreatedBy:          "test",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: revision,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return task, operation.Hub.After
}

func TestTaskTrainCreateBindsOnlyExplicitFirstTaskAndStatusIsBounded(t *testing.T) {
	s, revision, _ := testService(t)
	first, revision := createTrainTask(t, s, revision, "first")
	second, revision := createTrainTask(t, s, revision, "second")
	train, operation, err := s.TaskTrainCreate(context.Background(), TaskTrainCreateInput{
		ProjectID: "example",
		TaskIDs:   []string{first.ID, second.ID},
		CreatedBy: "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: revision,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if operation.Hub.After == "" || train.CurrentTaskID != first.ID || train.CurrentIndex != 0 || train.CurrentRunID != "" {
		t.Fatalf("unexpected train creation: %#v %#v", train, operation)
	}
	status, err := s.TaskTrainStatus(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	if status.CurrentTaskID != first.ID || status.NextTaskID != second.ID || status.TaskCount != 2 || status.CurrentTaskState != "created" {
		t.Fatalf("unexpected bounded train status: %#v", status)
	}
	if _, _, err := s.TaskTrainCreate(context.Background(), TaskTrainCreateInput{
		ProjectID: "example",
		TaskIDs:   []string{first.ID, first.ID},
		CreatedBy: "planner",
	}); err == nil {
		t.Fatal("duplicate train creation unexpectedly succeeded")
	}
}

func TestTaskTrainPollWaitsForDeliveryAfterTaskCompletion(t *testing.T) {
	s, revision, _ := testService(t)
	first, revision := createTrainTask(t, s, revision, "delivery-wait")
	second, revision := createTrainTask(t, s, revision, "delivery-next")
	train, operation, err := s.TaskTrainCreate(context.Background(), TaskTrainCreateInput{
		ProjectID: "example",
		TaskIDs:   []string{first.ID, second.ID},
		CreatedBy: "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: revision,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	completed := model.TaskState{SchemaVersion: model.SchemaVersion, TaskID: first.ID, TaskSHA256: first.SHA256, Status: "completed", UpdatedAt: time.Now().UTC()}
	updated, err := s.Hub.Transact(context.Background(), operation.Hub.After, "test: complete train task", func(worktree string) ([]string, error) {
		path := s.taskStatePath("example", first.ID)
		if err := hub.WriteJSON(worktree, path, completed); err != nil {
			return nil, err
		}
		return []string{path}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	status, err := s.TaskTrainPoll(context.Background(), TaskTrainPollInput{ProjectID: "example"})
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != model.TaskTrainWaitingDelivery || status.WaitReason != "delivery_review_or_merge_required" || status.NextTaskID != second.ID {
		t.Fatalf("unexpected delivery wait status: %#v", status)
	}
	got, err := s.TaskTrainRead(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != model.TaskTrainWaitingDelivery || got.CurrentIndex != train.CurrentIndex {
		t.Fatalf("unexpected persisted delivery wait: %#v", got)
	}
	if remote, err := s.Hub.RemoteRevision(context.Background()); err != nil || remote != updated.After {
		t.Fatalf("unexpected hub revision: got=%s want=%s err=%v", remote, updated.After, err)
	}
}
