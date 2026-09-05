package controller

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
)

const daemonUnitName = "gpt-tunnel.service"

type DaemonUnitStatus struct {
	Installed bool   `json:"installed"`
	Enabled   bool   `json:"enabled"`
	Active    bool   `json:"active"`
	State     string `json:"state,omitempty"`
	UnitPath  string `json:"unit_path"`
}

type DaemonStatus struct {
	Unit    DaemonUnitStatus `json:"unit"`
	Runtime Status           `json:"runtime"`
}

func daemonUnitPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", fmt.Errorf("home directory unavailable")
	}
	return filepath.Join(home, ".config", "systemd", "user", daemonUnitName), nil
}

func systemctl(ctx context.Context, args ...string) (string, error) {
	if runtime.GOOS != "linux" {
		return "", fmt.Errorf("daemon lifecycle is Linux/systemd-only")
	}
	command := exec.CommandContext(ctx, "systemctl", append([]string{"--user"}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}
		return "", fmt.Errorf("systemctl %s: %s", strings.Join(args, " "), message)
	}
	return strings.TrimSpace(string(output)), nil
}

func (c Controller) daemonUnitStatus(ctx context.Context) (DaemonUnitStatus, error) {
	path, err := daemonUnitPath()
	if err != nil {
		return DaemonUnitStatus{}, err
	}
	status := DaemonUnitStatus{UnitPath: path}
	if _, err := os.Stat(path); err == nil {
		status.Installed = true
	} else if !os.IsNotExist(err) {
		return status, err
	}
	if !status.Installed {
		return status, nil
	}
	enabled, enabledErr := systemctl(ctx, "is-enabled", daemonUnitName)
	status.Enabled = enabledErr == nil && enabled == "enabled"
	active, activeErr := systemctl(ctx, "is-active", daemonUnitName)
	if activeErr == nil {
		status.State = active
		status.Active = active == "active"
	}
	return status, nil
}

func (c Controller) DaemonStatus(ctx context.Context) (DaemonStatus, error) {
	unit, err := c.daemonUnitStatus(ctx)
	if err != nil {
		return DaemonStatus{}, err
	}
	runtimeStatus, err := c.Status(ctx)
	if err != nil {
		return DaemonStatus{}, err
	}
	return DaemonStatus{Unit: unit, Runtime: runtimeStatus}, nil
}

func (c Controller) DaemonInstall(ctx context.Context) (DaemonStatus, error) {
	path, err := daemonUnitPath()
	if err != nil {
		return DaemonStatus{}, err
	}
	if c.Config.Controller.GatewayBinary == "" || c.ConfigPath == "" {
		return DaemonStatus{}, fmt.Errorf("gateway binary and config path are required")
	}
	if err := fsutil.EnsureDir(filepath.Dir(path), 0o700); err != nil {
		return DaemonStatus{}, err
	}
	if err := os.WriteFile(path, []byte(daemonUnitText(c)), 0o600); err != nil {
		return DaemonStatus{}, err
	}
	if _, err := systemctl(ctx, "daemon-reload"); err != nil {
		return DaemonStatus{}, err
	}
	if _, err := systemctl(ctx, "enable", "--now", daemonUnitName); err != nil {
		return DaemonStatus{}, err
	}
	if err := c.waitDaemonReadiness(ctx); err != nil {
		return DaemonStatus{}, err
	}
	return c.DaemonStatus(ctx)
}

func (c Controller) DaemonRestart(ctx context.Context) (DaemonStatus, error) {
	unit, err := c.daemonUnitStatus(ctx)
	if err != nil {
		return DaemonStatus{}, err
	}
	if !unit.Installed {
		return DaemonStatus{}, fmt.Errorf("DAEMON_NOT_INSTALLED: %s", daemonUnitName)
	}
	if _, err := systemctl(ctx, "restart", daemonUnitName); err != nil {
		return DaemonStatus{}, err
	}
	if err := c.waitDaemonReadiness(ctx); err != nil {
		return DaemonStatus{}, err
	}
	return c.DaemonStatus(ctx)
}

func (c Controller) DaemonRemove(ctx context.Context) error {
	unit, err := c.daemonUnitStatus(ctx)
	if err != nil {
		return err
	}
	if !unit.Installed {
		return nil
	}
	if _, err := systemctl(ctx, "disable", "--now", daemonUnitName); err != nil {
		return err
	}
	if err := os.Remove(unit.UnitPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	_, err = systemctl(ctx, "daemon-reload")
	return err
}

func daemonUnitText(c Controller) string {
	return "[Unit]\nDescription=GPT Tunnel Gateway\nAfter=network-online.target\nWants=network-online.target\n\n[Service]\nType=simple\nExecStart=" + c.Config.Controller.GatewayBinary + " --config " + c.ConfigPath + "\nRestart=no\n\n[Install]\nWantedBy=default.target\n"
}

func (c Controller) waitDaemonReadiness(ctx context.Context) error {
	if err := waitURLContext(ctx, c.gatewayReadyURL(), true); err != nil {
		return fmt.Errorf("gateway readiness failed: %w", err)
	}
	if err := waitURLContext(ctx, c.tunnelReadyURL(), true); err != nil {
		return fmt.Errorf("tunnel readiness failed: %w", err)
	}
	return nil
}

func waitURLContext(ctx context.Context, url string, want bool) error {
	for {
		if checkURL(ctx, url) == want {
			return nil
		}
		if ctx.Err() != nil {
			return fmt.Errorf("readiness timeout for %s", url)
		}
		timer := time.NewTimer(250 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("readiness timeout for %s", url)
		case <-timer.C:
		}
	}
}
