package service

import (
	"context"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestTaskAuthoringReadyAsyncIsBoundedAndIdempotent(t *testing.T) {
	s, hubRevision, _ := testServiceWithoutIdentifiers(t)
	hubRevision = adoptAuthoringIdentifiersForTest(t, s, hubRevision)
	hubRevision = enableTrainV2ForTest(t, s, hubRevision)
	task, operation, err := s.TaskAuthoringCreate(context.Background(), TaskAuthoringCreateInput{
		ProjectID:          "example",
		Title:              "Async ready task",
		Objective:          "Persist a readiness intent before Hub work.",
		AcceptanceCriteria: []string{"one durable readiness seal"},
		ADRRelation:        model.TaskADRNoRequired,
		CreatedBy:          "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: hubRevision,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	in := TaskAuthoringReadyInput{
		ProjectID:              task.ProjectID,
		TaskID:                 task.ID,
		ExpectedRevision:       task.Revision,
		ExpectedRevisionSHA256: task.RevisionSHA256,
		ReadyBy:                "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: operation.Hub.After,
		},
	}
	started := time.Now()
	first, err := s.TaskAuthoringReadyAsync(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("task/ready receipt exceeded one second: %s", elapsed)
	} else {
		t.Logf("task/ready receipt latency: %s", elapsed)
	}
	second, err := s.TaskAuthoringReadyAsync(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if first.OperationID == "" || first.OperationID != second.OperationID {
		t.Fatalf("task/ready receipt was not idempotent: first=%#v second=%#v", first, second)
	}

	deadline := time.Now().Add(10 * time.Second)
	var completed TaskAuthoringReadyReceipt
	for time.Now().Before(deadline) {
		completed, err = s.TaskAuthoringReadyOperationStatus(context.Background(), first.OperationID)
		if err != nil {
			t.Fatal(err)
		}
		if completed.Status == "completed" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if completed.Status != "completed" || completed.Task == nil || completed.Task.Status != model.TaskAuthoringReady || completed.Task.ReadySeal == nil {
		t.Fatalf("task/ready worker did not complete: %#v", completed)
	}
}
