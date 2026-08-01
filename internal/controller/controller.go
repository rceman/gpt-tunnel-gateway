package controller

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
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
	IdentityValid      bool   `json:"identity_valid"`
	IdentityReason     string `json:"identity_reason,omitempty"`
	StartTimeTicks     uint64 `json:"start_time_ticks,omitempty"`
}
type Status struct {
	Gateway          ProcessStatus `json:"gateway"`
	Tunnel           ProcessStatus `json:"tunnel"`
	GatewayReady     bool          `json:"gateway_ready"`
	TunnelReady      bool          `json:"tunnel_ready"`
	InstalledVersion string        `json:"installed_version,omitempty"`
	RunningVersion   string        `json:"running_version,omitempty"`
	VersionMatch     bool          `json:"version_match"`
}

type pidRecord struct {
	PID            int    `json:"pid"`
	StartTimeTicks uint64 `json:"start_time_ticks"`
	UID            uint32 `json:"uid"`
	InstanceToken  string `json:"instance_token"`
}

func (c Controller) pidPath(name string) string {
	return filepath.Join(c.Config.Controller.PIDDir, name+".pid")
}
func (c Controller) logPath(name string) string {
	return filepath.Join(c.Config.Controller.LogDir, name+".log")
}
func readPID(path string) (int, error) {
	record, err := readPIDRecord(path)
	if err != nil {
		return 0, err
	}
	return record.PID, nil
}
func readPIDRecord(path string) (pidRecord, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return pidRecord{}, err
	}
	var record pidRecord
	if len(bytes.TrimSpace(data)) > 0 && bytes.TrimSpace(data)[0] == '{' {
		if err := json.Unmarshal(data, &record); err != nil {
			return pidRecord{}, err
		}
		if record.PID < 1 {
			return pidRecord{}, fmt.Errorf("invalid PID record")
		}
		return record, nil
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid < 1 {
		return pidRecord{}, fmt.Errorf("invalid PID file")
	}
	return pidRecord{PID: pid}, nil
}
func procExe(pid int) (string, error) {
	return filepath.EvalSymlinks(filepath.Join("/proc", strconv.Itoa(pid), "exe"))
}
func procCmdline(pid int) (string, error) {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline"))
	if err != nil {
		return "", err
	}
	return strings.Join(strings.FieldsFunc(string(data), func(r rune) bool { return r == 0 }), " "), nil
}
func procStartTime(pid int) (uint64, error) {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return 0, err
	}
	closeParen := bytes.LastIndexByte(data, ')')
	if closeParen < 0 || closeParen+2 >= len(data) {
		return 0, fmt.Errorf("invalid process stat")
	}
	fields := strings.Fields(string(data[closeParen+2:]))
	if len(fields) < 20 {
		return 0, fmt.Errorf("invalid process stat fields")
	}
	return strconv.ParseUint(fields[19], 10, 64)
}
func procUID(pid int) (uint32, error) {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "status"))
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "Uid:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			break
		}
		value, parseErr := strconv.ParseUint(fields[1], 10, 32)
		return uint32(value), parseErr
	}
	return 0, fmt.Errorf("process UID unavailable")
}
func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }
func (c Controller) process(name, expected string) ProcessStatus {
	p := ProcessStatus{Name: name, ExpectedExecutable: expected}
	if expected == "" {
		p.IdentityReason = "configured executable is unavailable"
		return p
	}
	record, err := readPIDRecord(c.pidPath(name))
	if err != nil {
		return p
	}
	p.PID = record.PID
	if !alive(record.PID) {
		_ = os.Remove(c.pidPath(name))
		return p
	}
	p.StartTimeTicks, _ = procStartTime(record.PID)
	uid, uidErr := procUID(record.PID)
	cmdline, cmdErr := procCmdline(record.PID)
	if uidErr != nil || cmdErr != nil || uid != uint32(os.Getuid()) {
		p.IdentityReason = "process UID does not match controller owner"
		return p
	}
	if record.StartTimeTicks != 0 && record.StartTimeTicks != p.StartTimeTicks {
		p.IdentityReason = "PID was reused after controller record"
		return p
	}
	if !strings.Contains(cmdline, expected) {
		p.IdentityReason = "configured executable is absent from process command line"
		return p
	}
	if name == "gateway" && c.ConfigPath != "" && !strings.Contains(cmdline, c.ConfigPath) {
		p.IdentityReason = "configured gateway config is absent from process command line"
		return p
	}
	if name == "tunnel" && !strings.Contains(cmdline, " run") && !strings.HasSuffix(cmdline, " run") {
		p.IdentityReason = "managed tunnel command is not run"
		return p
	}
	exe, _ := procExe(record.PID)
	p.Executable = exe
	p.Running = true
	p.IdentityValid = true
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
	s.InstalledVersion = installedVersion(c.Config.Controller.GatewayBinary)
	if s.GatewayReady {
		s.RunningVersion = runningVersion(ctx, c.gatewayReadyURL(), c.Config.GatewayID)
	}
	s.VersionMatch = s.InstalledVersion != "" && s.RunningVersion != "" && s.InstalledVersion == s.RunningVersion
	return s, nil
}
func installedVersion(path string) string {
	out, err := exec.Command(path, "--version").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
func runningVersion(ctx context.Context, readyURL, gatewayID string) string {
	endpoint := strings.TrimSuffix(readyURL, "/readyz") + "/mcp"
	payload := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "system_ping", "arguments": map[string]any{}}}
	data, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return ""
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Timeout: 2 * time.Second}).Do(request)
	if err != nil {
		return ""
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return ""
	}
	var envelope struct {
		Result struct {
			IsError           bool `json:"isError"`
			StructuredContent struct {
				Version   string `json:"version"`
				GatewayID string `json:"gateway_id"`
			} `json:"structuredContent"`
		} `json:"result"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&envelope); err != nil || envelope.Result.IsError {
		return ""
	}
	if gatewayID != "" && envelope.Result.StructuredContent.GatewayID != gatewayID {
		return ""
	}
	return envelope.Result.StructuredContent.Version
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

// ValidateTunnelEnv validates the controller-owned environment without exposing values.
func ValidateTunnelEnv(path string) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("tunnel env must be an owner-only regular file")
	}
	if st, ok := info.Sys().(*syscall.Stat_t); !ok || st.Uid != uint32(os.Getuid()) {
		return fmt.Errorf("tunnel env owner mismatch")
	}
	_, err = readTunnelEnv(path)
	return err
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
	startTime, startErr := procStartTime(cmd.Process.Pid)
	if startErr != nil {
		_ = cmd.Process.Kill()
		return startErr
	}
	record := pidRecord{PID: cmd.Process.Pid, StartTimeTicks: startTime, UID: uint32(os.Getuid()), InstanceToken: fmt.Sprintf("%d-%d", cmd.Process.Pid, startTime)}
	if err := fsutil.WriteJSONAtomic(c.pidPath(name), record, 0o600); err != nil {
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

// RestartGatewayAfterUpgrade stops the exact gateway recorded by the controller
// and starts the currently installed binary. It intentionally does not touch the
// tunnel process; callers own rollback of the installed gateway binary.
func (c Controller) RestartGatewayAfterUpgrade() error {
	lock, err := lockfile.Acquire(c.Config.Controller.PIDDir, "controller")
	if err != nil {
		return err
	}
	defer lock.Release()
	if err := c.stopProcess("gateway", c.Config.Controller.GatewayBinary); err != nil {
		return err
	}
	if err := c.startProcess("gateway", c.Config.Controller.GatewayBinary, []string{"--config", c.ConfigPath}, []string{"GPT_TUNNEL_CONFIG=" + c.ConfigPath}); err != nil {
		return err
	}
	return waitURL(c.gatewayReadyURL(), true, 30*time.Second)
}

// StopGatewayForUpgrade stops only the gateway recorded by this controller.
func (c Controller) StopGatewayForUpgrade() error {
	return c.stopProcess("gateway", c.Config.Controller.GatewayBinary)
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
	if !s.VersionMatch {
		return fmt.Errorf("installed and running gateway versions differ")
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
