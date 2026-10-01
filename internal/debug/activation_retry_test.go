package debug

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/activation"
	"github.com/rceman/gpt-tunnel-gateway/internal/controller"
)

// TestTSK679PreCutoverFailureAdmitsBoundedRetry reproduces the b407c9e4
// dirty-dist incident: a pre-cutover admission failure ("project source
// worktree is dirty") was durably keyed to the source SHA and permanently
// replayed. A retryable failure must admit a fresh evaluation for the same
// exact source while preserving the prior failed attempt's evidence.
func TestTSK679PreCutoverFailureAdmitsBoundedRetry(t *testing.T) {
	oldWorker := workerLaunchFn
	defer func() { workerLaunchFn = oldWorker }()
	var launches atomic.Int32
	workerLaunchFn = func(controller.Controller, string, string) error {
		launches.Add(1)
		return nil
	}
	c := testActivationConfig(t)
	source := strings.Repeat("9", 40)
	var callbacks []func()
	release := func(work func()) { callbacks = append(callbacks, work) }

	accepted, err := AcceptActivation(c, "config.json", source, release)
	if err != nil || accepted.Outcome != "accepted" {
		t.Fatalf("first accept: result=%#v err=%v", accepted, err)
	}
	callbacks[0]()
	path := receiptPath(c.StateDir, accepted.OperationID)

	// First attempt fails pre-cutover: the configured source worktree is
	// dirty. Nothing was stopped, replaced, or mutated.
	dirtyCause := &activation.PreflightError{Cause: errors.New("project source worktree is dirty")}
	_, err = RunActivation(c, "config.json", accepted.OperationID, source, func(context.Context) (ActivationResult, error) {
		return ActivationResult{}, dirtyCause
	})
	if err == nil {
		t.Fatal("first attempt did not fail")
	}
	receipt, exists, err := readReceipt(path, accepted.OperationID)
	if err != nil || !exists || receipt.Outcome != "failed" || !receipt.Retryable || receipt.attempt() != 1 {
		t.Fatalf("pre-cutover failure receipt=%#v exists=%v err=%v", receipt, exists, err)
	}

	// After the operator cleans the source, the same exact source must be
	// re-evaluated — not replayed from the poisoned receipt.
	second, err := AcceptActivation(c, "config.json", source, release)
	if err != nil || second.Outcome != "accepted" {
		t.Fatalf("retryable failure blocked re-evaluation: result=%#v err=%v", second, err)
	}
	if launches.Load() != 1 || len(callbacks) != 2 {
		t.Fatalf("retry did not schedule a new worker: launches=%d callbacks=%d", launches.Load(), len(callbacks))
	}
	receipt, exists, err = readReceipt(path, accepted.OperationID)
	if err != nil || !exists || receipt.Outcome != "accepted" || receipt.attempt() != 2 {
		t.Fatalf("retry receipt=%#v exists=%v err=%v", receipt, exists, err)
	}
	if len(receipt.PriorAttempts) != 1 || receipt.PriorAttempts[0].Attempt != 1 || receipt.PriorAttempts[0].Outcome != "failed" || !strings.Contains(receipt.PriorAttempts[0].Error, "worktree is dirty") {
		t.Fatalf("prior failed attempt evidence not preserved: %#v", receipt.PriorAttempts)
	}

	// The clean second attempt executes and reaches a truthful terminal.
	callbacks[1]()
	var secondExecutions atomic.Int32
	terminal, err := RunActivation(c, "config.json", accepted.OperationID, source, func(context.Context) (ActivationResult, error) {
		secondExecutions.Add(1)
		return ActivationResult{
			SourceHead: source,
			Activation: "passed",
			Smoke:      "passed",
			TunnelPID:  7,
			GatewayPID: 8,
		}, nil
	})
	if err != nil || terminal.Outcome != "succeeded" || secondExecutions.Load() != 1 {
		t.Fatalf("second attempt: terminal=%#v executions=%d err=%v", terminal, secondExecutions.Load(), err)
	}
	receipt, exists, err = readReceipt(path, accepted.OperationID)
	if err != nil || !exists || receipt.Outcome != "succeeded" || receipt.attempt() != 2 || len(receipt.PriorAttempts) != 1 {
		t.Fatalf("terminal receipt lost attempt history: %#v exists=%v err=%v", receipt, exists, err)
	}
}

