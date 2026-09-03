package debug

import (
	"context"
	"errors"
	"io/fs"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/controller"
	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
)

func testActivationConfig(t *testing.T) config.Config {
	t.Helper()
	root := t.TempDir()
	return config.Config{StateDir: root, RunTimeoutSeconds: 60, Controller: config.ControllerConfig{PIDDir: root, LogDir: root}}
}

func TestAcceptActivationPersistsBoundedReceiptBeforeWorkerRelease(t *testing.T) {
	oldWorker := workerLaunchFn
	defer func() { workerLaunchFn = oldWorker }()
	var launches atomic.Int32
	workerLaunchFn = func(controller.Controller, string, string) error {
		launches.Add(1)
		return nil
	}
	c := testActivationConfig(t)
	source := strings.Repeat("a", 40)
	var callbacks []func()
	result, err := AcceptActivation(c, "config.json", source, func(work func()) { callbacks = append(callbacks, work) })
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != "accepted" || result.OperationID != operationID(source) {
		t.Fatalf("accepted result=%#v", result)
	}
	receipt, exists, err := readReceipt(receiptPath(c.StateDir, result.OperationID), result.OperationID)
	if err != nil || !exists || receipt.Outcome != "accepted" {
		t.Fatalf("accepted receipt=%#v exists=%v err=%v", receipt, exists, err)
	}
	if launches.Load() != 0 || len(callbacks) != 1 {
		t.Fatalf("worker released before response completion: launches=%d callbacks=%d", launches.Load(), len(callbacks))
	}
	callbacks[0]()
	if launches.Load() != 1 {
		t.Fatalf("worker launches=%d want 1", launches.Load())
	}
}

func TestRunActivationTerminalReceiptIsIdempotent(t *testing.T) {
	oldWorker := workerLaunchFn
	defer func() { workerLaunchFn = oldWorker }()
	workerLaunchFn = func(controller.Controller, string, string) error { return nil }
	c := testActivationConfig(t)
	source := strings.Repeat("b", 40)
	var callback func()
	accepted, err := AcceptActivation(c, "config.json", source, func(work func()) { callback = work })
	if err != nil {
		t.Fatal(err)
	}
	callback()
	var executions atomic.Int32
	execute := func(context.Context) (ActivationResult, error) {
		executions.Add(1)
		return ActivationResult{SourceHead: source, Activation: "passed", Smoke: "passed", TunnelPID: 42, GatewayPID: 43}, nil
	}
	first, err := RunActivation(c, "config.json", accepted.OperationID, source, execute)
	if err != nil || first.Outcome != "succeeded" {
		t.Fatalf("first terminal result=%#v err=%v", first, err)
	}
	second, err := RunActivation(c, "config.json", accepted.OperationID, source, execute)
	if err != nil || second != first {
		t.Fatalf("idempotent retry first=%#v second=%#v err=%v", first, second, err)
	}
	if executions.Load() != 1 {
		t.Fatalf("activation executions=%d want 1", executions.Load())
	}
}

func TestRunActivationPersistsTerminalFailure(t *testing.T) {
	oldWorker := workerLaunchFn
	defer func() { workerLaunchFn = oldWorker }()
	workerLaunchFn = func(controller.Controller, string, string) error { return nil }
	c := testActivationConfig(t)
	source := strings.Repeat("c", 40)
	var callback func()
	accepted, err := AcceptActivation(c, "config.json", source, func(work func()) { callback = work })
	if err != nil {
		t.Fatal(err)
	}
	callback()
	failureCause := errors.New("rollback completed")
	_, err = RunActivation(c, "config.json", accepted.OperationID, source, func(context.Context) (ActivationResult, error) {
		return ActivationResult{}, failureCause
	})
	var typed ActivationFailure
	if !errors.As(err, &typed) || typed.OperationID != accepted.OperationID || typed.Cause != failureCause.Error() {
		t.Fatalf("terminal failure=%T %v", err, err)
	}
	_, err = RunActivation(c, "config.json", accepted.OperationID, source, func(context.Context) (ActivationResult, error) {
		t.Fatal("terminal retry executed activation")
		return ActivationResult{}, nil
	})
	if !errors.As(err, &typed) {
		t.Fatalf("failure retry=%T %v", err, err)
	}
	receipt, exists, readErr := readReceipt(receiptPath(c.StateDir, accepted.OperationID), accepted.OperationID)
	if readErr != nil || !exists || receipt.Outcome != "failed" || receipt.Error != failureCause.Error() {
		t.Fatalf("failure receipt=%#v exists=%v err=%v", receipt, exists, readErr)
	}
}

