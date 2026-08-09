package controller

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
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
	lock, err := lockfile.Acquire(c.Config.Controller.PIDDir, "controller")
	if err != nil {
		return err
	}
	defer lock.Release()
	expected, _ := filepath.EvalSymlinks(c.Config.Controller.GatewayBinary)
	running := c.process("gateway", expected)
	backup := ""
	if running.Running {
		backup = filepath.Join(c.Config.Controller.PIDDir, fmt.Sprintf("gateway.rollback.%d", running.PID))
		if err := copyExecutable(filepath.Join("/proc", strconv.Itoa(running.PID), "exe"), backup); err != nil {
			return fmt.Errorf("snapshot running gateway: %w", err)
		}
		defer os.Remove(backup)
	}
	if err := c.stopProcess("gateway", c.Config.Controller.GatewayBinary); err != nil {
		return err
	}
	startErr := c.startProcess("gateway", c.Config.Controller.GatewayBinary, []string{"--config", c.ConfigPath}, []string{"GPT_TUNNEL_CONFIG=" + c.ConfigPath})
	if startErr == nil {
		startErr = waitURL(c.gatewayReadyURL(), true, 30*time.Second)
	}
	if startErr == nil {
		return nil
	}
	_ = c.stopProcess("gateway", c.Config.Controller.GatewayBinary)
	if backup == "" {
		return startErr
	}
	if err := copyExecutable(backup, c.Config.Controller.GatewayBinary); err != nil {
		return fmt.Errorf("gateway restart failed (%v); rollback restore failed: %w", startErr, err)
	}
	if err := c.startProcess("gateway", c.Config.Controller.GatewayBinary, []string{"--config", c.ConfigPath}, []string{"GPT_TUNNEL_CONFIG=" + c.ConfigPath}); err != nil {
		return fmt.Errorf("gateway restart failed (%v); rollback start failed: %w", startErr, err)
	}
	if err := waitURL(c.gatewayReadyURL(), true, 30*time.Second); err != nil {
		return fmt.Errorf("gateway restart failed (%v); rollback readiness failed: %w", startErr, err)
	}
	return fmt.Errorf("gateway restart failed and previous executable was restored: %w", startErr)
}

// RestartGatewayAfterUpgrade stops the exact gateway recorded by the controller
// and starts the currently installed binary. It intentionally does not touch the
// tunnel process; callers own rollback of the installed gateway binary.
func (c Controller) RestartGatewayAfterUpgrade() error {
	_, err := c.RestartGatewayAfterUpgradeDiagnostics()
	return err
}
