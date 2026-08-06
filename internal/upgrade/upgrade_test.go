package upgrade

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/controller"
	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
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

func TestInspectConfiguredProjectsUsesManagedSnapshotAndRejectsMalformedRegistry(t *testing.T) {
	stateDir := t.TempDir()
	root := t.TempDir()
	c := config.Config{StateDir: stateDir}
	s := service.New(c)
	current := config.EmptyManagedProjectRegistry()
	digest, err := current.Digest()
	if err != nil {
		t.Fatal(err)
	}
	registry := config.ManagedProjectRegistry{SchemaVersion: config.ManagedProjectRegistrySchemaVersion, Revision: 1, Projects: map[string]config.ManagedProjectEntry{"managed": {Root: root, RepositoryURL: filepath.Join(stateDir, "managed.git"), Remote: "origin", DefaultBranch: "main", AirelaySessionKey: "managed_master"}}}
	if _, err := config.WriteManagedProjectRegistry(stateDir, digest, registry); err != nil {
		t.Fatal(err)
	}
	resolution, err := s.EffectiveProjectSnapshot()
	if err != nil {
		t.Fatalf("resolve managed inspection snapshot: %v", err)
	}
	foundManaged := false
	inspectConfiguredProjects(context.Background(), s.Git, resolution.Projects, func(code, project, task, run, path, detail string) {
		if project == "managed" {
			foundManaged = true
		}
	})
	if !foundManaged {
		t.Fatal("upgrade project inspection omitted managed project")
	}
	if err := os.WriteFile(config.ManagedProjectRegistryPath(stateDir), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EffectiveProjectSnapshot(); err == nil {
		t.Fatal("malformed registry was accepted by upgrade inspection boundary")
	}
}

func TestTargetStartupDiagnosticsProjectionCoversFailureStates(t *testing.T) {
	tests := []struct {
		name       string
		input      controller.GatewayStartupDiagnostics
		wantAlive  bool
		wantExited bool
	}{
		{name: "fatal-exit", input: controller.GatewayStartupDiagnostics{Phase: "TARGET_STARTUP", CaptureStatus: "captured", TargetPID: 41, TargetProcessExited: true, Error: fmt.Errorf("fatal: config=/home/private/config.json")}, wantExited: true},
		{name: "alive-unready", input: controller.GatewayStartupDiagnostics{Phase: "TARGET_STARTUP", CaptureStatus: "captured", TargetPID: 42, TargetProcessRunning: true, AliveButUnready: true, Elapsed: 30 * time.Second, Error: fmt.Errorf("readiness timeout")}, wantAlive: true},
		{name: "delayed-ready", input: controller.GatewayStartupDiagnostics{Phase: "TARGET_STARTUP", CaptureStatus: "captured", TargetPID: 43, TargetProcessRunning: true, ReadinessPassed: true, Elapsed: 3 * time.Second}, wantAlive: true},
		{name: "bind-failure", input: controller.GatewayStartupDiagnostics{Phase: "TARGET_STARTUP", CaptureStatus: "failed", TargetPID: 44, TargetProcessExited: true, LogCaptureError: fmt.Errorf("missing log")}, wantExited: true},
		{name: "process-state-unknown", input: controller.GatewayStartupDiagnostics{Phase: "TARGET_STARTUP", CaptureStatus: "captured", TargetPID: 45, ProcessStateError: fmt.Errorf("configured binary missing")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := targetStartupDiagnostics(tt.input)
			if got.Phase != "TARGET_STARTUP" || got.CaptureStatus == "" || got.TargetProcessRunning != tt.wantAlive || got.TargetProcessExited != tt.wantExited {
				t.Fatalf("projection=%#v want_alive=%v want_exited=%v", got, tt.wantAlive, tt.wantExited)
			}
			if got.Error != "" && strings.Contains(got.Error, "/home/private") {
				t.Fatal("diagnostic error exposed a path")
			}
			if tt.name == "process-state-unknown" && got.CaptureStatus != "partial" {
				t.Fatalf("process-state error did not make capture partial: %#v", got)
			}
		})
	}
}

func TestTargetStartupDiagnosticsReSanitizesAndBoundsLogDelta(t *testing.T) {
	in := controller.GatewayStartupDiagnostics{Phase: "TARGET_STARTUP", CaptureStatus: "captured", LogDelta: strings.Repeat("line\n", 5000) + "token=secret-value path=/home/private/config.json"}
	got := targetStartupDiagnostics(in)
	if len(got.LogDelta) > 16<<10 || !strings.HasPrefix(got.LogDelta, "line\n") {
		t.Fatalf("log delta was not bounded on complete lines: len=%d prefix=%q", len(got.LogDelta), got.LogDelta[:min(len(got.LogDelta), 32)])
	}
	if strings.Contains(got.LogDelta, "secret-value") || strings.Contains(got.LogDelta, "/home/private/config.json") {
		t.Fatalf("log delta was not re-sanitized: %q", got.LogDelta)
	}
}

func TestRunnerPersistsTargetDiagnosticsBeforeRollback(t *testing.T) {
	c, configPath, _, fake, cleanup := upgradeIntegrationFixture(t)
	defer cleanup()
	server, version := integrationMCPServer(t, &c)
	defer server.Close()
	fake.startupDiagnostics = controller.GatewayStartupDiagnostics{Phase: "TARGET_STARTUP", CaptureStatus: "captured", TargetPID: 99, TargetProcessExited: true, Elapsed: 2 * time.Second, Error: fmt.Errorf("fatal target startup")}
	fake.startupErr = fmt.Errorf("target startup failed")
	fake.statuses = []controller.Status{fake.statuses[0], fake.statuses[2]}
	fake.index = 0
	originalPersist := persistStartupDiagnosticsFn
	persistStartupDiagnosticsFn = func(c config.Config, tx *UpgradeTransaction) error {
		if fake.stopCalls != 0 || fake.restartCalls != 1 || tx.CurrentPhase != "rollback_pending" || tx.TargetStartup == nil {
			t.Fatalf("diagnostics were not persisted before rollback: stop=%d restart=%d phase=%q startup=%#v", fake.stopCalls, fake.restartCalls, tx.CurrentPhase, tx.TargetStartup)
		}
		return originalPersist(c, tx)
	}
	defer func() { persistStartupDiagnosticsFn = originalPersist }()
	smokeFn = func(ctx context.Context, c config.Config, target, previous string) error {
		*version = target
		return smoke(ctx, c, target, previous)
	}
	result, err := (Runner{Config: c, ConfigPath: configPath, Target: "0.2.3"}).Run(context.Background())
	if err == nil || result.Status != "UPGRADE_ROLLED_BACK" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	entries, readErr := os.ReadDir(filepath.Join(c.StateDir, "upgrade-transactions"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) == 0 {
		t.Fatal("rollback did not leave a durable transaction")
	}
	data, readErr := os.ReadFile(filepath.Join(c.StateDir, "upgrade-transactions", entries[len(entries)-1].Name()))
	if readErr != nil {
		t.Fatal(readErr)
	}
	var tx UpgradeTransaction
	if err := json.Unmarshal(data, &tx); err != nil {
		t.Fatal(err)
	}
	if tx.CurrentPhase != "complete" || tx.TargetStartup == nil || tx.TargetStartup.TargetPID != 99 || tx.TargetStartup.CaptureStatus != "captured" || !tx.TargetStartup.TargetProcessExited {
		t.Fatalf("diagnostics were not persisted before rollback: %#v", tx)
	}
	if fake.stopCalls != 1 || fake.restartCalls != 2 {
		t.Fatalf("rollback started with unexpected controller calls: stop=%d restart=%d", fake.stopCalls, fake.restartCalls)
	}
}

func TestRunnerRollsBackWhenTargetDiagnosticPersistenceFails(t *testing.T) {
	c, configPath, _, fake, cleanup := upgradeIntegrationFixture(t)
	defer cleanup()
	server, version := integrationMCPServer(t, &c)
	defer server.Close()
	fake.startupDiagnostics = controller.GatewayStartupDiagnostics{Phase: "TARGET_STARTUP", CaptureStatus: "failed", TargetPID: 100, TargetProcessExited: true}
	fake.startupErr = fmt.Errorf("target startup failed")
	fake.statuses = []controller.Status{fake.statuses[0], fake.statuses[2]}
	fake.index = 0
	original := persistStartupDiagnosticsFn
	persistStartupDiagnosticsFn = func(config.Config, *UpgradeTransaction) error {
		return fmt.Errorf("diagnostic transaction write failed")
	}
	defer func() { persistStartupDiagnosticsFn = original }()
	smokeFn = func(ctx context.Context, c config.Config, target, previous string) error {
		*version = target
		return smoke(ctx, c, target, previous)
	}
	result, err := (Runner{Config: c, ConfigPath: configPath, Target: "0.2.3"}).Run(context.Background())
	if err == nil || result.Status != "UPGRADE_ROLLED_BACK" || fake.stopCalls != 1 {
		t.Fatalf("write failure did not preserve rollback: result=%#v err=%v controller=%#v", result, err, fake)
	}
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

func TestRunnerRunSuccessProofClosure(t *testing.T) {
	c, configPath, _, fake, cleanup := upgradeIntegrationFixture(t)
	defer cleanup()
	server, version := integrationMCPServer(t, &c)
	defer server.Close()
	protected := map[string]string{}
	for _, path := range []string{configPath, c.Controller.TunnelEnvFile, c.Controller.TunnelClientBinary} {
		protected[path], _ = fileHash(path)
	}
	smokeFn = func(ctx context.Context, c config.Config, target, previous string) error {
		if target != "0.2.3" || previous != "0.2.2" {
			t.Fatalf("unexpected success versions: %s/%s", target, previous)
		}
		*version = target
		return smoke(ctx, c, target, previous)
	}
	r := Runner{Config: c, ConfigPath: configPath, Target: "0.2.3"}
	result, err := r.Run(context.Background())
	if err != nil || result.Status != "UPGRADE_COMPLETE" || result.GatewayPID != 11 || result.TunnelPID != 20 {
		t.Fatalf("success result=%#v err=%v", result, err)
	}
	transactionData, err := os.ReadFile(transactionPath(c, result.TransactionID))
	if err != nil {
		t.Fatal(err)
	}
	var transaction UpgradeTransaction
	if err := json.Unmarshal(transactionData, &transaction); err != nil {
		t.Fatal(err)
	}
	if transaction.CurrentPhase != "complete" || transaction.FinalStatus != "UPGRADE_COMPLETE" || len(transaction.MigrationOperations) == 0 || transaction.GatewayPIDBefore != 10 || transaction.GatewayPIDAfter != 11 || transaction.TunnelPIDBefore != 20 || transaction.TunnelPIDAfter != 20 {
		t.Fatalf("incomplete durable transaction: %#v", transaction)
	}
	if fake.doctorCalls != 1 || fake.restartCalls != 1 || fake.stopCalls != 0 {
		t.Fatalf("controller calls: %#v", fake)
	}
	for _, name := range binaryOrder {
		if v, err := installedVersion(filepath.Join(filepath.Dir(c.Controller.GatewayBinary), name)); err != nil || v != "0.2.3" {
			t.Fatalf("target %s: %s %v", name, v, err)
		}
	}
	for path, want := range protected {
		got, _ := fileHash(path)
		if got != want {
			t.Fatalf("protected file changed: %s", path)
		}
	}
	entries, _ := os.ReadDir(filepath.Join(c.Controller.PIDDir, "upgrades"))
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "backup-") {
			t.Fatalf("successful upgrade retained backup")
		}
	}
}

func TestRunnerRunSuccessfulRollbackProofClosure(t *testing.T) {
	c, configPath, _, fake, cleanup := upgradeIntegrationFixture(t)
	defer cleanup()
	server, version := integrationMCPServer(t, &c)
	defer server.Close()
	protected := map[string]string{}
	for _, path := range []string{configPath, c.Controller.TunnelEnvFile, c.Controller.TunnelClientBinary} {
		protected[path], _ = fileHash(path)
	}
	calls := 0
	smokeFn = func(ctx context.Context, c config.Config, target, previous string) error {
		calls++
		if calls == 1 {
			if target != "0.2.3" {
				t.Fatal("target smoke version mismatch")
			}
			return fmt.Errorf("target smoke failure")
		}
		if target != "0.2.2" || previous != "0.2.3" {
			t.Fatalf("unexpected rollback versions: %s/%s", target, previous)
		}
		*version = target
		return smoke(ctx, c, target, previous)
	}
	r := Runner{Config: c, ConfigPath: configPath, Target: "0.2.3"}
	result, err := r.Run(context.Background())
	if err == nil || result.Status != "UPGRADE_ROLLED_BACK" || result.GatewayPID != 12 || result.TunnelPID != 20 {
		t.Fatalf("rollback result=%#v err=%v", result, err)
	}
	if fake.doctorCalls != 2 || fake.restartCalls != 2 || fake.stopCalls != 1 {
		t.Fatalf("controller calls: %#v", fake)
	}
	for _, name := range binaryOrder {
		if v, err := installedVersion(filepath.Join(filepath.Dir(c.Controller.GatewayBinary), name)); err != nil || v != "0.2.2" {
			t.Fatalf("restored %s: %s %v", name, v, err)
		}
	}
	for path, want := range protected {
		got, _ := fileHash(path)
		if got != want {
			t.Fatalf("protected file changed: %s", path)
		}
	}
	entries, _ := os.ReadDir(filepath.Join(c.Controller.PIDDir, "upgrades"))
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "backup-") {
			t.Fatalf("successful rollback retained backup")
		}
	}
}

