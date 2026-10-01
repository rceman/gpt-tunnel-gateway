package controller

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
)

// TestMain lets the copied test binary stand in as a spawned Gateway stub:
// startProcess supplies GPT_TUNNEL_CONFIG and --config argv with no test
// flags, so when the <config>.listener sidecar holds a listen address this
// process serves a minimal /readyz there instead of running the suite.
func TestMain(m *testing.M) {
	if cfg := os.Getenv("GPT_TUNNEL_CONFIG"); cfg != "" {
		if data, err := os.ReadFile(cfg + ".listener"); err == nil {
			listener, err := net.Listen("tcp", strings.TrimSpace(string(data)))
			if err != nil {
				os.Exit(2)
			}
			mux := http.NewServeMux()
			mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
			_ = http.Serve(listener, mux)
			return
		}
	}
	os.Exit(m.Run())
}

// TestTSK671EndpointOwnerHelper is the listener stub: when env-marked it binds
// the requested endpoint and parks. argv carries `--config <path>` after `--`
// so the managed-orphan proof sees the configured gateway command shape.
func TestTSK671EndpointOwnerHelper(t *testing.T) {
	addr := os.Getenv("GPT_TUNNEL_ENDPOINT_HELPER_ADDR")
	if addr == "" {
		return
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("helper listener bind failed: %v", err)
	}
	defer listener.Close()
	fmt.Println("listening")
	time.Sleep(5 * time.Minute)
}

