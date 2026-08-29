package controller

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
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

func TestGatewayRecoveryDuplicateOperationRestartsOneRealProcess(t *testing.T) {
	configPath := os.Getenv("GPT_TUNNEL_CONFIG")
	if configPath != "" {
		address, err := os.ReadFile(configPath)
		if err != nil {
			t.Fatal(err)
		}
		listener, err := net.Listen("tcp", strings.TrimSpace(string(address)))
		if err != nil {
			t.Fatal(err)
		}
		server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/readyz" {
				w.WriteHeader(http.StatusOK)
				return
			}
			http.NotFound(w, r)
		})}
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Fatal(err)
		}
		return
	}

	oldLaunch, oldStop, oldStart, oldWait := gatewayRecoveryWorkerLaunchFn, gatewayRecoveryStopFn, gatewayRecoveryStartFn, gatewayRecoveryWaitFn
	defer func() {
		gatewayRecoveryWorkerLaunchFn, gatewayRecoveryStopFn, gatewayRecoveryStartFn, gatewayRecoveryWaitFn = oldLaunch, oldStop, oldStart, oldWait
	}()

	root := t.TempDir()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(root, "gateway-address")
	if err := os.WriteFile(configFile, []byte(address+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	binary, err := filepath.Abs(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	c := Controller{Config: config.Config{
		ListenAddr: address,
		StateDir:   filepath.Join(root, "state"),
		Controller: config.ControllerConfig{
			GatewayBinary:      binary,
			TunnelClientBinary: binary,
			PIDDir:             filepath.Join(root, "pid"),
			LogDir:             filepath.Join(root, "logs"),
		},
	}}
	if err := fsutil.WriteJSONAtomic(filepath.Join(c.Config.Controller.PIDDir, "tunnel.pid"), pidRecord{PID: os.Getpid()}, 0o600); err != nil {
		t.Fatal(err)
	}

	var helpers []*os.Process
	t.Cleanup(func() {
		for _, process := range helpers {
			if process != nil {
				_ = process.Kill()
				_, _ = process.Wait()
			}
		}
	})
	var stops, starts, launches atomic.Int32
	firstStart := make(chan struct{})
	allowStart := make(chan struct{})
	secondLaunch := make(chan struct{})
	var workers sync.WaitGroup
	startHelper := func(controller Controller) error {
		process := exec.Command(controller.Config.Controller.GatewayBinary, "-test.run=^TestGatewayRecoveryDuplicateOperationRestartsOneRealProcess$", "-test.v=false")
		process.Env = processEnv([]string{"GPT_TUNNEL_CONFIG=" + configFile})
		process.Stdout = io.Discard
		process.Stderr = io.Discard
		if err := process.Start(); err != nil {
			return err
		}
		helpers = append(helpers, process.Process)
		startTime, err := procStartTime(process.Process.Pid)
		if err != nil {
			_ = process.Process.Kill()
			return err
		}
		return fsutil.WriteJSONAtomic(controller.pidPath("gateway"), pidRecord{PID: process.Process.Pid, StartTimeTicks: startTime}, 0o600)
	}
	if err := startHelper(c); err != nil {
		t.Fatal(err)
	}
	if err := waitURL(c.gatewayReadyURL(), true, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	gatewayRecoveryStopFn = func(controller Controller) error {
		stops.Add(1)
		return controller.stopProcess("gateway", controller.Config.Controller.GatewayBinary)
	}
	gatewayRecoveryStartFn = func(controller Controller) error {
		if err := startHelper(controller); err != nil {
			return err
		}
		if starts.Add(1) == 1 {
			close(firstStart)
			<-allowStart
		}
		return nil
	}
	gatewayRecoveryWaitFn = waitURL
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
	if _, err := c.AcceptGatewayRecovery("restart-real", release); err != nil {
		t.Fatal(err)
	}
	<-firstStart
	if _, err := c.AcceptGatewayRecovery("restart-real", release); err != nil {
		t.Fatal(err)
	}
	<-secondLaunch
	close(allowStart)
	workers.Wait()

	if stops.Load() != 1 || starts.Load() != 1 {
		t.Fatalf("actual restart stop/start=%d/%d, want 1/1", stops.Load(), starts.Load())
	}
	receipt, exists, err := readGatewayRecoveryReceipt(gatewayRecoveryPath(c.Config.StateDir, "restart-real"), "restart-real")
	if err != nil || !exists || receipt.Outcome != "succeeded" || !receipt.GatewayReady || receipt.NewPID == 0 {
		t.Fatalf("actual recovery receipt=%#v exists=%v err=%v", receipt, exists, err)
	}
	if receipt.TunnelPID != os.Getpid() {
		t.Fatalf("actual recovery TunnelPID=%d, want %d", receipt.TunnelPID, os.Getpid())
	}
	if launches.Load() != 2 {
		t.Fatalf("worker attempts=%d, want two serialized attempts for one restart", launches.Load())
	}
	if tunnel := c.process("tunnel", mustEval(c.Config.Controller.TunnelClientBinary)); tunnel.PID != os.Getpid() {
		t.Fatalf("actual recovery changed Tunnel PID to %d", tunnel.PID)
	}
}
