package controller

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
)

type Controller struct {
	Config     config.Config
	ConfigPath string
}
type ProcessStatus struct {
	Name               string `json:"name"`
	Running            bool   `json:"running"`
	PID                int    `json:"pid,omitempty"`
	Executable         string `json:"executable,omitempty"`
	ExpectedExecutable string `json:"expected_executable"`
}
type Status struct {
	Gateway      ProcessStatus `json:"gateway"`
	Tunnel       ProcessStatus `json:"tunnel"`
	GatewayReady bool          `json:"gateway_ready"`
	TunnelReady  bool          `json:"tunnel_ready"`
}

func (c Controller) pidPath(name string) string {
	return filepath.Join(c.Config.Controller.PIDDir, name+".pid")
}
func (c Controller) logPath(name string) string {
	return filepath.Join(c.Config.Controller.LogDir, name+".log")
}
func readPID(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(data)))
}
func procExe(pid int) (string, error) {
	return filepath.EvalSymlinks(filepath.Join("/proc", strconv.Itoa(pid), "exe"))
}
func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }
func (c Controller) process(name, expected string) ProcessStatus {
	p := ProcessStatus{Name: name, ExpectedExecutable: expected}
	pid, err := readPID(c.pidPath(name))
	if err != nil {
		return p
	}
	p.PID = pid
	if !alive(pid) {
		_ = os.Remove(c.pidPath(name))
		return p
	}
	exe, _ := procExe(pid)
	p.Executable = exe
	p.Running = exe == expected
	return p
}
func (c Controller) Status(ctx context.Context) (Status, error) {
	gatewayExpected, _ := filepath.EvalSymlinks(c.Config.Controller.GatewayBinary)
	tunnelExpected, _ := filepath.EvalSymlinks(c.Config.Controller.TunnelClientBinary)
	s := Status{Gateway: c.process("gateway", gatewayExpected), Tunnel: c.process("tunnel", tunnelExpected)}
	s.GatewayReady = checkURL(ctx, "http://"+c.Config.ListenAddr+"/readyz")
	if c.Config.Controller.TunnelReadyURL != "" {
		s.TunnelReady = checkURL(ctx, c.Config.Controller.TunnelReadyURL)
	}
	return s, nil
}
func checkURL(ctx context.Context, url string) bool {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	client := http.Client{Timeout: 2 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}
func waitURL(url string, want bool, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		ok := checkURL(ctx, url)
		cancel()
		if ok == want {
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("readiness timeout for %s", url)
}
func readEnv(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := []string{}
	scan := bufio.NewScanner(io.LimitReader(f, 1<<20))
	for scan.Scan() {
		line := strings.TrimSpace(scan.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok || k == "" || strings.ContainsAny(k, " \t\r\n") {
			return nil, fmt.Errorf("invalid env line")
		}
		out = append(out, k+"="+v)
	}
	return out, scan.Err()
}
func (c Controller) startProcess(name, binary string, args, env []string) error {
	expected, err := filepath.EvalSymlinks(binary)
	if err != nil {
		return err
	}
	existing := c.process(name, expected)
	if existing.Running {
		return fmt.Errorf("%s already running", name)
	}
	if err := fsutil.EnsureDir(c.Config.Controller.PIDDir, 0o700); err != nil {
		return err
	}
	if err := fsutil.EnsureDir(c.Config.Controller.LogDir, 0o700); err != nil {
		return err
	}
	log, err := os.OpenFile(c.logPath(name), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer log.Close()
	cmd := exec.Command(binary, args...)
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdin = nil
	cmd.Stdout = log
	cmd.Stderr = log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	if err := fsutil.WriteFileAtomic(c.pidPath(name), []byte(strconv.Itoa(cmd.Process.Pid)+"\n"), 0o600); err != nil {
		_ = cmd.Process.Kill()
		return err
	}
	return nil
}
func (c Controller) stopProcess(name, expected string) error {
	expected, _ = filepath.EvalSymlinks(expected)
	p := c.process(name, expected)
	if !p.Running {
		if p.PID != 0 && p.Executable != expected {
			return fmt.Errorf("refusing to signal unexpected executable %s", p.Executable)
		}
		_ = os.Remove(c.pidPath(name))
		return nil
	}
	if err := syscall.Kill(p.PID, syscall.SIGTERM); err != nil {
		return err
	}
	deadline := time.Now().Add(5 * time.Second)
	for alive(p.PID) && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if alive(p.PID) {
		_ = syscall.Kill(p.PID, syscall.SIGKILL)
	}
	_ = os.Remove(c.pidPath(name))
	return nil
}
func (c Controller) Start() error {
	lock, err := lockfile.Acquire(c.Config.Controller.PIDDir, "controller")
	if err != nil {
		return err
	}
	defer lock.Release()
	if err := c.startProcess("gateway", c.Config.Controller.GatewayBinary, []string{"--config", c.ConfigPath}, []string{"GPT_TUNNEL_CONFIG=" + c.ConfigPath}); err != nil {
		return err
	}
	if err := waitURL("http://"+c.Config.ListenAddr+"/readyz", true, 30*time.Second); err != nil {
		_ = c.stopProcess("gateway", c.Config.Controller.GatewayBinary)
		return err
	}
	env, err := readEnv(c.Config.Controller.TunnelEnvFile)
	if err != nil {
		_ = c.stopProcess("gateway", c.Config.Controller.GatewayBinary)
		return err
	}
	if err := c.startProcess("tunnel", c.Config.Controller.TunnelClientBinary, []string{"run"}, env); err != nil {
		_ = c.stopProcess("gateway", c.Config.Controller.GatewayBinary)
		return err
	}
	if c.Config.Controller.TunnelReadyURL != "" {
		if err := waitURL(c.Config.Controller.TunnelReadyURL, true, 30*time.Second); err != nil {
			_ = c.stopProcess("tunnel", c.Config.Controller.TunnelClientBinary)
			_ = c.stopProcess("gateway", c.Config.Controller.GatewayBinary)
			return err
		}
	}
	return nil
}
func (c Controller) Stop() error {
	lock, err := lockfile.Acquire(c.Config.Controller.PIDDir, "controller")
	if err != nil {
		return err
	}
	defer lock.Release()
	if err := c.stopProcess("tunnel", c.Config.Controller.TunnelClientBinary); err != nil {
		return err
	}
	return c.stopProcess("gateway", c.Config.Controller.GatewayBinary)
}
func (c Controller) Restart() error {
	if err := c.Stop(); err != nil {
		return err
	}
	return c.Start()
}
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
		startErr = waitURL("http://"+c.Config.ListenAddr+"/readyz", true, 30*time.Second)
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
	if err := waitURL("http://"+c.Config.ListenAddr+"/readyz", true, 30*time.Second); err != nil {
		return fmt.Errorf("gateway restart failed (%v); rollback readiness failed: %w", startErr, err)
	}
	return fmt.Errorf("gateway restart failed and previous executable was restored: %w", startErr)
}
func (c Controller) Doctor(ctx context.Context) error {
	s, err := c.Status(ctx)
	if err != nil {
		return err
	}
	if !s.Gateway.Running || !s.GatewayReady {
		return fmt.Errorf("gateway is not healthy")
	}
	if !s.Tunnel.Running || !s.TunnelReady {
		return fmt.Errorf("tunnel is not healthy")
	}
	return nil
}
func (c Controller) Logs(name string, lines int) (string, error) {
	if lines < 1 || lines > 10000 {
		return "", fmt.Errorf("invalid line count")
	}
	paths := []string{}
	switch name {
	case "gateway":
		paths = []string{c.logPath("gateway")}
	case "tunnel":
		paths = []string{c.logPath("tunnel")}
	case "all", "":
		paths = []string{c.logPath("gateway"), c.logPath("tunnel")}
	default:
		return "", fmt.Errorf("unknown log name")
	}
	var b strings.Builder
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil && !os.IsNotExist(err) {
			return "", err
		}
		parts := strings.Split(string(data), "\n")
		start := 0
		if len(parts) > lines {
			start = len(parts) - lines
		}
		fmt.Fprintf(&b, "==> %s <==\n%s\n", p, strings.Join(parts[start:], "\n"))
	}
	return b.String(), nil
}
