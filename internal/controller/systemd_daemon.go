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

func requireLinux() error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("daemon lifecycle is Linux/systemd-only")
	}
	return nil
}

func sudoCommand(ctx context.Context, args ...string) (string, error) {
	if err := requireLinux(); err != nil {
		return "", err
	}
	command := exec.CommandContext(ctx, "sudo", append([]string{"-n"}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}
		return "", fmt.Errorf("sudo %s: %s", strings.Join(args, " "), message)
	}
	return strings.TrimSpace(string(output)), nil
}

func systemctl(ctx context.Context, args ...string) (string, error) {
	if err := requireLinux(); err != nil {
		return "", err
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

func privilegedSystemctl(ctx context.Context, args ...string) (string, error) {
	return sudoCommand(ctx, append([]string{"systemctl"}, args...)...)
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
	if err := requireLinux(); err != nil {
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
	text, err := c.daemonUnitText(identity)
	if err != nil {
		return DaemonStatus{}, err
	}
	previous, err := c.daemonUnitStatus(ctx)
	if err != nil {
		return DaemonStatus{}, err
	}
	old, readErr := os.ReadFile(path)
	if readErr != nil && !os.IsNotExist(readErr) {
		return DaemonStatus{}, readErr
	}
	changed := !previous.Installed || string(old) != text
	if changed {
		if err := writeSystemUnit(ctx, path, []byte(text)); err != nil {
			return DaemonStatus{}, err
		}
	}
	if _, err := privilegedSystemctl(ctx, "daemon-reload"); err != nil {
		return DaemonStatus{}, err
	}
	if !previous.Active {
		if err := c.Stop(); err != nil {
			return DaemonStatus{}, fmt.Errorf("stop legacy runtime before daemon start: %w", err)
		}
	}
	if _, err := privilegedSystemctl(ctx, "enable", daemonUnitName); err != nil {
		return DaemonStatus{}, err
	}
	if previous.Active && changed {
		if _, err := privilegedSystemctl(ctx, "restart", "--no-block", daemonUnitName); err != nil {
			return DaemonStatus{}, err
		}
	} else if !previous.Active {
		if _, err := privilegedSystemctl(ctx, "start", "--no-block", daemonUnitName); err != nil {
			return DaemonStatus{}, err
		}
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
	if _, err := privilegedSystemctl(ctx, "restart", "--no-block", daemonUnitName); err != nil {
		return DaemonStatus{}, err
	}
	if err := c.waitDaemonReadiness(ctx); err != nil {
		return DaemonStatus{}, err
	}
	return c.DaemonStatus(ctx)
}

func (c Controller) DaemonRemove(ctx context.Context) error {
	if err := requireLinux(); err != nil {
		return err
	}
	unit, err := c.daemonUnitStatus(ctx)
	if err != nil {
		return err
	}
	if !unit.Installed {
		return nil
	}
	if _, err := privilegedSystemctl(ctx, "disable", "--now", daemonUnitName); err != nil {
		return err
	}
	if _, err := sudoCommand(ctx, "rm", "--", unit.UnitPath); err != nil {
		return err
	}
	_, err = privilegedSystemctl(ctx, "daemon-reload")
	return err
}

func writeSystemUnit(ctx context.Context, path string, data []byte) error {
	tmp, err := os.CreateTemp("", "gpt-tunnel.service.*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if _, err := sudoCommand(ctx, "mv", "--", tmpPath, path); err != nil {
		return err
	}
	_, err = sudoCommand(ctx, "chmod", "0644", "--", path)
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

func daemonControlBinary() (string, error) {
	executable, err := os.Executable()
	if err == nil {
		candidate := filepath.Join(filepath.Dir(executable), "gpt-tunnelctl")
		if info, statErr := os.Stat(candidate); statErr == nil && info.Mode().IsRegular() {
			return candidate, nil
		}
	}
	path, err := exec.LookPath("gpt-tunnelctl")
	if err != nil {
		return "", fmt.Errorf("gpt-tunnelctl is not installed beside the CLI or on PATH")
	}
	return filepath.Abs(path)
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
	controlBinary, err := daemonControlBinary()
	if err != nil {
		return "", err
	}
	return "[Unit]\n" +
		"Description=GPT Tunnel Gateway and Tunnel\n" +
		"After=network-online.target\n" +
		"Wants=network-online.target\n\n" +
		"[Service]\n" +
		"Type=oneshot\n" +
		"RemainAfterExit=yes\n" +
		"User=" + systemdQuote(identity.User) + "\n" +
		"Group=" + systemdQuote(identity.Group) + "\n" +
		"WorkingDirectory=" + systemdQuote(workingDir) + "\n" +
		"Environment=GPT_TUNNEL_CONFIG=" + systemdQuote(c.ConfigPath) + "\n" +
		"EnvironmentFile=-" + systemdQuote(c.Config.Controller.TunnelEnvFile) + "\n" +
		"ExecStart=" + systemdQuote(controlBinary) + " daemon-start\n" +
		"ExecStop=" + systemdQuote(controlBinary) + " daemon-stop\n" +
		"KillMode=control-group\n" +
		"Restart=no\n" +
		"TimeoutStartSec=90s\n\n" +
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
