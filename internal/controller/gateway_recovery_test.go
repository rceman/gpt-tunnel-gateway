package controller

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

func TestGatewayRecoveryDuplicateOperationRestartsOnceAndPreservesTunnel(t *testing.T) {
	oldLaunch, oldStop, oldStart, oldWait := gatewayRecoveryWorkerLaunchFn, gatewayRecoveryStopFn, gatewayRecoveryStartFn, gatewayRecoveryWaitFn
	defer func() {
		gatewayRecoveryWorkerLaunchFn, gatewayRecoveryStopFn, gatewayRecoveryStartFn, gatewayRecoveryWaitFn = oldLaunch, oldStop, oldStart, oldWait
	}()

	ready := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/readyz" {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	defer ready.Close()

	root := t.TempDir()
	c := Controller{Config: config.Config{
		ListenAddr: strings.TrimPrefix(ready.URL, "http://"),
		StateDir:   filepath.Join(root, "state"),
		Controller: config.ControllerConfig{
			GatewayBinary:      "/bin/false",
			TunnelClientBinary: "/bin/false",
			PIDDir:             filepath.Join(root, "pid"),
			LogDir:             filepath.Join(root, "logs"),
		},
	}}
	if err := fsutil.WriteJSONAtomic(filepath.Join(c.Config.Controller.PIDDir, "tunnel.pid"), pidRecord{PID: os.Getpid()}, 0o600); err != nil {
		t.Fatal(err)
	}

	var stops, starts, launches atomic.Int32
	firstStart := make(chan struct{})
	allowStart := make(chan struct{})
	secondLaunch := make(chan struct{})
	var workers sync.WaitGroup
	gatewayRecoveryStopFn = func(Controller) error {
		stops.Add(1)
		return nil
	}
	gatewayRecoveryStartFn = func(Controller) error {
		if starts.Add(1) == 1 {
			close(firstStart)
			<-allowStart
		}
		return nil
	}
	gatewayRecoveryWaitFn = func(string, bool, time.Duration) error { return nil }
	gatewayRecoveryWorkerLaunchFn = func(workerController Controller, operationID string) error {
		workers.Add(1)
		defer workers.Done()
		if launches.Add(1) == 2 {
			close(secondLaunch)
		}
		_, err := workerController.RestartGatewayRecovery(operationID)
		return err
	}
	release := func(work func()) { go work() }
	if _, err := c.AcceptGatewayRecovery("restart-once", release); err != nil {
		t.Fatal(err)
	}
	<-firstStart
	if _, err := c.AcceptGatewayRecovery("restart-once", release); err != nil {
		t.Fatal(err)
	}
	<-secondLaunch
	close(allowStart)
	workers.Wait()

	if got := stops.Load(); got != 1 {
		t.Fatalf("gateway stop calls=%d, want 1", got)
	}
	if got := starts.Load(); got != 1 {
		t.Fatalf("gateway start calls=%d, want 1", got)
	}
	receipt, exists, err := readGatewayRecoveryReceipt(gatewayRecoveryPath(c.Config.StateDir, "restart-once"), "restart-once")
	if err != nil || !exists || receipt.Outcome != "succeeded" || !receipt.GatewayReady {
		t.Fatalf("recovery receipt=%#v exists=%v err=%v", receipt, exists, err)
	}
	if receipt.TunnelPID != os.Getpid() {
		t.Fatalf("receipt TunnelPID=%d, want %d", receipt.TunnelPID, os.Getpid())
	}
	if tunnel := c.process("tunnel", mustEval(c.Config.Controller.TunnelClientBinary)); tunnel.PID != os.Getpid() {
		t.Fatalf("Tunnel PID changed to %d", tunnel.PID)
	}
	if launches.Load() != 2 {
		t.Fatalf("worker launches=%d, want two serialized attempts for one restart", launches.Load())
	}
}
