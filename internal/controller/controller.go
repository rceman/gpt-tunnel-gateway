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
	"regexp"
	"sort"
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
func (c Controller) gatewayReadyURL() string { return "http://" + c.Config.ListenAddr + "/readyz" }
func (c Controller) tunnelReadyURL() string {
	return "http://" + c.Config.Controller.TunnelHealthListenAddr + "/readyz"
}
func (c Controller) Status(ctx context.Context) (Status, error) {
	gatewayExpected, _ := filepath.EvalSymlinks(c.Config.Controller.GatewayBinary)
	tunnelExpected, _ := filepath.EvalSymlinks(c.Config.Controller.TunnelClientBinary)
	s := Status{Gateway: c.process("gateway", gatewayExpected), Tunnel: c.process("tunnel", tunnelExpected)}
	s.GatewayReady = checkURL(ctx, c.gatewayReadyURL())
	s.TunnelReady = checkURL(ctx, c.tunnelReadyURL())
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
func readTunnelEnv(path string) ([]string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("tunnel env file must be a regular file with mode 0600 or stricter")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	values := map[string]string{}
	reserved := map[string]bool{
		"MCP_SERVER_URL": true, "MCP_COMMAND": true, "HEALTH_LISTEN_ADDR": true,
		"TUNNEL_CLIENT_CONFIG": true, "TUNNEL_CLIENT_PROFILE": true, "TUNNEL_CLIENT_PROFILE_FILE": true,
	}
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
		if !ok || k == "" || !validEnvName(k) {
			return nil, fmt.Errorf("invalid tunnel env line")
		}
		if reserved[k] {
			return nil, fmt.Errorf("tunnel env must not override controller-owned variable %s", k)
		}
		if _, exists := values[k]; exists {
			return nil, fmt.Errorf("duplicate tunnel env variable %s", k)
		}
		values[k] = v
	}
	if err := scan.Err(); err != nil {
		return nil, err
	}
	for _, required := range []string{"CONTROL_PLANE_API_KEY", "CONTROL_PLANE_TUNNEL_ID"} {
		if values[required] == "" {
			return nil, fmt.Errorf("tunnel env is missing %s", required)
		}
	}
	if !regexp.MustCompile(`^tunnel_[0-9a-f]{32}$`).MatchString(values["CONTROL_PLANE_TUNNEL_ID"]) {
		return nil, fmt.Errorf("CONTROL_PLANE_TUNNEL_ID has invalid format")
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, key+"="+values[key])
	}
	return out, nil
}

func validEnvName(name string) bool {
	for i, r := range name {
		if (r >= 'A' && r <= 'Z') || r == '_' || (i > 0 && r >= '0' && r <= '9') {
			continue
		}
		return false
	}
	return name != ""
}

func processEnv(extra []string) []string {
	values := map[string]string{}
	values["LC_ALL"] = "C"
	for _, key := range []string{"HOME", "PATH", "USER", "LOGNAME", "TMPDIR", "SSH_AUTH_SOCK", "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "SSL_CERT_FILE", "SSL_CERT_DIR"} {
		if value := os.Getenv(key); value != "" {
			values[key] = value
		}
	}
	for _, entry := range extra {
		key, value, ok := strings.Cut(entry, "=")
		if ok && validEnvName(key) {
			values[key] = value
		}
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, key+"="+values[key])
	}
	return out
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
	cmd.Env = processEnv(env)
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
	if err := waitURL(c.gatewayReadyURL(), true, 30*time.Second); err != nil {
		_ = c.stopProcess("gateway", c.Config.Controller.GatewayBinary)
		return err
	}
	env, err := readTunnelEnv(c.Config.Controller.TunnelEnvFile)
	if err != nil {
		_ = c.stopProcess("gateway", c.Config.Controller.GatewayBinary)
		return err
	}
	env = append(env, "MCP_SERVER_URL=http://"+c.Config.ListenAddr+"/mcp", "HEALTH_LISTEN_ADDR="+c.Config.Controller.TunnelHealthListenAddr)
	if err := c.startProcess("tunnel", c.Config.Controller.TunnelClientBinary, []string{"run"}, env); err != nil {
		_ = c.stopProcess("gateway", c.Config.Controller.GatewayBinary)
		return err
	}
	if err := waitURL(c.tunnelReadyURL(), true, 30*time.Second); err != nil {
		_ = c.stopProcess("tunnel", c.Config.Controller.TunnelClientBinary)
		_ = c.stopProcess("gateway", c.Config.Controller.GatewayBinary)
		return err
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
