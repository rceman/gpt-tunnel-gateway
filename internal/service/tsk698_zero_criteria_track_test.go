package service

import (
	"context"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

// TSK698: for a zero-acceptance-criteria integrated Task the durable
// accepted-Track proof is required even when the current immutable
// verification proof succeeds — verification alone does not prove delivery
// for a Task with no criteria. Tasks WITH criteria keep the
// current-verification-preferred path untouched.

// tsk698VerifiedZeroCriteriaFixture drives a zero-criteria Task through the
// real dispatch/verify/integrate pipeline so its immutable verification
// receipt is current.
func tsk698VerifiedZeroCriteriaFixture(t *testing.T, s *Service, key string) model.TaskAuthoring {
	t.Helper()
	task := tsk585CompleteTask(t, s, key, "Zero criteria Task")
	tsk585Dispatch(t, s, task.ID)
	tsk585LaneCommit(t, s, task.ID, "candidate")
	tsk585DriveToVerified(t, s, task.ID)
	if operation := tsk585Integrate(t, s, task.ID); operation.Status != "completed" {
		t.Fatalf("integrate status=%q error=%q", operation.Status, operation.Error)
	}
	return task
}

// TestTSK698ZeroCriteriaRequiresTrackUnderCurrentVerification proves the
// correction: a zero-criteria Task whose verification is current completes
// only when a durable accepted Track also pins its delivery.
func TestTSK698ZeroCriteriaRequiresTrackUnderCurrentVerification(t *testing.T) {
	s, db := tsk585Setup(t)
	defer db.Close()
	ctx := context.Background()
	task := tsk698VerifiedZeroCriteriaFixture(t, s, "tsk698-zero-verified")
	review := tsk697ReviewJournal(t, s, task.ID)

	// Current verification alone cannot complete a zero-criteria Task.
	if _, err := tsk697Complete(s, task, review.ID); err == nil {
		t.Fatal("zero-criteria Task completed without accepted-Track proof")
	}

	track := tsk697AcceptedTrack(t, s, "EXM-TRK801", []string{task.ID})
	out, err := tsk697Complete(s, task, review.ID)
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != model.TaskAuthoringDone {
		t.Fatalf("status=%q", out.Status)
	}
	event, found, err := db.ReadTaskCompletionEvent(ctx, "example", task.ID)
	if err != nil || !found {
		t.Fatalf("completion event found=%v err=%v", found, err)
	}
	var contract taskCompletionContract
	if err := decodeStrict(event.Contract, &contract); err != nil {
		t.Fatal(err)
	}
	// The contract carries both the current verification identity and the
	// accepted-Track proof.
	if contract.AcceptedTrack != track.ID || contract.VerificationOperationID == "" || contract.VerificationAttemptRevision < 1 {
		t.Fatalf("contract=%#v", contract)
	}
	// Idempotent replay resolves the recorded contract.
	if _, err := tsk697Complete(s, task, review.ID); err != nil {
		t.Fatalf("replay: %v", err)
	}
}

// TestTSK698CriteriaTaskKeepsVerificationPath proves a Task WITH criteria
// still completes on current verification alone — no Track required.
func TestTSK698CriteriaTaskKeepsVerificationPath(t *testing.T) {
	s, db := tsk585Setup(t)
	defer db.Close()
	ctx := context.Background()
	task := tsk585CompleteTask(t, s, "tsk698-with-criteria", "Criteria Task", "criterion one")
	tsk585Dispatch(t, s, task.ID)
	tsk585LaneCommit(t, s, task.ID, "candidate")
	tsk585DriveToVerified(t, s, task.ID)
	if operation := tsk585Integrate(t, s, task.ID); operation.Status != "completed" {
		t.Fatalf("integrate status=%q error=%q", operation.Status, operation.Error)
	}
	review := tsk697ReviewJournal(t, s, task.ID)
	out, err := tsk697Complete(s, task, review.ID)
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != model.TaskAuthoringDone {
		t.Fatalf("status=%q", out.Status)
	}
	event, found, err := db.ReadTaskCompletionEvent(ctx, "example", task.ID)
	if err != nil || !found {
		t.Fatalf("completion event found=%v err=%v", found, err)
	}
	var contract taskCompletionContract
	if err := decodeStrict(event.Contract, &contract); err != nil {
		t.Fatal(err)
	}
	if contract.AcceptedTrack != "" || contract.VerificationOperationID == "" {
		t.Fatalf("contract=%#v", contract)
	}
}
