package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func tsk657WorkerContext(t *testing.T, s *Service) context.Context {
	t.Helper()
	workerSession := tsk622AssertWorkerSessionCount(t, s.Durability)
	return WithAgentSessionID(context.Background(), workerSession)
}

func tsk657WaitSubmitOperation(t *testing.T, s *Service, operationID string, statuses ...string) durableMutationOperation {
	t.Helper()
	want := make(map[string]bool, len(statuses))
	for _, status := range statuses {
		want[status] = true
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		operation, err := s.readDurableMutation(operationID)
		if err == nil && want[operation.Status] {
			return operation
		}
		time.Sleep(10 * time.Millisecond)
	}
	operation, err := s.readDurableMutation(operationID)
	if err != nil {
		t.Fatalf("read submit operation: %v", err)
	}
	t.Fatalf("submit operation did not reach %v: %#v", statuses, operation)
	return operation
}

func TestTSK657SubmitAdmissionReconcilesStillExecutingAndCompletedRepeat(t *testing.T) {
	s, db := tsk585Setup(t)
	defer db.Close()
	task := tsk585Task(t, s, "tsk657-running-repeat", "Bounded submit repeat")
	tsk585Dispatch(t, s, task.ID)
	tsk585LaneCommit(t, s, task.ID, "bounded submit candidate")
	workerCtx := tsk657WorkerContext(t, s)

	started := make(chan struct{})
	release := make(chan struct{})
	var startedOnce bool
	s.durableMutationExecutor = func(ctx context.Context, operation durableMutationOperation) (json.RawMessage, error) {
		if operation.Kind == taskExecutionSubmitKind && !startedOnce {
			startedOnce = true
			close(started)
			<-release
		}
		return s.executeDurableMutation(ctx, operation)
	}

	first, err := s.TaskExecutionSubmitAsync(workerCtx, "example", "code")
	if err != nil || first.OperationID == "" {
		t.Fatalf("initial submit receipt=%#v err=%v", first, err)
	}
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("submit operation did not start")
	}
	second, err := s.TaskExecutionSubmitAsync(workerCtx, "example", "code")
	if err != nil || second.OperationID != first.OperationID || (second.Status != "accepted" && second.Status != "running") {
		t.Fatalf("repeat did not reconcile the running operation: first=%#v second=%#v err=%v", first, second, err)
	}
	close(release)
	operation := tsk657WaitSubmitOperation(t, s, first.OperationID, "completed")
	if operation.Status != "completed" {
		t.Fatalf("submit operation=%#v", operation)
	}
	third, err := s.TaskExecutionSubmitAsync(workerCtx, "example", "code")
	if err != nil || third.OperationID != first.OperationID || third.Status != "completed" || third.Result == nil || third.Result.Status != model.TaskExecutionAwaitingReview || third.Result.Key != task.ID {
		t.Fatalf("completed repeat did not return durable result: %#v err=%v", third, err)
	}
	state, found, err := db.ReadTaskExecutionState(context.Background(), "example", task.ID)
	if err != nil || !found || state.Status != model.TaskExecutionAwaitingReview || state.ExecutionRevision != 2 {
		t.Fatalf("submitted state=%#v found=%v err=%v", state, found, err)
	}
	phases, err := db.ReadTaskExecutionPhases(context.Background(), "example", task.ID, "code")
	if err != nil || len(phases) != 1 || phases[0].EventKind != "submission" {
		t.Fatalf("submit phases=%#v err=%v", phases, err)
	}
}

