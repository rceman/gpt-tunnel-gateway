package upgrade

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
)

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