func TestRunnerRunRollbackCleanupFailureRetainsBackup(t *testing.T) {
	c, configPath, _, _, cleanup := upgradeIntegrationFixture(t)
	defer cleanup()
	server, version := integrationMCPServer(t, &c)
	defer server.Close()
	calls := 0
	smokeFn = func(ctx context.Context, c config.Config, target, previous string) error {
		calls++
		if calls == 1 {
			return fmt.Errorf("target smoke failure")
		}
		*version = target
		return smoke(ctx, c, target, previous)
	}
	originalRemove := removeUpgradeBackup
	removeUpgradeBackup = func(string) error { return os.ErrPermission }
	defer func() { removeUpgradeBackup = originalRemove }()
	r := Runner{Config: c, ConfigPath: configPath, Target: "0.2.3"}
	result, err := r.Run(context.Background())
	if err == nil || result.Status != "UPGRADE_ROLLBACK_FAILED" {
		t.Fatalf("cleanup failure result=%#v err=%v", result, err)
	}
	entries, _ := os.ReadDir(filepath.Join(c.Controller.PIDDir, "upgrades"))
	found := false
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "backup-") {
			found = true
		}
	}
	if !found {
		t.Fatal("rollback cleanup failure did not retain backup")
	}
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

func TestReplaceAllStagesBeforeCommitAndRestoresCommitFailure(t *testing.T) {
	release, paths, old := makeUpgradeFixtures(t)
	originalRename, originalCopy := stageRename, stageCopy
	t.Cleanup(func() { stageRename, stageCopy = originalRename, originalCopy })
	commits := 0
	stageRename = func(src, dst string) error {
		commits++
		if commits == 2 {
			return os.ErrPermission
		}
		return os.Rename(src, dst)
	}
	if err := replaceAll(release, paths, old); err == nil {
		t.Fatal("commit failure accepted")
	}
	for _, name := range binaryOrder {
		got, err := os.ReadFile(paths[name])
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(old[name]) {
			t.Fatalf("%s was not restored", name)
		}
	}
}

