package debug

import (
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/controller"
)

func testActivationConfig(t *testing.T) config.Config {
	t.Helper()
	root := t.TempDir()
	return config.Config{StateDir: root, Controller: config.ControllerConfig{PIDDir: root, LogDir: root}}
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
	execute := func() (ActivationResult, error) {
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
	_, err = RunActivation(c, "config.json", accepted.OperationID, source, func() (ActivationResult, error) {
		return ActivationResult{}, failureCause
	})
	var typed ActivationFailure
	if !errors.As(err, &typed) || typed.OperationID != accepted.OperationID || typed.Cause != failureCause.Error() {
		t.Fatalf("terminal failure=%T %v", err, err)
	}
	_, err = RunActivation(c, "config.json", accepted.OperationID, source, func() (ActivationResult, error) {
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