func TestRunActivationUsesConfiguredBoundedContext(t *testing.T) {
	oldWorker := workerLaunchFn
	defer func() { workerLaunchFn = oldWorker }()
	workerLaunchFn = func(controller.Controller, string, string) error { return nil }
	c := testActivationConfig(t)
	c.RunTimeoutSeconds = 60
	source := strings.Repeat("d", 40)
	var callback func()
	accepted, err := AcceptActivation(c, "config.json", source, func(work func()) { callback = work })
	if err != nil {
		t.Fatal(err)
	}
	callback()
	var deadline time.Time
	_, err = RunActivation(c, "config.json", accepted.OperationID, source, func(ctx context.Context) (ActivationResult, error) {
		var ok bool
		deadline, ok = ctx.Deadline()
		if !ok {
			t.Fatal("activation context has no deadline")
		}
		return ActivationResult{SourceHead: source}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if remaining := time.Until(deadline); remaining <= 0 || remaining > time.Minute {
		t.Fatalf("activation deadline remaining=%s, want a bounded 60s context", remaining)
	}
}

func TestAcceptActivationDoesNotRescheduleExistingReceipt(t *testing.T) {
	oldWorker := workerLaunchFn
	defer func() { workerLaunchFn = oldWorker }()
	var launches atomic.Int32
	workerLaunchFn = func(controller.Controller, string, string) error {
		launches.Add(1)
		return nil
	}
	c := testActivationConfig(t)
	source := strings.Repeat("e", 40)
	var firstCallbacks []func()
	accepted, err := AcceptActivation(c, "config.json", source, func(work func()) { firstCallbacks = append(firstCallbacks, work) })
	if err != nil {
		t.Fatal(err)
	}
	if len(firstCallbacks) != 1 {
		t.Fatalf("initial callbacks=%d want 1", len(firstCallbacks))
	}
	var repeatCallbacks int
	if _, err := AcceptActivation(c, "config.json", source, func(func()) { repeatCallbacks++ }); err != nil {
		t.Fatal(err)
	}
	receiptPath := receiptPath(c.StateDir, accepted.OperationID)
	receipt, exists, err := readReceipt(receiptPath, accepted.OperationID)
	if err != nil || !exists {
		t.Fatalf("accepted receipt=%#v exists=%v err=%v", receipt, exists, err)
	}
	receipt.Outcome = "in_progress"
	if err := fsutil.WriteJSONAtomic(receiptPath, receipt, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := AcceptActivation(c, "config.json", source, func(func()) { repeatCallbacks++ }); err != nil {
		t.Fatal(err)
	}
	if repeatCallbacks != 0 {
		t.Fatalf("repeat callbacks=%d want 0", repeatCallbacks)
	}
	firstCallbacks[0]()
	if launches.Load() != 1 {
		t.Fatalf("worker launches=%d want 1", launches.Load())
	}
}

func TestRunActivationRetriesTransientSuccessReceiptWriteWithoutFalseFailure(t *testing.T) {
	oldWorker := workerLaunchFn
	oldWriter := writeReceiptAtomicFn
	defer func() {
		workerLaunchFn = oldWorker
		writeReceiptAtomicFn = oldWriter
	}()
	workerLaunchFn = func(controller.Controller, string, string) error { return nil }
	c := testActivationConfig(t)
	source := strings.Repeat("f", 40)
	var callback func()
	accepted, err := AcceptActivation(c, "config.json", source, func(work func()) { callback = work })
	if err != nil {
		t.Fatal(err)
	}
	callback()

	var writes atomic.Int32
	writeReceiptAtomicFn = func(path string, value any, mode fs.FileMode) error {
		if writes.Add(1) == 1 {
			return errors.New("transient receipt write")
		}
		return fsutil.WriteJSONAtomic(path, value, mode)
	}
	result, err := RunActivation(c, "config.json", accepted.OperationID, source, func(context.Context) (ActivationResult, error) {
		return ActivationResult{SourceHead: source, Activation: "passed", Smoke: "passed", TunnelPID: 42, GatewayPID: 43}, nil
	})
	if err != nil || result.Outcome != "succeeded" {
		t.Fatalf("successful activation result=%#v err=%v", result, err)
	}
	if writes.Load() != receiptWriteAttempts {
		t.Fatalf("receipt writes=%d want %d", writes.Load(), receiptWriteAttempts)
	}
	receipt, exists, err := readReceipt(receiptPath(c.StateDir, accepted.OperationID), accepted.OperationID)
	if err != nil || !exists || receipt.Outcome != "succeeded" || receipt.Error != "" {
		t.Fatalf("success receipt=%#v exists=%v err=%v", receipt, exists, err)
	}
}

func TestRunActivationRetriesTransientFailureReceiptWriteAndPreservesFailure(t *testing.T) {
	oldWorker := workerLaunchFn
	oldWriter := writeReceiptAtomicFn
	defer func() {
		workerLaunchFn = oldWorker
		writeReceiptAtomicFn = oldWriter
	}()
	workerLaunchFn = func(controller.Controller, string, string) error { return nil }
	c := testActivationConfig(t)
	source := strings.Repeat("1", 40)
	var callback func()
	accepted, err := AcceptActivation(c, "config.json", source, func(work func()) { callback = work })
	if err != nil {
		t.Fatal(err)
	}
	callback()

	var writes atomic.Int32
	writeReceiptAtomicFn = func(path string, value any, mode fs.FileMode) error {
		if writes.Add(1) == 1 {
			return errors.New("transient receipt write")
		}
		return fsutil.WriteJSONAtomic(path, value, mode)
	}
	failureCause := errors.New("rollback completed")
	result, err := RunActivation(c, "config.json", accepted.OperationID, source, func(context.Context) (ActivationResult, error) {
		return ActivationResult{}, failureCause
	})
	var typed ActivationFailure
	if !errors.As(err, &typed) || typed.Cause != failureCause.Error() || result.Outcome != "failed" {
		t.Fatalf("failure result=%#v err=%T %v", result, err, err)
	}
	if writes.Load() != receiptWriteAttempts {
		t.Fatalf("receipt writes=%d want %d", writes.Load(), receiptWriteAttempts)
	}
	receipt, exists, err := readReceipt(receiptPath(c.StateDir, accepted.OperationID), accepted.OperationID)
	if err != nil || !exists || receipt.Outcome != "failed" || receipt.Error != failureCause.Error() {
		t.Fatalf("failure receipt=%#v exists=%v err=%v", receipt, exists, err)
	}
}

func TestRecordLaunchFailureRetriesTransientReceiptWrite(t *testing.T) {
	oldWorker := workerLaunchFn
	oldWriter := writeReceiptAtomicFn
	defer func() {
		workerLaunchFn = oldWorker
		writeReceiptAtomicFn = oldWriter
	}()
	c := testActivationConfig(t)
	source := strings.Repeat("2", 40)
	var callbacks []func()
	accepted, err := AcceptActivation(c, "config.json", source, func(work func()) { callbacks = append(callbacks, work) })
	if err != nil {
		t.Fatal(err)
	}
	workerLaunchFn = func(controller.Controller, string, string) error { return errors.New("worker unavailable") }
	var writes atomic.Int32
	writeReceiptAtomicFn = func(path string, value any, mode fs.FileMode) error {
		if writes.Add(1) == 1 {
			return errors.New("transient receipt write")
		}
		return fsutil.WriteJSONAtomic(path, value, mode)
	}
	if len(callbacks) != 1 {
		t.Fatalf("callbacks=%d want 1", len(callbacks))
	}
	callbacks[0]()
	receipt, exists, err := readReceipt(receiptPath(c.StateDir, accepted.OperationID), accepted.OperationID)
	if err != nil || !exists || receipt.Outcome != "failed" || receipt.Error != "worker unavailable" {
		t.Fatalf("launch failure receipt=%#v exists=%v err=%v", receipt, exists, err)
	}
	if writes.Load() != receiptWriteAttempts {
		t.Fatalf("receipt writes=%d want %d", writes.Load(), receiptWriteAttempts)
	}
}