func TestReplaceAllSucceedsAndCoversEveryStagePosition(t *testing.T) {
	for _, position := range binaryOrder {
		t.Run(position, func(t *testing.T) {
			release, paths, old := makeUpgradeFixtures(t)
			originalCopy := stageCopy
			t.Cleanup(func() { stageCopy = originalCopy })
			calls := 0
			stageCopy = func(src, dst string) (string, error) {
				calls++
				if binaryOrder[calls-1] == position {
					return "", os.ErrPermission
				}
				return stageOne(src, dst)
			}
			if err := replaceAll(release, paths, old); err == nil {
				t.Fatal("stage failure accepted")
			}
			for _, name := range binaryOrder {
				got, _ := os.ReadFile(paths[name])
				if string(got) != string(old[name]) {
					t.Fatalf("%s changed", name)
				}
			}
		})
	}
	// A normal transaction commits all three staged files in deterministic order.
	release, paths, old := makeUpgradeFixtures(t)
	if err := replaceAll(release, paths, old); err != nil {
		t.Fatal(err)
	}
	for _, name := range binaryOrder {
		got, _ := os.ReadFile(paths[name])
		if string(got) != "#!/bin/sh\nprintf '0.2.3\\n'\n" {
			t.Fatalf("%s was not committed", name)
		}
	}
}

