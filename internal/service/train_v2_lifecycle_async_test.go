package service

import (
	"context"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

func TestTrainV2AdvanceReceiptIdentityTracksLocalExecutionGeneration(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	in := TrainV2AdvanceInput{ProjectID: "example", TrainID: "GTW-TRN999"}
	runtime := trainv2.RuntimeBinding{
		SchemaVersion: 1,
		ProjectID:     in.ProjectID,
		TrainID:       in.TrainID,
		WorktreePath:  trainv2.ExpectedWorktreePath(s.Config.StateDir, in.ProjectID, in.TrainID),
		AgentID:       "agent",
		SessionKey:    "session-a",
		ItemPosition:  16,
		TaskID:        "GTW-TSK274",
		AttemptNumber: 1,
		StartedAt:     time.Unix(1, 0).UTC(),
	}
	if err := fsutil.WriteJSONAtomic(trainv2.RuntimePath(s.Config.StateDir, in.ProjectID, in.TrainID), runtime, 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := s.TrainV2AdvanceAsync(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	waitDurableMutationTerminal(t, s, first.OperationID)
	same, err := s.TrainV2AdvanceAsync(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if same.OperationID != first.OperationID {
		t.Fatalf("same execution generation was not idempotent: %q != %q", same.OperationID, first.OperationID)
	}
	waitDurableMutationTerminal(t, s, same.OperationID)
	runtime.ItemPosition = 17
	runtime.TaskID = "GTW-TSK276"
	runtime.StartedAt = time.Unix(2, 0).UTC()
	if err := fsutil.WriteJSONAtomic(trainv2.RuntimePath(s.Config.StateDir, in.ProjectID, in.TrainID), runtime, 0o600); err != nil {
		t.Fatal(err)
	}
	changed, err := s.TrainV2AdvanceAsync(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if changed.OperationID == first.OperationID {
		t.Fatal("changed execution generation reused stale advance receipt")
	}
	waitDurableMutationTerminal(t, s, changed.OperationID)
}

func TestTrainV2LifecycleInitiationsAreBoundedAndIdempotent(t *testing.T) {
	s, revision, _ := testServiceWithoutIdentifiers(t)
	_ = enableTrainV2ForTest(t, s, revision)
	ctx := context.Background()

	startInput := TrainV2StartInput{
		ProjectID: "example",
		TrainID:   "GTW-TRN999",
		StartedBy: "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: revision,
		},
	}
	startedAt := time.Now()
	startReceipt, err := s.TrainV2StartAsync(ctx, startInput)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(startedAt); elapsed >= time.Second {
		t.Fatalf("train/start initiation exceeded one second: %s", elapsed)
	} else {
		t.Logf("train/start initiation latency: %s", elapsed)
	}
	startAgain, err := s.TrainV2StartAsync(ctx, startInput)
	if err != nil || startAgain.OperationID != startReceipt.OperationID {
		t.Fatalf("train/start initiation was not idempotent: %#v %#v %v", startReceipt, startAgain, err)
	}
	if _, err := s.TrainV2StartOperationStatus(ctx, startReceipt.OperationID); err != nil {
		t.Fatal(err)
	}
	waitDurableMutationTerminal(t, s, startReceipt.OperationID)

	advanceInput := TrainV2AdvanceInput{
		ProjectID: "example",
		TrainID:   "GTW-TRN999",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: revision,
		},
	}
	advancedAt := time.Now()
	advanceReceipt, err := s.TrainV2AdvanceAsync(ctx, advanceInput)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(advancedAt); elapsed >= time.Second {
		t.Fatalf("train/advance initiation exceeded one second: %s", elapsed)
	} else {
		t.Logf("train/advance initiation latency: %s", elapsed)
	}
	advanceAgain, err := s.TrainV2AdvanceAsync(ctx, advanceInput)
	if err != nil || advanceAgain.OperationID != advanceReceipt.OperationID {
		t.Fatalf("train/advance initiation was not idempotent: %#v %#v %v", advanceReceipt, advanceAgain, err)
	}
	if _, err := s.TrainV2AdvanceOperationStatus(ctx, advanceReceipt.OperationID); err != nil {
		t.Fatal(err)
	}
	waitDurableMutationTerminal(t, s, advanceReceipt.OperationID)
}

func waitDurableMutationTerminal(t *testing.T, s *Service, operationID string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		operation, err := s.readDurableMutation(operationID)
		if err != nil {
			t.Fatal(err)
		}
		if operation.Status == "completed" || operation.Status == "failed" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("durable mutation %s did not reach a terminal status", operationID)
}