func tsk671Controller(t *testing.T, binaryName string) (Controller, string) {
	t.Helper()
	dir := t.TempDir()
	binary := filepath.Join(dir, binaryName)
	data, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, data, 0o700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	_ = listener.Close()
	configPath := filepath.Join(dir, "gateway.json")
	if err := os.WriteFile(configPath, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := Controller{
		ConfigPath: configPath,
		Config: config.Config{
			ListenAddr: addr,
			StateDir:   dir,
			Controller: config.ControllerConfig{
				PIDDir:             filepath.Join(dir, "pid"),
				LogDir:             filepath.Join(dir, "log"),
				GatewayBinary:      binary,
				TunnelClientBinary: binary,
			},
		},
	}
	return c, addr
}

// tsk671SpawnListener spawns the endpoint stub: exe basename is controlled by
// binaryPath (the managed proof hinges on it matching the configured Gateway
// binary class), and argv carries the given config path.
func tsk671SpawnListener(t *testing.T, binaryPath, addr, configArg string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(binaryPath, "-test.run=^TestTSK671EndpointOwnerHelper$", "--", "--config", configArg)
	cmd.Env = append(os.Environ(), "GPT_TUNNEL_ENDPOINT_HELPER_ADDR="+addr)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		owners, err := endpointOwnerPIDs(addr)
		if err == nil {
			for _, pid := range owners {
				if pid == cmd.Process.Pid {
					return cmd
				}
			}
		}
		if !alive(cmd.Process.Pid) {
			t.Fatalf("listener helper exited early")
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("listener helper did not bind %s", addr)
	return nil
}

// TestTSK671StaleManagedOrphanRetired reproduces the TSK670 incident: an
// orphaned daemon running an older (deleted-path class) binary owns the
// endpoint while the PID record is missing. Retirement must kill only the
// proven orphan and free the port.
func TestTSK671StaleManagedOrphanRetired(t *testing.T) {
	c, addr := tsk671Controller(t, "gpt-tunnel-gatewayd")
	stale := tsk671SpawnListener(t, c.Config.Controller.GatewayBinary, addr, c.ConfigPath)

	if err := c.retireStaleGatewayEndpointOwners(); err != nil {
		t.Fatalf("managed orphan retirement failed: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for alive(stale.Process.Pid) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if alive(stale.Process.Pid) {
		t.Fatalf("proven stale managed orphan still runs")
	}
	if owners, err := endpointOwnerPIDs(addr); err != nil || len(owners) != 0 {
		t.Fatalf("endpoint still owned after orphan retirement: %v %v", owners, err)
	}
}

// TestTSK671UnprovenEndpointOwnerFailsClosed: a listener whose executable is
// not the managed Gateway class must never be signaled — activation fails
// closed with bounded evidence instead.
func TestTSK671UnprovenEndpointOwnerFailsClosed(t *testing.T) {
	c, addr := tsk671Controller(t, "gpt-tunnel-gatewayd")
	dir := filepath.Dir(c.Config.Controller.GatewayBinary)
	foreign := filepath.Join(dir, "not-a-gateway")
	data, err := os.ReadFile(os.Args[0])
	if err != nil || os.WriteFile(foreign, data, 0o700) != nil {
		t.Fatal(err)
	}
	owner := tsk671SpawnListener(t, foreign, addr, c.ConfigPath)

	err = c.retireStaleGatewayEndpointOwners()
	if err == nil || !strings.Contains(err.Error(), "unproven") {
		t.Fatalf("expected fail-closed endpoint error, got %v", err)
	}
	if !alive(owner.Process.Pid) {
		t.Fatalf("unproven endpoint owner was signaled")
	}
	if _, err := endpointOwnerPIDs(addr); err != nil {
		t.Fatal(err)
	}
}

// TestTSK671ForeignConfigEndpointOwnerFailsClosed: the same binary class
// serving a different config is not provably this controller's managed
// daemon and must be left alone.
func TestTSK671ForeignConfigEndpointOwnerFailsClosed(t *testing.T) {
	c, addr := tsk671Controller(t, "gpt-tunnel-gatewayd")
	owner := tsk671SpawnListener(t, c.Config.Controller.GatewayBinary, addr, filepath.Join(t.TempDir(), "other.json"))

	if err := c.retireStaleGatewayEndpointOwners(); err == nil {
		t.Fatalf("foreign-config owner was retired")
	}
	if !alive(owner.Process.Pid) {
		t.Fatalf("foreign-config endpoint owner was signaled")
	}
}

// TestTSK671RecordedDaemonOwnerLeftAlone: when the tracked PID still owns the
// endpoint the canonical stop path owns the lifecycle; the orphan sweep must
// not signal it.
func TestTSK671RecordedDaemonOwnerLeftAlone(t *testing.T) {
	c, addr := tsk671Controller(t, "gpt-tunnel-gatewayd")
	owner := tsk671SpawnListener(t, c.Config.Controller.GatewayBinary, addr, c.ConfigPath)
	start, err := procStartTime(owner.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	if err := fsutil.EnsureDir(c.Config.Controller.PIDDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := fsutil.WriteJSONAtomic(c.pidPath("gateway"), pidRecord{
		PID:            owner.Process.Pid,
		StartTimeTicks: start,
		UID:            uint32(os.Getuid()),
	}, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := c.retireStaleGatewayEndpointOwners(); err != nil {
		t.Fatalf("recorded owner sweep failed: %v", err)
	}
	if !alive(owner.Process.Pid) {
		t.Fatalf("recorded endpoint owner was signaled")
	}
}

// TestTSK671EndpointCoherenceVerification: post-readiness proof — an endpoint
// owner whose executable is not the installed binary fails the check; an owner
// running the exact installed binary passes.
func TestTSK671EndpointCoherenceVerification(t *testing.T) {
	c, addr := tsk671Controller(t, "gpt-tunnel-gatewayd")
	if err := c.verifyGatewayEndpointCoherence(); err != nil {
		t.Fatalf("empty endpoint must be coherent: %v", err)
	}
	owner := tsk671SpawnListener(t, c.Config.Controller.GatewayBinary, addr, c.ConfigPath)
	if err := c.verifyGatewayEndpointCoherence(); err != nil {
		t.Fatalf("installed-binary owner not coherent: %v", err)
	}
	_ = owner
}

// TestTSK671EndpointCoherenceRejectsShadow: the incident class — the port is
// held by a binary that is not the installed Gateway artifact — must fail
// coherence even though readiness could answer.
func TestTSK671EndpointCoherenceRejectsShadow(t *testing.T) {
	c, addr := tsk671Controller(t, "gpt-tunnel-gatewayd")
	dir := filepath.Dir(c.Config.Controller.GatewayBinary)
	shadow := filepath.Join(dir, "shadow-gatewayd")
	data, err := os.ReadFile(os.Args[0])
	if err != nil || os.WriteFile(shadow, data, 0o700) != nil {
		t.Fatal(err)
	}
	tsk671SpawnListener(t, shadow, addr, c.ConfigPath)
	if err := c.verifyGatewayEndpointCoherence(); err == nil {
		t.Fatalf("shadow endpoint owner passed coherence verification")
	}
}

// TestTSK671GatewayStartBlockedByUnprovenOwner wires the reconcile into the
// canonical start path: startProcess must refuse to spawn while an unproven
// process holds the endpoint, without signaling it.
func TestTSK671GatewayStartBlockedByUnprovenOwner(t *testing.T) {
	c, addr := tsk671Controller(t, "gpt-tunnel-gatewayd")
	foreign := filepath.Join(t.TempDir(), "unrelated")
	data, err := os.ReadFile(os.Args[0])
	if err != nil || os.WriteFile(foreign, data, 0o700) != nil {
		t.Fatal(err)
	}
	owner := tsk671SpawnListener(t, foreign, addr, c.ConfigPath)

	if err := c.startProcess("gateway", c.Config.Controller.GatewayBinary, []string{"--config", c.ConfigPath}, nil); err == nil || !strings.Contains(err.Error(), "endpoint") {
		t.Fatalf("gateway start did not fail closed on unproven endpoint owner: %v", err)
	}
	if !alive(owner.Process.Pid) {
		t.Fatalf("unproven endpoint owner was signaled by start")
	}
	if _, err := readPIDRecord(c.pidPath("gateway")); err == nil {
		t.Fatalf("PID record was written for a blocked start")
	}
}

// TestTSK671ActivationConvergesPastStaleOrphan is the TSK670 incident shape
// end-to-end through the canonical restart path: an old orphan daemon owns
// the endpoint while the PID record is absent. The real stop/start/readiness
// flow must retire the orphan, spawn the installed binary, and verify that
// the process serving the endpoint is the newly recorded managed daemon.
func TestTSK671ActivationConvergesPastStaleOrphan(t *testing.T) {
	c, addr := tsk671Controller(t, "gpt-tunnel-gatewayd")
	// The spawned stub turns into a readyz listener via the marker sidecar.
	if err := os.WriteFile(c.ConfigPath+".listener", []byte(addr), 0o600); err != nil {
		t.Fatal(err)
	}
	stale := tsk671SpawnListener(t, c.Config.Controller.GatewayBinary, addr, c.ConfigPath)

	diagnostics, err := c.restartGatewayAfterUpgradeDiagnosticsLocked(false, time.Now())
	if err != nil {
		t.Fatalf("canonical restart did not converge past stale orphan: %v %#v", err, diagnostics)
	}
	if !diagnostics.ReadinessPassed {
		t.Fatalf("readiness did not pass on the converged endpoint: %#v", diagnostics)
	}
	deadline := time.Now().Add(5 * time.Second)
	for alive(stale.Process.Pid) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if alive(stale.Process.Pid) {
		t.Fatalf("stale orphan survived canonical restart")
	}
	record, err := readPIDRecord(c.pidPath("gateway"))
	if err != nil || record.PID < 1 {
		t.Fatalf("gateway PID record was not reconciled to the new daemon: %#v %v", record, err)
	}
	owners, err := endpointOwnerPIDs(addr)
	if err != nil || len(owners) != 1 || owners[0] != record.PID {
		t.Fatalf("endpoint owner is not the recorded new daemon: %v %v record=%#v", owners, err, record)
	}
	if exe, err := procExe(record.PID); err != nil || exe != c.Config.Controller.GatewayBinary {
		t.Fatalf("serving daemon does not map to the installed binary: exe=%q err=%v", exe, err)
	}
	// Reconcile the controller PID state: the record must describe the live
	// serving daemon.
	status := c.ProcessStatus("gateway")
	if !status.Running || !status.IdentityValid || status.PID != record.PID {
		t.Fatalf("controller PID state not reconciled to live daemon: %#v", status)
	}
	if err := c.stopProcess("gateway", c.Config.Controller.GatewayBinary); err != nil {
		t.Fatalf("new daemon cleanup failed: %v", err)
	}
}