func TestTSK657SubmitAdmissionReconcilesLandedOutcomeUnknown(t *testing.T) {
	s, db := tsk585Setup(t)
	defer db.Close()
	task := tsk585Task(t, s, "tsk657-landed-repeat", "Landed submit repeat")
	tsk585Dispatch(t, s, task.ID)
	tsk585LaneCommit(t, s, task.ID, "landed submit candidate")
	workerCtx := tsk657WorkerContext(t, s)

	s.durableMutationExecutor = func(ctx context.Context, operation durableMutationOperation) (json.RawMessage, error) {
		if operation.Kind != taskExecutionSubmitKind {
			return s.executeDurableMutation(ctx, operation)
		}
		if _, err := s.executeDurableMutation(ctx, operation); err != nil {
			return nil, err
		}
		return nil, context.Canceled
	}
	first, err := s.TaskExecutionSubmitAsync(workerCtx, "example", "code")
	if err != nil || first.OperationID == "" {
		t.Fatalf("initial submit receipt=%#v err=%v", first, err)
	}
	unknown := tsk657WaitSubmitOperation(t, s, first.OperationID, "outcome_unknown")
	if unknown.Status != "outcome_unknown" {
		t.Fatalf("submit operation=%#v", unknown)
	}
	reconciled, err := s.TaskExecutionSubmitAsync(workerCtx, "example", "code")
	if err != nil || reconciled.OperationID != first.OperationID || reconciled.Status != "completed" || reconciled.Result == nil || reconciled.Result.Status != model.TaskExecutionAwaitingReview {
		t.Fatalf("landed outcome did not reconcile: %#v err=%v", reconciled, err)
	}
	state, found, err := db.ReadTaskExecutionState(context.Background(), "example", task.ID)
	if err != nil || !found || state.Status != model.TaskExecutionAwaitingReview || state.ExecutionRevision != 2 {
		t.Fatalf("reconciled state=%#v found=%v err=%v", state, found, err)
	}
}

func TestTSK657SubmitAdmissionRetriesOnlyAfterNotLandedProof(t *testing.T) {
	s, db := tsk585Setup(t)
	defer db.Close()
	task := tsk585Task(t, s, "tsk657-not-landed-repeat", "Not-landed submit repeat")
	tsk585Dispatch(t, s, task.ID)
	tsk585LaneCommit(t, s, task.ID, "not-landed submit candidate")
	workerCtx := tsk657WorkerContext(t, s)

	fail := true
	s.durableMutationExecutor = func(ctx context.Context, operation durableMutationOperation) (json.RawMessage, error) {
		if operation.Kind == taskExecutionSubmitKind && fail {
			return nil, context.Canceled
		}
		return s.executeDurableMutation(ctx, operation)
	}
	first, err := s.TaskExecutionSubmitAsync(workerCtx, "example", "code")
	if err != nil || first.OperationID == "" {
		t.Fatalf("initial submit receipt=%#v err=%v", first, err)
	}
	failed := tsk657WaitSubmitOperation(t, s, first.OperationID, "outcome_unknown")
	if failed.Status != "outcome_unknown" {
		t.Fatalf("submit operation=%#v", failed)
	}
	state, found, err := db.ReadTaskExecutionState(context.Background(), "example", task.ID)
	if err != nil || !found || state.Status != model.TaskExecutionDispatched || state.ExecutionRevision != 1 {
		t.Fatalf("not-landed proof state=%#v found=%v err=%v", state, found, err)
	}
	fail = false
	second, err := s.TaskExecutionSubmitAsync(workerCtx, "example", "code")
	if err != nil || second.OperationID != first.OperationID {
		t.Fatalf("explicit repeat did not reuse the failed operation: first=%#v second=%#v err=%v", first, second, err)
	}
	completed := tsk657WaitSubmitOperation(t, s, first.OperationID, "completed")
	if completed.Status != "completed" {
		t.Fatalf("retried submit operation=%#v", completed)
	}
	state, found, err = db.ReadTaskExecutionState(context.Background(), "example", task.ID)
	if err != nil || !found || state.Status != model.TaskExecutionAwaitingReview || state.ExecutionRevision != 2 {
		t.Fatalf("retried submit state=%#v found=%v err=%v", state, found, err)
	}
}