func TestReplaceAllRenameFailureAfterEachCommitRestoresAll(t *testing.T) {
	for failure := 1; failure <= len(binaryOrder); failure++ {
		t.Run(fmt.Sprintf("rename-%d", failure), func(t *testing.T) {
			release, paths, old := makeUpgradeFixtures(t)
			originalRename := stageRename
			t.Cleanup(func() { stageRename = originalRename })
			calls := 0
			stageRename = func(src, dst string) error {
				calls++
				if calls == failure {
					return os.ErrPermission
				}
				return os.Rename(src, dst)
			}
			if err := replaceAll(release, paths, old); err == nil {
				t.Fatal("rename failure accepted")
			}
			for _, name := range binaryOrder {
				got, _ := os.ReadFile(paths[name])
				if string(got) != string(old[name]) {
					t.Fatalf("%s was not restored", name)
				}
			}
		})
	}
}

func TestReplaceAllDirectorySyncFailureCleansStaging(t *testing.T) {
	release, paths, old := makeUpgradeFixtures(t)
	originalSync := stageSyncDir
	t.Cleanup(func() { stageSyncDir = originalSync })
	stageSyncDir = func(string) error { return os.ErrPermission }
	if err := replaceAll(release, paths, old); err == nil {
		t.Fatal("sync failure accepted")
	}
	for _, name := range binaryOrder {
		got, _ := os.ReadFile(paths[name])
		if string(got) != string(old[name]) {
			t.Fatalf("%s was not restored", name)
		}
	}
	entries, _ := os.ReadDir(filepath.Dir(paths["gpt-tunnel"]))
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".gpt-tunnel-upgrade-stage-") {
			t.Fatalf("staging file remains: %s", entry.Name())
		}
	}
}

