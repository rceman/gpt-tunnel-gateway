package upgrade

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/activation"
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
)

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
	smokeFn = func(ctx context.Context, c config.Config, target string) error {
		calls++
		if calls == 1 {
			if target != "0.2.3" {
				t.Fatal("target smoke version mismatch")
			}
			return fmt.Errorf("target smoke failure")
		}
		if target != "0.2.2" {
			t.Fatalf("unexpected rollback version: %s", target)
		}
		*version = target
		return activation.LiveMCPSmoke(ctx, c, target)
	}
	r := Runner{
		Config:     c,
		ConfigPath: configPath,
		Target:     "0.2.3",
	}
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
	smokeFn = func(ctx context.Context, c config.Config, target string) error {
		calls++
		if calls == 1 {
			return fmt.Errorf("target smoke failure")
		}
		*version = target
		return activation.LiveMCPSmoke(ctx, c, target)
	}
	originalRemove := removeUpgradeBackup
	removeUpgradeBackup = func(string) error { return os.ErrPermission }
	defer func() { removeUpgradeBackup = originalRemove }()
	r := Runner{
		Config:     c,
		ConfigPath: configPath,
		Target:     "0.2.3",
	}
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
