package upgrade

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/controller"
)

type fakeUpgradeController struct {
	statuses                             []controller.Status
	index                                int
	doctorCalls, restartCalls, stopCalls int
	startupDiagnostics                   controller.GatewayStartupDiagnostics
	startupErr                           error
}

func (f *fakeUpgradeController) Status(context.Context) (controller.Status, error) {
	s := f.statuses[f.index]
	if f.index < len(f.statuses)-1 {
		f.index++
	}
	return s, nil
}
func (f *fakeUpgradeController) Doctor(context.Context) error      { f.doctorCalls++; return nil }
func (f *fakeUpgradeController) RestartGatewayAfterUpgrade() error { f.restartCalls++; return nil }
func (f *fakeUpgradeController) StopGatewayForUpgrade() error      { f.stopCalls++; return nil }
func (f *fakeUpgradeController) RestartGatewayAfterUpgradeDiagnostics() (controller.GatewayStartupDiagnostics, error) {
	f.restartCalls++
	d := f.startupDiagnostics
	if d.Phase == "" {
		d.Phase = "TARGET_STARTUP"
	}
	if d.CaptureStatus == "" {
		d.CaptureStatus = "captured"
	}
	if f.startupErr == nil {
		d.ReadinessPassed = true
	}
	return d, f.startupErr
}

