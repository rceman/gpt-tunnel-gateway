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
	hubRevision = enableCanonicalExecutionForTest(t, s, hubRevision)
	_ = testServiceWithDurability(t, s)
	task, _, err := s.taskAuthoringCreateShared(context.Background(), "EXM-OPR100", TaskAuthoringCreateInput{
		ProjectID:          "example",
		Title:              "Async ready task",
		Summary:            "Persist a readiness intent.",
		Objective:          "Persist a readiness intent before Hub work.",
		AcceptanceCriteria: []string{"one durable readiness seal"},
		Priority:           model.TaskPriorityP2,
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
			ExpectedHubRevision: hubRevision,
		},
	}
	first, err := s.TaskAuthoringReadyAsync(context.Background(), in)
	if err != nil {
		t.Fatal(err)
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
