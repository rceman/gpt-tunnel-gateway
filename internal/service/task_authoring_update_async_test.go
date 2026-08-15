package service

import (
	"context"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestTaskAuthoringUpdateAsyncIsBoundedIdempotentAndRestartReadable(t *testing.T) {
	s, hubRevision, _ := testServiceWithoutIdentifiers(t)
	hubRevision = adoptAuthoringIdentifiersForTest(t, s, hubRevision)
	hubRevision = enableTrainV2ForTest(t, s, hubRevision)
	task, operation, err := s.TaskAuthoringCreate(context.Background(), TaskAuthoringCreateInput{
		ProjectID:          "example",
		Title:              "Async update task",
		Objective:          "Persist an update intent before Hub work.",
		AcceptanceCriteria: []string{"one bounded receipt"},
		ADRRelation:        model.TaskADRNoRequired,
		CreatedBy:          "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: hubRevision,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	title := "Updated through a durable receipt"
	in := TaskAuthoringUpdateInput{
		ProjectID:              task.ProjectID,
		TaskID:                 task.ID,
		ExpectedRevision:       task.Revision,
		ExpectedRevisionSHA256: task.RevisionSHA256,
		Title:                  &title,
		UpdatedBy:              "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: operation.Hub.After,
		},
	}
	ownerContext := WithAgentSessionID(context.Background(), "SP-ABCDEFGH")
	started := time.Now()
	first, err := s.TaskAuthoringUpdateAsync(ownerContext, in)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("task/update receipt exceeded one second: %s", elapsed)
	} else {
		t.Logf("task/update receipt latency: %s", elapsed)
	}
	second, err := s.TaskAuthoringUpdateAsync(ownerContext, in)
	if err != nil {
		t.Fatal(err)
	}
	if first.OperationID == "" || first.OperationID != second.OperationID {
		t.Fatalf("task/update receipt was not idempotent: first=%#v second=%#v", first, second)
	}

	deadline := time.Now().Add(10 * time.Second)
	var completed TaskAuthoringUpdateReceipt
	for time.Now().Before(deadline) {
		completed, err = s.TaskAuthoringUpdateOperationStatus(ownerContext, first.OperationID)
		if err != nil {
			t.Fatal(err)
		}
		if completed.Status == "completed" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if completed.Status != "completed" || completed.Task == nil || completed.Task.Title != title {
		t.Fatalf("task/update worker did not complete: %#v", completed)
	}
	if completed.Task.Metadata["gateway_operation_id"] != first.OperationID {
		t.Fatalf("completed task lost durable operation marker: %#v", completed.Task.Metadata)
	}
	if _, err := s.TaskAuthoringUpdateOperationStatus(WithAgentSessionID(context.Background(), "SP-IJKLQRST"), first.OperationID); err == nil {
		t.Fatal("task/update status was readable from the wrong Agent session")
	}

	// A fresh Service instance can read the durable completed receipt without
	// reconstructing or re-running the Hub mutation.
	restarted := New(s.Config)
	afterRestart, err := restarted.TaskAuthoringUpdateOperationStatus(ownerContext, first.OperationID)
	if err != nil || afterRestart.Status != "completed" || afterRestart.OperationID != first.OperationID {
		t.Fatalf("restart could not read durable task/update receipt: %#v %v", afterRestart, err)
	}
}
