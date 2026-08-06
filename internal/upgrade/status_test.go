package upgrade

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
)

func TestStatusNoHistory(t *testing.T) {
	result, err := Status(config.Config{StateDir: t.TempDir()})
	if err != nil || result.Status != "no_history" {
		t.Fatalf("status=%#v err=%v", result, err)
	}
}

func TestStatusReturnsLatestDurableTransactionWithoutUnsafeFields(t *testing.T) {
	c := config.Config{StateDir: t.TempDir()}
	tx, err := newTransaction(c, "0.6.0", "0.6.2", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	tx.FinalStatus = "UPGRADE_ROLLED_BACK"
	tx.PrimaryError = "readiness timeout for http://127.0.0.1:8765/readyz"
	tx.BackupPath = "/unsafe/private/backup"
	if err := writeTransaction(c, tx); err != nil {
		t.Fatal(err)
	}
	result, err := Status(c)
	if err != nil || result.Status != "available" || result.TransactionID != tx.TransactionID || result.ErrorClass != "transaction_error" {
		t.Fatalf("status=%#v err=%v", result, err)
	}
	if result.TransactionID == "" {
		t.Fatal("status omitted durable transaction identity/error")
	}
	tx.TargetStartup = &TargetStartupDiagnostics{Phase: "TARGET_STARTUP", CaptureStatus: "captured", TargetPID: 42, TargetProcessRunning: true, AliveButUnready: true, ElapsedMilliseconds: 30000, Error: "dial http://127.0.0.1:8765/readyz", LogDelta: "config=/home/secret/config.json CONTROL_PLANE_API_KEY=secret-value"}
	if err := writeTransaction(c, tx); err != nil {
		t.Fatal(err)
	}
	result, err = Status(c)
	if err != nil || result.TargetStartup == nil || result.TargetStartup.ErrorClass != "target_startup_error" {
		t.Fatalf("target startup status=%#v err=%v", result, err)
	}
	serialized, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	output := string(serialized)
	for _, forbidden := range []string{"/unsafe/private/backup", "http://127.0.0.1", "/home/secret/config.json", "CONTROL_PLANE_API_KEY", "secret-value", "dial"} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("serialized status exposed %q: %s", forbidden, output)
		}
	}
}

func TestStatusRejectsCorruptLatestTransaction(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "upgrade-transactions"), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "upgrade-transactions", "upgrade-20260806T121311Z-1.json")
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := Status(config.Config{StateDir: root})
	if err == nil || result.Status != "corrupt" || result.ErrorClass != "history_invalid" {
		t.Fatalf("status=%#v err=%v", result, err)
	}
}

func TestStatusIgnoresNonCanonicalJSONRecords(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "upgrade-transactions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.json"), []byte("not a transaction"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := Status(config.Config{StateDir: root})
	if err != nil || result.Status != "no_history" {
		t.Fatalf("status=%#v err=%v", result, err)
	}
}

func TestStatusRejectsCanonicalSymlinkRecord(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "upgrade-transactions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "outside.json")
	if err := os.WriteFile(target, []byte(`{"transaction_id":"unsafe"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "upgrade-20260806T121311Z-1.json")
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	result, err := Status(config.Config{StateDir: root})
	if err == nil || result.Status != "corrupt" || result.ErrorClass != "history_invalid" {
		t.Fatalf("status=%#v err=%v", result, err)
	}
}

func TestStatusRejectsCanonicalNonRegularRecord(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "upgrade-transactions", "upgrade-20260806T121311Z-2.json")
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	result, err := Status(config.Config{StateDir: root})
	if err == nil || result.Status != "corrupt" || result.ErrorClass != "history_invalid" {
		t.Fatalf("status=%#v err=%v", result, err)
	}
}
