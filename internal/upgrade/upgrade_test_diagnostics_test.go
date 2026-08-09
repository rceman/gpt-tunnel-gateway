package upgrade

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/controller"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

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
	result, err := (Runner{
		Config:     c,
		ConfigPath: configPath,
		Target:     "0.2.3",
	}).Run(context.Background())
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
	result, err := (Runner{
		Config:     c,
		ConfigPath: configPath,
		Target:     "0.2.3",
	}).Run(context.Background())
	if err == nil || result.Status != "UPGRADE_ROLLED_BACK" || fake.stopCalls != 1 {
		t.Fatalf("write failure did not preserve rollback: result=%#v err=%v controller=%#v", result, err, fake)
	}
}