func TestReplaceAllPropagatesStagingCleanupFailure(t *testing.T) {
	release, paths, old := makeUpgradeFixtures(t)
	originalCopy, originalRemove := stageCopy, stageRemove
	t.Cleanup(func() { stageCopy, stageRemove = originalCopy, originalRemove })
	calls := 0
	stageCopy = func(src, dst string) (string, error) {
		calls++
		if calls == 1 {
			return stageOne(src, dst)
		}
		return "", os.ErrPermission
	}
	stageRemove = func(string) error { return os.ErrPermission }
	if err := replaceAll(release, paths, old); err == nil || !strings.Contains(err.Error(), "cleanup") {
		t.Fatalf("cleanup failure not propagated: %v", err)
	}
}

func TestRollbackBackupCleanupPolicy(t *testing.T) {
	dir := t.TempDir()
	original := removeUpgradeBackup
	t.Cleanup(func() { removeUpgradeBackup = original })
	if err := os.WriteFile(filepath.Join(dir, "binary"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cleanupRollbackBackup(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("successful rollback retained backup: %v", err)
	}

	dir = t.TempDir()
	removeUpgradeBackup = func(string) error { return os.ErrPermission }
	if err := cleanupRollbackBackup(dir); err == nil {
		t.Fatal("cleanup failure not reported")
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("failed cleanup did not retain backup: %v", err)
	}
}

func TestTunnelClientOwnershipRejection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tunnel-client")
	if err := os.WriteFile(path, []byte("x"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(path, os.Getuid()+1, -1); err != nil {
		t.Skipf("cannot create foreign-owned fixture: %v", err)
	}
	if err := validateOwnedExecutable(path, "tunnel-client"); err == nil {
		t.Fatal("foreign-owned tunnel-client accepted")
	}
}

func TestUpgradeLockContentionAndReacquisition(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "upgrades")
	first, err := lockfile.Acquire(dir, "upgrade")
	if err != nil {
		t.Fatal(err)
	}
	if second, err := lockfile.Acquire(dir, "upgrade"); err == nil {
		_ = second.Release()
		t.Fatal("second upgrade acquisition succeeded while held")
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	second, err := lockfile.Acquire(dir, "upgrade")
	if err != nil {
		t.Fatalf("upgrade lock did not reacquire: %v", err)
	}
	if err := second.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestReplaceAllStageFailureCleansStaging(t *testing.T) {
	release, paths, old := makeUpgradeFixtures(t)
	originalCopy := stageCopy
	t.Cleanup(func() { stageCopy = originalCopy })
	calls := 0
	stageCopy = func(src, dst string) (string, error) {
		calls++
		if calls == 2 {
			return "", os.ErrPermission
		}
		return stageOne(src, dst)
	}
	if err := replaceAll(release, paths, old); err == nil {
		t.Fatal("stage failure accepted")
	}
	entries, err := os.ReadDir(filepath.Dir(paths["gpt-tunnel"]))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if filepath.Base(entry.Name()) != filepath.Base(paths["gpt-tunnel-gatewayd"]) && filepath.Base(entry.Name()) != filepath.Base(paths["gpt-tunnel"]) && filepath.Base(entry.Name()) != filepath.Base(paths["gpt-tunnelctl"]) {
			if len(entry.Name()) > 0 && entry.Name()[0] == '.' {
				t.Fatalf("staging file remains: %s", entry.Name())
			}
		}
	}
}

func TestVersionOrderingRejectsSameAndDowngrade(t *testing.T) {
	if compareVersion("0.2.3", "0.2.3") != 0 {
		t.Fatal("same version must compare equal")
	}
	if compareVersion("0.2.2", "0.2.3") >= 0 {
		t.Fatal("downgrade must compare lower")
	}
	if compareVersion("0.2.4", "0.2.3") <= 0 {
		t.Fatal("upgrade must compare higher")
	}
}

func TestValidateReleaseRequiresExactArtifactsAndChecksums(t *testing.T) {
	dir := t.TempDir()
	names := []string{"gpt-tunnel", "gpt-tunnel-gatewayd", "gpt-tunnelctl"}
	lines := ""
	for _, name := range names {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf '0.2.3\\n'\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		data, _ := os.ReadFile(path)
		sum := sha256.Sum256(data)
		lines += hex.EncodeToString(sum[:]) + "  " + name + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateRelease(dir, "0.2.3"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "unexpected"), []byte("x"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := validateRelease(dir, "0.2.3"); err == nil {
		t.Fatal("unexpected release artifact accepted")
	}
}

func TestParseVersionRejectsNonCanonical(t *testing.T) {
	for _, value := range []string{"v0.2.3", "0.2", "0.2.3-beta", "01.2.3"} {
		if _, err := parseVersion(value); err == nil {
			t.Fatalf("accepted invalid version %q", value)
		}
	}
	if got, err := parseVersion("0.2.3"); err != nil || got != "0.2.3" {
		t.Fatalf("canonical version parse failed: %q %v", got, err)
	}
}

func TestValidateReleaseRejectsDuplicateAndTraversalManifest(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"gpt-tunnel", "gpt-tunnel-gatewayd", "gpt-tunnelctl"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\nprintf '0.2.3\\n'\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, "gpt-tunnel"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	line := hex.EncodeToString(sum[:]) + "  gpt-tunnel\n"
	if err := os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(line+line), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateRelease(dir, "0.2.3"); err == nil {
		t.Fatal("duplicate manifest accepted")
	}
	if err := os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(line+strings.Repeat("0", 64)+"  ../gpt-tunnel-gatewayd\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateRelease(dir, "0.2.3"); err == nil {
		t.Fatal("traversal manifest accepted")
	}
}

func TestSmokeTimeoutIsBounded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(250 * time.Millisecond)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
	}))
	t.Cleanup(server.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	addr := strings.TrimPrefix(server.URL, "http://")
	c := config.Config{ListenAddr: addr, GatewayID: "home", StateDir: t.TempDir(), Hub: config.HubConfig{Branch: "gpt-tunnel/home"}}
	start := time.Now()
	if err := smoke(ctx, c, "0.2.3", "0.2.2"); err == nil {
		t.Fatal("timeout server accepted")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("smoke exceeded timeout bound")
	}
}

func TestSmokeRejectsMalformedJSONRPCAndToolContracts(t *testing.T) {
	validInit := `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-03-26","serverInfo":{"version":"0.2.3"}}}`
	cases := []struct{ name, body string }{
		{"jsonrpc-version", `{"jsonrpc":"1.0","id":1,"result":{}}`},
		{"mismatched-id", `{"jsonrpc":"2.0","id":9,"result":{}}`},
		{"top-level-error", `{"jsonrpc":"2.0","id":1,"error":{"code":-1}}`},
		{"missing-result", `{"jsonrpc":"2.0","id":1}`},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(test.body)) }))
			defer server.Close()
			c := config.Config{ListenAddr: strings.TrimPrefix(server.URL, "http://"), GatewayID: "home", StateDir: t.TempDir(), Hub: config.HubConfig{Branch: "gpt-tunnel/home"}}
			if err := smoke(context.Background(), c, "0.2.3", "0.2.2"); err == nil {
				t.Fatal("malformed response accepted")
			}
		})
	}
	validTool := map[string]any{"name": "system_ping", "inputSchema": map[string]any{"type": "object"}, "outputSchema": map[string]any{"type": "object"}, "annotations": map[string]any{"readOnlyHint": true, "destructiveHint": false, "idempotentHint": true, "openWorldHint": false}}
	validList := map[string]any{"tools": []any{validTool}}
	contractCases := []struct {
		name                       string
		list                       map[string]any
		pingError, capabilityError bool
	}{
		{"malformed-tool-descriptor", map[string]any{"tools": []any{"bad"}}, false, false},
		{"missing-tool-name", map[string]any{"tools": []any{map[string]any{"inputSchema": map[string]any{"type": "object"}, "outputSchema": map[string]any{"type": "object"}, "annotations": validTool["annotations"]}}}, false, false},
		{"empty-tool-name", map[string]any{"tools": []any{map[string]any{"name": "", "inputSchema": map[string]any{"type": "object"}, "outputSchema": map[string]any{"type": "object"}, "annotations": validTool["annotations"]}}}, false, false},
		{"invalid-input-schema", map[string]any{"tools": []any{map[string]any{"name": "system_ping", "inputSchema": map[string]any{"type": "array"}, "outputSchema": map[string]any{"type": "object"}, "annotations": validTool["annotations"]}}}, false, false},
		{"invalid-output-schema", map[string]any{"tools": []any{map[string]any{"name": "system_ping", "inputSchema": map[string]any{"type": "object"}, "outputSchema": map[string]any{"type": "array"}, "annotations": validTool["annotations"]}}}, false, false},
		{"invalid-annotations", map[string]any{"tools": []any{map[string]any{"name": "system_ping", "inputSchema": map[string]any{"type": "object"}, "outputSchema": map[string]any{"type": "object"}, "annotations": map[string]any{"readOnlyHint": "yes"}}}}, false, false},
		{"ping-error-result", validList, true, false},
		{"capability-error-result", validList, false, true},
	}
	for _, test := range contractCases {
		t.Run(test.name, func(t *testing.T) {
			count := 0
			stateDir := t.TempDir()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				count++
				body := validInit
				if count == 2 {
					payload := test.list
					bodyBytes, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 2, "result": payload})
					body = string(bodyBytes)
				} else if count == 3 {
					ping := map[string]any{"isError": false, "structuredContent": map[string]any{"service": "gpt-tunnel-gatewayd", "version": "0.2.3", "gateway_id": "home"}}
					if test.pingError {
						ping["isError"] = true
					}
					bodyBytes, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 3, "result": ping})
					body = string(bodyBytes)
				} else if count == 4 {
					capability := map[string]any{"isError": false, "structuredContent": map[string]any{"gateway_id": "home", "hub_protocol_root": "gpt-tunnel/v1", "hub_branch": "gpt-tunnel/home", "hub_managed_root": filepath.Join(stateDir, "hub", "repository")}}
					if test.capabilityError {
						capability["isError"] = true
					}
					bodyBytes, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 4, "result": capability})
					body = string(bodyBytes)
				}
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()
			c := config.Config{ListenAddr: strings.TrimPrefix(server.URL, "http://"), GatewayID: "home", StateDir: stateDir, Hub: config.HubConfig{Branch: "gpt-tunnel/home"}}
			if err := smoke(context.Background(), c, "0.2.3", "0.2.2"); err == nil {
				t.Fatal("invalid contract accepted")
			}
		})
	}
}
