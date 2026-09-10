package controller

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
)

func TestGatewayOnlyLifecycleHelper(t *testing.T) {
	if os.Getenv("GPT_TUNNEL_GATEWAY_ONLY_HELPER") != "1" {
		return
	}
	for {
		time.Sleep(time.Second)
	}
}

func gatewayOnlyTestController(t *testing.T, dir string) Controller {
	t.Helper()
	binary := filepath.Join(dir, "gateway-control")
	data, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, data, 0o700); err != nil {
		t.Fatal(err)
	}
	return Controller{Config: config.Config{ListenAddr: "127.0.0.1:1", Controller: config.ControllerConfig{
		PIDDir: filepath.Join(dir, "pid"), GatewayBinary: binary, TunnelClientBinary: binary,
	}}}
}

func startGatewayOnlyTestProcess(t *testing.T, c Controller, name string) (*exec.Cmd, pidRecord) {
	t.Helper()
	cmd := exec.Command(c.Config.Controller.GatewayBinary, "-test.run=^TestGatewayOnlyLifecycleHelper$", "--")
	cmd.Env = append(os.Environ(), "GPT_TUNNEL_GATEWAY_ONLY_HELPER=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	start, err := procStartTime(cmd.Process.Pid)
	if err != nil {
		_ = cmd.Process.Kill()
		t.Fatal(err)
	}
	record := pidRecord{
		PID:            cmd.Process.Pid,
		StartTimeTicks: start,
		UID:            uint32(os.Getuid()),
		InstanceToken:  name,
	}
	if err := fsutil.WriteJSONAtomic(c.pidPath(name), record, 0o600); err != nil {
		_ = cmd.Process.Kill()
		t.Fatal(err)
	}
	return cmd, record
}

func TestStopGatewayOnlyPreservesTunnelIdentity(t *testing.T) {
	dir := t.TempDir()
	c := gatewayOnlyTestController(t, dir)
	gateway, _ := startGatewayOnlyTestProcess(t, c, "gateway")
	tunnel, tunnelRecord := startGatewayOnlyTestProcess(t, c, "tunnel")
	defer tunnel.Process.Kill()

	if err := c.StopGatewayOnly(); err != nil {
		t.Fatal(err)
	}
	if alive(gateway.Process.Pid) {
		t.Fatal("Gateway process survived Gateway-only stop")
	}
	if !alive(tunnel.Process.Pid) {
		t.Fatal("Gateway-only stop affected Tunnel process")
	}
	data, err := os.ReadFile(c.pidPath("tunnel"))
	if err != nil {
		t.Fatal(err)
	}
	var preserved pidRecord
	if err := json.Unmarshal(data, &preserved); err != nil {
		t.Fatal(err)
	}
	if preserved != tunnelRecord {
		t.Fatalf("Tunnel PID record changed: got=%#v want=%#v", preserved, tunnelRecord)
	}
}

func TestStopGatewayOnlySerializesWithControllerLifecycle(t *testing.T) {
	dir := t.TempDir()
	c := gatewayOnlyTestController(t, dir)
	lock, err := lockfile.Acquire(c.Config.Controller.PIDDir, "controller")
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	if err := c.StopGatewayOnly(); err == nil {
		t.Fatal("Gateway-only stop ignored controller lock contention")
	}
}

func TestStartAndRestartGatewayOnlyNeverTouchTunnel(t *testing.T) {
	oldStop, oldStart, oldWait := restartGatewayStopFn, restartGatewayStartFn, restartGatewayWaitFn
	defer func() { restartGatewayStopFn, restartGatewayStartFn, restartGatewayWaitFn = oldStop, oldStart, oldWait }()
	steps := []string{}
	restartGatewayStopFn = func(c Controller) error {
		steps = append(steps, "stop:"+c.Config.Controller.GatewayBinary)
		return nil
	}
	restartGatewayStartFn = func(c Controller) error {
		steps = append(steps, "start:"+c.Config.Controller.GatewayBinary)
		return nil
	}
	restartGatewayWaitFn = func(string, bool, time.Duration) error {
		steps = append(steps, "ready")
		return nil
	}
	c := Controller{Config: config.Config{Controller: config.ControllerConfig{PIDDir: t.TempDir(), GatewayBinary: "/current/gateway"}}}
	if err := c.StartGatewayOnly(); err != nil {
		t.Fatal(err)
	}
	if _, err := c.RestartGatewayOnlyAfterUpgradeDiagnostics(); err != nil {
		t.Fatal(err)
	}
	if len(steps) != 5 || steps[0] != "start:/current/gateway" || steps[1] != "ready" || steps[2] != "stop:/current/gateway" || steps[3] != "start:/current/gateway" || steps[4] != "ready" {
		t.Fatalf("Gateway-only lifecycle steps=%v", steps)
	}
}

func TestControllerStopStillStopsTunnelAndGateway(t *testing.T) {
	dir := t.TempDir()
	c := gatewayOnlyTestController(t, dir)
	gateway, _ := startGatewayOnlyTestProcess(t, c, "gateway")
	tunnel, _ := startGatewayOnlyTestProcess(t, c, "tunnel")
	if err := c.Stop(); err != nil {
		t.Fatal(err)
	}
	if alive(gateway.Process.Pid) || alive(tunnel.Process.Pid) {
		t.Fatal("full Controller.Stop did not stop both processes")
	}
}