// TestTSK679OutcomeBearingFailureStaysSticky proves that a failure not
// classified as pre-cutover — the candidate handoff may have begun — still
// replays its durable failed receipt and is never silently re-executed.
func TestTSK679OutcomeBearingFailureStaysSticky(t *testing.T) {
	oldWorker := workerLaunchFn
	defer func() { workerLaunchFn = oldWorker }()
	var launches atomic.Int32
	workerLaunchFn = func(controller.Controller, string, string) error {
		launches.Add(1)
		return nil
	}
	c := testActivationConfig(t)
	source := strings.Repeat("8", 40)
	var callbacks []func()
	accepted, err := AcceptActivation(c, "config.json", source, func(work func()) { callbacks = append(callbacks, work) })
	if err != nil {
		t.Fatal(err)
	}
	callbacks[0]()
	_, err = RunActivation(c, "config.json", accepted.OperationID, source, func(context.Context) (ActivationResult, error) {
		return ActivationResult{}, errors.New("activation runtime identity/readiness/source proof failed")
	})
	var typed ActivationFailure
	if !errors.As(err, &typed) {
		t.Fatalf("terminal failure=%T %v", err, err)
	}
	if _, err := AcceptActivation(c, "config.json", source, func(func()) { t.Error("sticky failure scheduled a retry") }); !errors.As(err, &typed) {
		t.Fatalf("sticky failure replay=%T %v", err, err)
	}
	if launches.Load() != 1 {
		t.Fatalf("sticky failure launched %d workers", launches.Load())
	}
	receipt, exists, err := readReceipt(receiptPath(c.StateDir, accepted.OperationID), accepted.OperationID)
	if err != nil || !exists || receipt.Outcome != "failed" || receipt.Retryable {
		t.Fatalf("sticky receipt=%#v exists=%v err=%v", receipt, exists, err)
	}
}

// TestTSK679RetryAttemptsAreBounded proves the same source cannot loop
// pre-cutover retries forever: after the attempt bound, the failed receipt
// becomes sticky.
func TestTSK679RetryAttemptsAreBounded(t *testing.T) {
	oldWorker := workerLaunchFn
	defer func() { workerLaunchFn = oldWorker }()
	var launches atomic.Int32
	workerLaunchFn = func(controller.Controller, string, string) error {
		launches.Add(1)
		return nil
	}
	c := testActivationConfig(t)
	source := strings.Repeat("7", 40)
	var callbacks []func()
	release := func(work func()) { callbacks = append(callbacks, work) }
	preflightCause := &activation.PreflightError{Cause: errors.New("project source worktree is dirty")}
	for attempt := 1; attempt <= maxDebugActivationAttempts; attempt++ {
		accepted, err := AcceptActivation(c, "config.json", source, release)
		if err != nil || accepted.Outcome != "accepted" {
			t.Fatalf("attempt %d not admitted: result=%#v err=%v", attempt, accepted, err)
		}
		callbacks[len(callbacks)-1]()
		if _, err := RunActivation(c, "config.json", accepted.OperationID, source, func(context.Context) (ActivationResult, error) {
			return ActivationResult{}, preflightCause
		}); err == nil {
			t.Fatalf("attempt %d did not fail", attempt)
		}
	}
	var typed ActivationFailure
	if _, err := AcceptActivation(c, "config.json", source, func(func()) { t.Error("exhausted attempts scheduled a retry") }); !errors.As(err, &typed) {
		t.Fatalf("exhausted attempts did not replay sticky failure: %T %v", err, err)
	}
	if int(launches.Load()) != maxDebugActivationAttempts {
		t.Fatalf("workers launched=%d want %d", launches.Load(), maxDebugActivationAttempts)
	}
	receipt, exists, err := readReceipt(receiptPath(c.StateDir, operationID(source)), operationID(source))
	if err != nil || !exists || receipt.Outcome != "failed" || receipt.attempt() != maxDebugActivationAttempts || len(receipt.PriorAttempts) != maxDebugActivationAttempts-1 {
		t.Fatalf("bounded receipt=%#v exists=%v err=%v", receipt, exists, err)
	}
}

// TestTSK679LaunchFailureIsRetryable proves a worker that never started — no
// candidate ran at all — admits a retry for the same source.
func TestTSK679LaunchFailureIsRetryable(t *testing.T) {
	oldWorker := workerLaunchFn
	defer func() { workerLaunchFn = oldWorker }()
	c := testActivationConfig(t)
	source := strings.Repeat("6", 40)
	var callbacks []func()
	release := func(work func()) { callbacks = append(callbacks, work) }
	var launches atomic.Int32
	workerLaunchFn = func(controller.Controller, string, string) error {
		if launches.Add(1) == 1 {
			return errors.New("worker unavailable")
		}
		return nil
	}
	accepted, err := AcceptActivation(c, "config.json", source, release)
	if err != nil {
		t.Fatal(err)
	}
	callbacks[0]()
	receipt, exists, err := readReceipt(receiptPath(c.StateDir, accepted.OperationID), accepted.OperationID)
	if err != nil || !exists || receipt.Outcome != "failed" || !receipt.Retryable {
		t.Fatalf("launch failure receipt=%#v exists=%v err=%v", receipt, exists, err)
	}
	if _, err := AcceptActivation(c, "config.json", source, release); err != nil {
		t.Fatalf("launch failure did not admit retry: %v", err)
	}
	if len(callbacks) != 2 {
		t.Fatalf("retry callbacks=%d want 2", len(callbacks))
	}
	callbacks[1]()
	if launches.Load() != 2 {
		t.Fatalf("retry launches=%d want 2", launches.Load())
	}
}
