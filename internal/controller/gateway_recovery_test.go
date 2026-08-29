package controller

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
)

func testGatewayRecoveryController(t *testing.T) Controller {
	t.Helper()
	root := t.TempDir()
	return Controller{Config: config.Config{
		StateDir: filepath.Join(root, "state"),
		Controller: config.ControllerConfig{
			GatewayBinary:      "/bin/false",
			TunnelClientBinary: "/bin/false",
			PIDDir:             filepath.Join(root, "pid"),
			LogDir:             filepath.Join(root, "logs"),
		},
	}}
}

func TestAcceptGatewayRecoveryDefersWorkerUntilResponseRelease(t *testing.T) {
	c := testGatewayRecoveryController(t)
	oldLaunch := gatewayRecoveryWorkerLaunchFn
	defer func() { gatewayRecoveryWorkerLaunchFn = oldLaunch }()
	launches := 0
	gatewayRecoveryWorkerLaunchFn = func(Controller, string) error {
		launches++
		return nil
	}
	var released []func()
	result, err := c.AcceptGatewayRecovery("restart-1", func(work func()) { released = append(released, work) })
	if err != nil {
		t.Fatal(err)
	}
	if result.OperationID != "restart-1" || result.Outcome != "accepted" {
		t.Fatalf("accepted result=%#v", result)
	}
	if launches != 0 || len(released) != 1 {
		t.Fatalf("worker launched before response release: launches=%d callbacks=%d", launches, len(released))
	}
	receipt, exists, err := readGatewayRecoveryReceipt(gatewayRecoveryPath(c.Config.StateDir, "restart-1"), "restart-1")
	if err != nil || !exists || receipt.Outcome != "accepted" {
		t.Fatalf("durable accepted receipt=%#v exists=%v err=%v", receipt, exists, err)
	}
	released[0]()
	if launches != 1 {
		t.Fatalf("worker launches=%d after response release, want 1", launches)
	}
}

func TestAcceptGatewayRecoveryReusesTerminalReceipts(t *testing.T) {
	c := testGatewayRecoveryController(t)
	operationID := "restart-terminal"
	path := gatewayRecoveryPath(c.Config.StateDir, operationID)
	succeeded := gatewayRecoveryReceipt{GatewayRecoveryResult: GatewayRecoveryResult{OperationID: operationID, Outcome: "succeeded", GatewayReady: true}}
	if err := fsutil.WriteJSONAtomic(path, succeeded, 0o600); err != nil {
		t.Fatal(err)
	}
	called := false
	result, err := c.AcceptGatewayRecovery(operationID, func(func()) { called = true })
	if err != nil || result.Outcome != "succeeded" || called {
		t.Fatalf("successful retry result=%#v err=%v callback=%v", result, err, called)
	}

	failureID := "restart-failed"
	failurePath := gatewayRecoveryPath(c.Config.StateDir, failureID)
	failure := gatewayRecoveryReceipt{GatewayRecoveryResult: GatewayRecoveryResult{OperationID: failureID, Outcome: "failed"}, Error: "readiness timeout"}
	if err := fsutil.WriteJSONAtomic(failurePath, failure, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = c.AcceptGatewayRecovery(failureID, func(func()) { t.Fatal("failed retry scheduled a worker") })
	var typed GatewayRecoveryFailure
	if !errors.As(err, &typed) || typed.OperationID != failureID || typed.Cause != "readiness timeout" {
		t.Fatalf("failed retry error=%T %v", err, err)
	}
}

func TestRestartGatewayRecoveryReusesTerminalFailureWithoutRestart(t *testing.T) {
	c := testGatewayRecoveryController(t)
	operationID := "restart-worker-failed"
	path := gatewayRecoveryPath(c.Config.StateDir, operationID)
	receipt := gatewayRecoveryReceipt{GatewayRecoveryResult: GatewayRecoveryResult{OperationID: operationID, Outcome: "failed"}, Error: "gateway did not become ready"}
	if err := fsutil.WriteJSONAtomic(path, receipt, 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := c.RestartGatewayRecovery(operationID)
	var typed GatewayRecoveryFailure
	if !errors.As(err, &typed) || typed.OperationID != operationID || result.Outcome != "failed" {
		t.Fatalf("terminal failure retry result=%#v err=%T %v", result, err, err)
	}
}
