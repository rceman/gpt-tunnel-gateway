package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

// tsk680BindVerifyProcedure commits a verification Procedure script into the
// fixture project — the candidate lane only contains files committed before
// dispatch — then binds it to pre_task_verify. The procedure definition stays
// constant for the whole test so the admitted gate profile, and therefore
// the attempt identity, does not change.
func tsk680BindVerifyProcedure(t *testing.T, s *Service, name, body string) {
	t.Helper()
	plannerCtx := procedurePlannerContext(t, s)
	root := s.Config.Projects["example"].Root
	if err := os.MkdirAll(filepath.Join(root, "scripts"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "scripts", name+".py"), []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, root, "add", "scripts/"+name+".py")
	testutil.Git(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-m", "test: install "+name)
	tsk585RefreshProjectMirror(t, s, s.Config.Projects["example"])
	input, ok := model.ProjectHookPayloadSchema(model.HookPreTaskVerify)
	if !ok {
		t.Fatal("canonical pre_task_verify input schema is unavailable")
	}
	output, err := model.TaskVerificationProcedureOutputSchema([]string{"fixture_check"})
	if err != nil {
		t.Fatal(err)
	}
	definition := model.ProjectProcedureDefinition{
		Script: "scripts/" + name + ".py", Summary: "Exercise Task verification retry semantics.",
		Guide: "Accepts the canonical lifecycle payload.", Input: input, Output: output,
	}
	if _, err := s.ConfigProcedureCreate(plannerCtx, ConfigProcedureCreateInput{
		ProjectID:  "example",
		Name:       name,
		Definition: definition,
		Reason:     "Test truthful verification retry.",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ConfigHookBind(plannerCtx, ConfigHookBindInput{
		ProjectID: "example",
		Hook:      model.HookPreTaskVerify,
		Procedure: name,
		Reason:    "Test truthful verification retry.",
	}); err != nil {
		t.Fatal(err)
	}
}

// tsk680FlakyScript fails while the marker file is absent and emits valid
// gate evidence once it exists, reproducing the TSK678 transient failure:
// the candidate, gate profile, and request stay identical between attempts.
func tsk680FlakyScript(marker string) string {
	return fmt.Sprintf(`#!/usr/bin/env python3
import json
import os
import sys

if not os.path.exists(%q):
    sys.exit(41)
with open(os.environ["GTW_PROCEDURE_OUTPUT_FILE"], "w", encoding="utf-8") as stream:
    json.dump({"gates": [{"id": "fixture_check", "exit_code": 0, "duration_ms": 1}]}, stream)
`, marker)
}

// TestTSK680FailedVerificationAdmitsFreshAttempt reproduces the TSK678
// incident: a terminal failed verification must not permanently poison the
// same exact candidate for the Session. The explicit retry allocates a
// distinct Operation chained to the preserved failure, and the successful
// retry leaves the earlier failed Operation readable.
func TestTSK680FailedVerificationAdmitsFreshAttempt(t *testing.T) {
	s, db := tsk585Setup(t)
	defer db.Close()
	ctx := context.Background()
	workerCtx := tsk585WorkerContext(t, s)
	marker := filepath.Join(s.Config.StateDir, "tsk680-pass")
	tsk680BindVerifyProcedure(t, s, "flaky_verify", tsk680FlakyScript(marker))
	task := tsk585Task(t, s, "tsk680-flake-retry", "Truthful verification retry")
	tsk585Dispatch(t, s, task.ID)
	tsk585DriveToVerification(t, s, task.ID)

	first, err := s.TaskExecutionTestAsync(workerCtx, TaskExecutionTestInput{
		ProjectID: "example",
		Key:       task.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	failed := tsk585WaitOperation(t, s, first.OperationID)
	if failed.Status != "failed" {
		t.Fatalf("first verification attempt=%#v", failed)
	}

	if err := os.WriteFile(marker, []byte("pass"), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := s.TaskExecutionTestAsync(workerCtx, TaskExecutionTestInput{
		ProjectID: "example",
		Key:       task.ID,
	})
	if err != nil {
		t.Fatalf("same-session retry was rejected: %v", err)
	}
	if second.OperationID == first.OperationID || second.Status != "accepted" {
		t.Fatalf("retry did not allocate a distinct fresh attempt: first=%#v second=%#v", first, second)
	}
	succeeded := tsk585WaitOperation(t, s, second.OperationID)
	if succeeded.Status != "completed" || succeeded.RetriedFrom != failed.OperationID {
		t.Fatalf("retry attempt=%#v", succeeded)
	}

	prior, err := s.readDurableMutation(failed.OperationID)
	if err != nil || prior.Status != "failed" || prior.Error != failed.Error || len(taskLifecycleHookAttempts(prior.Result)) != 1 {
		t.Fatalf("prior failed Operation was rewritten: %#v err=%v", prior, err)
	}
	receipt, found, err := db.ReadLatestTaskExecutionVerification(ctx, "example", task.ID)
	if err != nil || !found || receipt.OperationID != succeeded.OperationID || receipt.Outcome != model.TaskExecutionVerificationSucceeded {
		t.Fatalf("verification receipt=%#v found=%v err=%v", receipt, found, err)
	}
	state, found, err := db.ReadTaskExecutionState(ctx, "example", task.ID)
	if err != nil || !found || state.Status != model.TaskExecutionVerified {
		t.Fatalf("execution state=%#v found=%v err=%v", state, found, err)
	}

	// The now-verified exact candidate remains idempotently reusable.
	again, err := s.TaskExecutionTestAsync(workerCtx, TaskExecutionTestInput{
		ProjectID: "example",
		Key:       task.ID,
	})
	if err != nil || again.OperationID != succeeded.OperationID || again.Status != "completed" {
		t.Fatalf("successful verification was not reused idempotently: %#v err=%v", again, err)
	}
}

// TestTSK680InFlightVerificationReplayAttaches proves duplicate calls while
// an attempt is in flight still replay the same Operation rather than
// minting concurrent duplicate verifications.
func TestTSK680InFlightVerificationReplayAttaches(t *testing.T) {
	s, db := tsk585Setup(t)
	defer db.Close()
	workerCtx := tsk585WorkerContext(t, s)
	release := filepath.Join(s.Config.StateDir, "tsk680-release")
	tsk680BindVerifyProcedure(t, s, "blocking_verify", fmt.Sprintf(`#!/usr/bin/env python3
import json
import os
import sys
import time

deadline = time.time() + 20
while not os.path.exists(%q):
    if time.time() > deadline:
        sys.exit(44)
    time.sleep(0.05)
with open(os.environ["GTW_PROCEDURE_OUTPUT_FILE"], "w", encoding="utf-8") as stream:
    json.dump({"gates": [{"id": "fixture_check", "exit_code": 0, "duration_ms": 1}]}, stream)
`, release))
	task := tsk585Task(t, s, "tsk680-inflight", "In-flight verification dedupe")
	tsk585Dispatch(t, s, task.ID)
	tsk585DriveToVerification(t, s, task.ID)

	first, err := s.TaskExecutionTestAsync(workerCtx, TaskExecutionTestInput{
		ProjectID: "example",
		Key:       task.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if operation, err := s.readDurableMutation(first.OperationID); err == nil && operation.Status == "running" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	second, err := s.TaskExecutionTestAsync(workerCtx, TaskExecutionTestInput{
		ProjectID: "example",
		Key:       task.ID,
	})
	if err != nil || second.OperationID != first.OperationID {
		t.Fatalf("in-flight duplicate did not attach: %#v err=%v", second, err)
	}
	if err := os.WriteFile(release, []byte("go"), 0o600); err != nil {
		t.Fatal(err)
	}
	if operation := tsk585WaitOperation(t, s, first.OperationID); operation.Status != "completed" {
		t.Fatalf("in-flight attempt=%#v", operation)
	}
}

// TestTSK680VerificationAttemptsAreBounded proves retry allocation is
// bounded: after the attempt limit the terminal failed receipt stays and
// the call fails closed instead of minting unbounded Operations.
func TestTSK680VerificationAttemptsAreBounded(t *testing.T) {
	s, db := tsk585Setup(t)
	defer db.Close()
	workerCtx := tsk585WorkerContext(t, s)
	tsk680BindVerifyProcedure(t, s, "always_fail_verify", "#!/bin/sh\nexit 41\n")
	task := tsk585Task(t, s, "tsk680-bounded", "Bounded verification attempts")
	tsk585Dispatch(t, s, task.ID)
	tsk585DriveToVerification(t, s, task.ID)

	operations := map[string]bool{}
	for attempt := 1; attempt <= maxTaskVerificationAttempts; attempt++ {
		receipt, err := s.TaskExecutionTestAsync(workerCtx, TaskExecutionTestInput{
			ProjectID: "example",
			Key:       task.ID,
		})
		if err != nil {
			t.Fatalf("attempt %d was not admitted: %v", attempt, err)
		}
		operation := tsk585WaitOperation(t, s, receipt.OperationID)
		if operation.Status != "failed" {
			t.Fatalf("attempt %d=%#v", attempt, operation)
		}
		operations[operation.OperationID] = true
	}
	if len(operations) != maxTaskVerificationAttempts {
		t.Fatalf("attempts did not allocate distinct Operations: %#v", operations)
	}
	if _, err := s.TaskExecutionTestAsync(workerCtx, TaskExecutionTestInput{
		ProjectID: "example",
		Key:       task.ID,
	}); err == nil || !strings.Contains(err.Error(), "attempt bound") {
		t.Fatalf("retry past the bound err=%v", err)
	}
	sessionID := AgentSessionID(workerCtx)
	equivalents, err := db.ListLocalOperationsByAdmissionCoordinate(context.Background(), "example", "task-execution-test", sessionID, durableMutationInputSHA256([]byte(mustJSON(TaskExecutionTestInput{
		ProjectID: "example",
		Key:       task.ID,
	}))))
	if err != nil || len(equivalents) != maxTaskVerificationAttempts {
		t.Fatalf("durable attempt history=%d err=%v", len(equivalents), err)
	}
}

// TestTSK680VerificationDriftFailsClosed proves that changing the candidate
// between attempts is rejected at admission rather than reusing or retrying
// stale identity.
func TestTSK680VerificationDriftFailsClosed(t *testing.T) {
	s, db := tsk585Setup(t)
	defer db.Close()
	_ = db
	workerCtx := tsk585WorkerContext(t, s)
	marker := filepath.Join(s.Config.StateDir, "tsk680-drift-pass")
	tsk680BindVerifyProcedure(t, s, "drift_verify", tsk680FlakyScript(marker))
	task := tsk585Task(t, s, "tsk680-drift", "Verification drift fails closed")
	tsk585Dispatch(t, s, task.ID)
	tsk585DriveToVerification(t, s, task.ID)

	first, err := s.TaskExecutionTestAsync(workerCtx, TaskExecutionTestInput{
		ProjectID: "example",
		Key:       task.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if failed := tsk585WaitOperation(t, s, first.OperationID); failed.Status != "failed" {
		t.Fatalf("first attempt=%#v", failed)
	}
	tsk585LaneCommit(t, s, task.ID, "mutate the reviewed candidate")
	if _, err := s.TaskExecutionTestAsync(workerCtx, TaskExecutionTestInput{
		ProjectID: "example",
		Key:       task.ID,
	}); err == nil || !strings.Contains(err.Error(), "exact clean reviewed head") {
		t.Fatalf("drifted candidate was not rejected: %v", err)
	}
}
