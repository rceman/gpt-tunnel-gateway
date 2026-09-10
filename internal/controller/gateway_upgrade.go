package controller

import (
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
)

func copyExecutable(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.CreateTemp(filepath.Dir(dst), ".gateway-backup-*")
	if err != nil {
		return err
	}
	tmp := out.Name()
	defer os.Remove(tmp)
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Chmod(0o755); err != nil {
		out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}
func (c Controller) RestartGateway() error {
	_, err := c.RestartGatewayRecovery("")
	return err
}

// RestartGatewayAfterUpgrade stops the exact gateway recorded by the controller
// and starts the currently installed binary. It intentionally does not touch the
// tunnel process; callers own rollback of the installed gateway binary.
func (c Controller) RestartGatewayAfterUpgrade() error {
	_, err := c.RestartGatewayOnlyAfterUpgradeDiagnostics()
	return err
}

// StopGatewayOnly stops only the controller-owned Gateway process. The Tunnel
// process is deliberately outside this handoff.
func (c Controller) StopGatewayOnly() error {
	lock, err := lockfile.Acquire(c.Config.Controller.PIDDir, "controller")
	if err != nil {
		return err
	}
	defer lock.Release()
	return c.stopProcess("gateway", c.Config.Controller.GatewayBinary)
}

// StartGatewayOnly starts the controller-owned Gateway and waits for its
// readiness endpoint. It never starts or inspects the Tunnel process.
func (c Controller) StartGatewayOnly() error {
	lock, err := lockfile.Acquire(c.Config.Controller.PIDDir, "controller")
	if err != nil {
		return err
	}
	defer lock.Release()
	if err := restartGatewayStartFn(c); err != nil {
		return err
	}
	if err := restartGatewayWaitFn(c.gatewayReadyURL(), true, 30*time.Second); err != nil {
		_ = c.stopProcess("gateway", c.Config.Controller.GatewayBinary)
		return err
	}
	c.processEvent("gateway", c.Config.Controller.GatewayBinary, "info", "process_ready", c.process("gateway", c.Config.Controller.GatewayBinary).PID, "gateway ready", nil)
	return nil
}

// RestartGatewayOnlyAfterUpgradeDiagnostics is the explicit Gateway-only
// restart authority used by machine handoffs and artifact upgrades.
func (c Controller) RestartGatewayOnlyAfterUpgradeDiagnostics() (GatewayStartupDiagnostics, error) {
	return c.RestartGatewayAfterUpgradeDiagnostics()
}