func upgradeIntegrationFixture(t *testing.T) (config.Config, string, string, *fakeUpgradeController, func()) {
	t.Helper()
	dir := t.TempDir()
	root := filepath.Join(dir, "gpt-tunnel-gateway")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "VERSION"), []byte("0.2.3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	paths := make(map[string]string)
	for _, name := range binaryOrder {
		path := filepath.Join(binDir, name)
		paths[name] = path
		if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf '0.2.2\\n'\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	envPath := filepath.Join(dir, "tunnel.env")
	if err := os.WriteFile(envPath, []byte("CONTROL_PLANE_API_KEY=redacted-test\nCONTROL_PLANE_TUNNEL_ID=tunnel_0123456789abcdef0123456789abcdef\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tunnelPath := filepath.Join(dir, "tunnel-client")
	if err := os.WriteFile(tunnelPath, []byte("tunnel"), 0o700); err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(dir, "state")
	if err := os.MkdirAll(filepath.Join(stateDir, "pid"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "pid", "gateway.pid"), []byte("10\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "pid", "tunnel.pid"), []byte("20\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := config.Config{GatewayID: "home", ListenAddr: "127.0.0.1:0", StateDir: stateDir, Hub: config.HubConfig{Branch: "gpt-tunnel/home"}, Controller: config.ControllerConfig{GatewayBinary: paths["gpt-tunnel-gatewayd"], TunnelClientBinary: tunnelPath, TunnelEnvFile: envPath, PIDDir: filepath.Join(stateDir, "pid"), LogDir: filepath.Join(stateDir, "logs"), TunnelHealthListenAddr: "127.0.0.1:8766"}}
	before := controller.Status{Gateway: controller.ProcessStatus{Running: true, PID: 10, Executable: paths["gpt-tunnel-gatewayd"]}, Tunnel: controller.ProcessStatus{Running: true, PID: 20, Executable: tunnelPath}, GatewayReady: true, TunnelReady: true}
	before.Gateway.IdentityValid, before.Tunnel.IdentityValid = true, true
	before.InstalledVersion, before.RunningVersion, before.VersionMatch = "0.2.2", "0.2.2", true
	after := before
	after.Gateway.PID = 11
	after.InstalledVersion, after.RunningVersion, after.VersionMatch = "0.2.3", "0.2.3", true
	rolled := before
	rolled.Gateway.PID = 12
	fake := &fakeUpgradeController{statuses: []controller.Status{before, after, rolled}}
	originals := struct {
		source         func() (string, string, error)
		sourceValidate func(string, string) error
		installed      func(config.Config) (string, error)
		env            func(string) error
		build          func(context.Context, string, string) error
		release        func(string, string) error
		factory        func(config.Config, string) upgradeController
		smoke          func(context.Context, config.Config, string, string) error
		preflight      func(context.Context, config.Config, string) (InspectResult, error)
		remove         func(string) error
	}{sourceRootFn, validateSourceFn, validateInstalledRuntimeFn, validateTunnelEnvFn, buildReleaseFn, validateReleaseFn, newUpgradeControllerFn, smokeFn, preflightFn, removeUpgradeBackup}
	cleanup := func() {
		sourceRootFn, validateSourceFn, validateInstalledRuntimeFn, validateTunnelEnvFn, buildReleaseFn, validateReleaseFn, newUpgradeControllerFn, smokeFn, preflightFn, removeUpgradeBackup = originals.source, originals.sourceValidate, originals.installed, originals.env, originals.build, originals.release, originals.factory, originals.smoke, originals.preflight, originals.remove
	}
	sourceRootFn = func() (string, string, error) { return root, strings.Repeat("a", 40), nil }
	validateSourceFn = func(string, string) error { return nil }
	validateInstalledRuntimeFn = func(config.Config) (string, error) { return "0.2.2", nil }
	validateTunnelEnvFn = func(string) error { return nil }
	buildReleaseFn = func(_ context.Context, _ string, release string) error {
		for _, name := range binaryOrder {
			if err := os.WriteFile(filepath.Join(release, name), []byte("#!/bin/sh\nprintf '0.2.3\\n'\n"), 0o700); err != nil {
				return err
			}
		}
		return nil
	}
	validateReleaseFn = func(string, string) error { return nil }
	newUpgradeControllerFn = func(config.Config, string) upgradeController { return fake }
	preflightFn = func(context.Context, config.Config, string) (InspectResult, error) {
		return InspectResult{Status: "ready"}, nil
	}
	return c, configPath, envPath, fake, cleanup
}

func integrationMCPServer(t *testing.T, c *config.Config) (*httptest.Server, *string) {
	t.Helper()
	version := "0.2.3"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		_ = json.NewDecoder(r.Body).Decode(&request)
		id := request["id"]
		method, _ := request["method"].(string)
		result := map[string]any{}
		switch method {
		case "initialize":
			result = map[string]any{"protocolVersion": "2025-03-26", "serverInfo": map[string]any{"version": version}}
		case "tools/list":
			result = map[string]any{"tools": []any{map[string]any{"name": "system_ping", "inputSchema": map[string]any{"type": "object"}, "outputSchema": map[string]any{"type": "object"}, "annotations": map[string]any{"readOnlyHint": true, "destructiveHint": false, "idempotentHint": true, "openWorldHint": false}}}}
		case "tools/call":
			params, _ := request["params"].(map[string]any)
			name, _ := params["name"].(string)
			if name == "system_ping" {
				result = map[string]any{"isError": false, "structuredContent": map[string]any{"service": "gpt-tunnel-gatewayd", "version": version, "gateway_id": c.GatewayID}}
			} else {
				result = map[string]any{"isError": false, "structuredContent": map[string]any{"gateway_id": c.GatewayID, "hub_protocol_root": "gpt-tunnel/v1", "hub_branch": c.Hub.Branch, "hub_managed_root": filepath.Join(c.StateDir, "hub", "repository")}}
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	}))
	c.ListenAddr = strings.TrimPrefix(server.URL, "http://")
	return server, &version
}

func makeUpgradeFixtures(t *testing.T) (string, map[string]string, map[string][]byte) {
	t.Helper()
	release, paths, old := t.TempDir(), map[string]string{}, map[string][]byte{}
	dest := t.TempDir()
	for _, name := range binaryOrder {
		src := filepath.Join(release, name)
		if err := os.WriteFile(src, []byte("#!/bin/sh\nprintf '0.2.3\\n'\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dest, name)
		data := []byte("old-" + name)
		if err := os.WriteFile(path, data, 0o700); err != nil {
			t.Fatal(err)
		}
		paths[name], old[name] = path, data
	}
	return release, paths, old
}
