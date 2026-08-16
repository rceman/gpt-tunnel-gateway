package service

import (
	"context"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestTrainV2AttemptCompletionIsServerMaterializedWithoutAgentFile(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	task := model.TaskAuthoring{SchemaVersion: 1, ID: "GTW-TSK285", RevisionSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	item := model.TrainV2Item{Position: 0, TaskID: task.ID}
	attempt := model.TrainV2Attempt{Number: 1}
	completion, err := s.readTrainV2AttemptCompletion(context.Background(), TrainV2AttemptFinalizeInput{
		ProjectID:     "example",
		TrainID:       "GTW-TRN999",
		ItemPosition:  0,
		AttemptNumber: 1,
		Summary:       "server materialized completion",
	}, task, item, attempt, []model.CompletionGateResult{
		{ID: model.WorkflowGateFormat, ExitCode: 0},
		{ID: model.WorkflowGateCheck, ExitCode: 0},
		{ID: model.WorkflowGateTest, ExitCode: 0},
	})
	if err != nil {
		t.Fatal(err)
	}
	if completion.TaskID != task.ID || completion.TaskSHA256 != task.RevisionSHA256 || completion.Status != "succeeded" || completion.Summary != "server materialized completion" || len(completion.GateResults) != 3 {
		t.Fatalf("unexpected server completion: %#v", completion)
	}
}

func TestTrainV2AttemptMutationsReturnBoundedReceipts(t *testing.T) {
	s, revision, _ := testServiceWithoutIdentifiers(t)
	ctx := context.Background()
	finalizeInput := TrainV2AttemptFinalizeInput{
		ProjectID:     "example",
		TrainID:       "GTW-TRN999",
		ItemPosition:  0,
		AttemptNumber: 1,
		WriteOptions: WriteOptions{
			ExpectedHubRevision: revision,
		},
	}
	started := time.Now()
	finalizeReceipt, err := s.TrainV2AttemptFinalizeAsync(ctx, finalizeInput)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("train/attempt-finalize initiation exceeded one second: %s", elapsed)
	} else {
		t.Logf("train/attempt-finalize initiation latency: %s", elapsed)
	}
	finalizeAgain, err := s.TrainV2AttemptFinalizeAsync(ctx, finalizeInput)
	if err != nil || finalizeAgain.OperationID != finalizeReceipt.OperationID {
		t.Fatalf("train/attempt-finalize initiation was not idempotent: %#v %#v %v", finalizeReceipt, finalizeAgain, err)
	}
	waitDurableMutationTerminal(t, s, finalizeReceipt.OperationID)
	if _, err := s.TrainV2AttemptOperationStatus(ctx, finalizeReceipt.OperationID); err != nil {
		t.Fatal(err)
	}

	reviewInput := TrainV2AttemptReviewInput{
		ProjectID:     "example",
		TrainID:       "GTW-TRN999",
		ItemPosition:  0,
		AttemptNumber: 1,
		Outcome:       "accepted",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: revision,
		},
	}
	started = time.Now()
	reviewReceipt, err := s.TrainV2AttemptReviewAsync(ctx, reviewInput)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("train/attempt-review initiation exceeded one second: %s", elapsed)
	} else {
		t.Logf("train/attempt-review initiation latency: %s", elapsed)
	}
	reviewAgain, err := s.TrainV2AttemptReviewAsync(ctx, reviewInput)
	if err != nil || reviewAgain.OperationID != reviewReceipt.OperationID {
		t.Fatalf("train/attempt-review initiation was not idempotent: %#v %#v %v", reviewReceipt, reviewAgain, err)
	}
	waitDurableMutationTerminal(t, s, reviewReceipt.OperationID)
	if _, err := s.TrainV2AttemptOperationStatus(ctx, reviewReceipt.OperationID); err != nil {
		t.Fatal(err)
	}
}
