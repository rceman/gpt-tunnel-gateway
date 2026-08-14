package service

import (
	"context"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestTaskAuthoringCreateAsyncIsDurableAndIdempotent(t *testing.T) {
	s, revision, _ := testServiceWithoutIdentifiers(t)
	revision = adoptAuthoringIdentifiersForTest(t, s, revision)
	revision = enableTrainV2ForTest(t, s, revision)
	in := TaskAuthoringCreateInput{
		ProjectID: "example", Title: "Async task receipt", Objective: "Persist intent before remote Hub work.",
		AcceptanceCriteria: []string{"one durable task"}, ADRRelation: model.TaskADRNoRequired, CreatedBy: "planner",
		WriteOptions: WriteOptions{ExpectedHubRevision: revision},
	}
	started := time.Now()
	first, err := s.TaskAuthoringCreateAsync(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("durable receipt exceeded one second: %s", elapsed)
	}
	second, err := s.TaskAuthoringCreateAsync(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if first.OperationID == "" || first.OperationID != second.OperationID || first.Status != "accepted" {
		t.Fatalf("non-idempotent task/create receipt: first=%#v second=%#v", first, second)
	}

	deadline := time.Now().Add(10 * time.Second)
	var completed TaskCreateReceipt
	for time.Now().Before(deadline) {
		completed, err = s.TaskCreateOperationStatus(context.Background(), first.OperationID)
		if err != nil {
			t.Fatal(err)
		}
		if completed.Status == "completed" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if completed.Status != "completed" || completed.Task == nil {
		t.Fatalf("task/create worker did not complete: %#v", completed)
	}
	all, err := s.TaskAuthoringList(context.Background(), TaskAuthoringListInput{ProjectID: "example", Limit: MaxTaskListLimit})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, task := range all.Tasks {
		if task.Metadata["gateway_operation_id"] == first.OperationID {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("task/create operation produced %d durable tasks, want 1", count)
	}
}
