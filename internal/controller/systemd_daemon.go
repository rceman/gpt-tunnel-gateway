package controller

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
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
	if runtime.GOOS != "linux" {
		return "", fmt.Errorf("daemon lifecycle is Linux/systemd-only")
	}
	return filepath.Join(string(filepath.Separator), "etc", "systemd", "system", daemonUnitName), nil
}

func requireSystemInstallPrivileges() error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("daemon lifecycle is Linux/systemd-only")
	}
	if os.Geteuid() != 0 {
		return fmt.Errorf("system daemon installation requires root privileges")
	}
	return nil
}

func systemctl(ctx context.Context, args ...string) (string, error) {
	if runtime.GOOS != "linux" {
		return "", fmt.Errorf("daemon lifecycle is Linux/systemd-only")
	}
	command := exec.CommandContext(ctx, "systemctl", args...)
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
	state, stateErr := systemctl(ctx, "show", "--property=ActiveState", "--value", daemonUnitName)
	if stateErr == nil {
		status.State = state
		status.Active = state == "active" || state == "activating"
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
	if err := requireSystemInstallPrivileges(); err != nil {
		return DaemonStatus{}, err
	}
	path, err := daemonUnitPath()
	if err != nil {
		return DaemonStatus{}, err
	}
	if c.Config.Controller.GatewayBinary == "" || c.Config.Controller.TunnelClientBinary == "" || c.ConfigPath == "" {
		return DaemonStatus{}, fmt.Errorf("gateway binary, tunnel client binary, and config path are required")
	}
	identity, err := daemonRuntimeUser(c.ConfigPath)
	if err != nil {
		return DaemonStatus{}, err
	}
	if err := fsutil.EnsureDir(filepath.Dir(path), 0o755); err != nil {
		return DaemonStatus{}, err
	}
	text, err := c.daemonUnitText(identity)
	if err != nil {
		return DaemonStatus{}, err
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		return DaemonStatus{}, err
	}
	if _, err := systemctl(ctx, "daemon-reload"); err != nil {
		return DaemonStatus{}, err
	}
	if _, err := systemctl(ctx, "enable", "--now", "--no-block", daemonUnitName); err != nil {
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
	if _, err := systemctl(ctx, "restart", "--no-block", daemonUnitName); err != nil {
		return DaemonStatus{}, err
	}
	if err := c.waitDaemonReadiness(ctx); err != nil {
		return DaemonStatus{}, err
	}
	return c.DaemonStatus(ctx)
}

func (c Controller) DaemonRemove(ctx context.Context) error {
	if err := requireSystemInstallPrivileges(); err != nil {
		return err
	}
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

type daemonRuntimeIdentity struct {
	User  string
	Group string
}

func daemonRuntimeUser(configPath string) (daemonRuntimeIdentity, error) {
	info, err := os.Stat(configPath)
	if err != nil {
		return daemonRuntimeIdentity{}, fmt.Errorf("stat config for daemon owner: %w", err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return daemonRuntimeIdentity{}, fmt.Errorf("config owner metadata unavailable")
	}
	owner, err := user.LookupId(strconv.FormatUint(uint64(stat.Uid), 10))
	if err != nil || owner.Username == "" || owner.Gid == "0" {
		return daemonRuntimeIdentity{}, fmt.Errorf("config must be owned by a non-root runtime user")
	}
	group, err := user.LookupGroupId(owner.Gid)
	if err != nil || group.Name == "" {
		return daemonRuntimeIdentity{}, fmt.Errorf("config primary group unavailable")
	}
	return daemonRuntimeIdentity{User: owner.Username, Group: group.Name}, nil
}

func systemdQuote(value string) string { return strconv.Quote(value) }

func (c Controller) daemonUnitText(identity daemonRuntimeIdentity) (string, error) {
	if c.Config.Controller.TunnelEnvFile == "" || c.Config.Controller.TunnelHealthListenAddr == "" || c.Config.ListenAddr == "" {
		return "", fmt.Errorf("tunnel env file, gateway listen address, and tunnel health address are required")
	}
	if _, err := os.Stat(c.Config.Controller.TunnelEnvFile); err != nil {
		return "", fmt.Errorf("tunnel env file unavailable: %w", err)
	}
	workingDir, err := c.gatewayWorkingDir()
	if err != nil {
		return "", err
	}
	return "[Unit]\n" +
		"Description=GPT Tunnel Gateway and Tunnel\n" +
		"After=network-online.target\n" +
		"Wants=network-online.target\n\n" +
		"[Service]\n" +
		"Type=simple\n" +
		"User=" + systemdQuote(identity.User) + "\n" +
		"Group=" + systemdQuote(identity.Group) + "\n" +
		"WorkingDirectory=" + systemdQuote(workingDir) + "\n" +
		"EnvironmentFile=-" + systemdQuote(c.Config.Controller.TunnelEnvFile) + "\n" +
		"Environment=GPT_TUNNEL_CONFIG=" + systemdQuote(c.ConfigPath) + "\n" +
		"Environment=MCP_SERVER_URL=" + systemdQuote("http://"+c.Config.ListenAddr+"/mcp") + "\n" +
		"Environment=HEALTH_LISTEN_ADDR=" + systemdQuote(c.Config.Controller.TunnelHealthListenAddr) + "\n" +
		"ExecStart=" + systemdQuote(c.Config.Controller.GatewayBinary) + " --config " + systemdQuote(c.ConfigPath) + "\n" +
		"ExecStartPost=" + systemdQuote(c.Config.Controller.TunnelClientBinary) + " run\n" +
		"KillMode=control-group\n" +
		"Restart=no\n" +
		"TimeoutStartSec=infinity\n\n" +
		"[Install]\n" +
		"WantedBy=multi-user.target\n", nil
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
