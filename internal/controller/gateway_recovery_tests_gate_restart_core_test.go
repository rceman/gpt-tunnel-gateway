package controller

import (
	"errors"
	"io"
	"net"
	"net/http"
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
		return fsutil.WriteJSONAtomic(controller.pidPath("gateway"), pidRecord{
			PID:            process.Process.Pid,
			StartTimeTicks: startTime,
		}, 0o600)
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
