package upgrade

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
)

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
	r := Runner{
		Config:     c,
		ConfigPath: configPath,
		Target:     "0.2.3",
	}
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
