package upgrade

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/controller"
	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
)

var semverRE = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

type Result struct {
	Status     string `json:"status"`
	SourceRoot string `json:"source_root"`
	SourceSHA  string `json:"source_sha"`
	Previous   string `json:"previous_version"`
	Target     string `json:"target_version"`
	GatewayPID int    `json:"gateway_pid"`
	TunnelPID  int    `json:"tunnel_pid"`
	Rollback   bool   `json:"rollback"`
	Error      string `json:"error,omitempty"`
}

type Runner struct {
	Config     config.Config
	ConfigPath string
	Target     string
}

func (r Runner) Run(ctx context.Context) (Result, error) {
	root, sha, err := sourceRoot()
	if err != nil {
		return Result{}, err
	}
	if err := validateSource(root, sha); err != nil {
		return Result{}, err
	}
	targetValue := r.Target
	if targetValue == "" {
		data, readErr := os.ReadFile(filepath.Join(root, "VERSION"))
		if readErr != nil {
			return Result{}, readErr
		}
		targetValue = strings.TrimSpace(string(data))
	}
	target, err := parseVersion(targetValue)
	if err != nil {
		return Result{}, err
	}
	installed, err := installedVersion(r.Config.Controller.GatewayBinary)
	if err != nil {
		return Result{}, err
	}
	if compareVersion(target, installed) <= 0 {
		return Result{}, fmt.Errorf("target version %s is not newer than installed version %s", target, installed)
	}
	if err := controller.ValidateTunnelEnv(r.Config.Controller.TunnelEnvFile); err != nil {
		return Result{}, err
	}
	before, err := r.ConfigController().Status(ctx)
	if err != nil {
		return Result{}, err
	}
	if !before.Gateway.Running || !before.Tunnel.Running || !before.GatewayReady || !before.TunnelReady {
		return Result{}, fmt.Errorf("runtime is not healthy before upgrade")
	}
	if err := os.MkdirAll(filepath.Join(r.Config.Controller.PIDDir, "upgrades"), 0o700); err != nil {
		return Result{}, err
	}
	lock, err := lockfile.Acquire(filepath.Join(r.Config.Controller.PIDDir, "upgrades"), "upgrade")
	if err != nil {
		return Result{}, err
	}
	defer lock.Release()
	release, err := os.MkdirTemp("/tmp", "gpt-tunnel-upgrade-")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(release)
	if err := buildRelease(ctx, root, release); err != nil {
		return Result{}, err
	}
	if err := validateRelease(release, target); err != nil {
		return Result{}, err
	}
	paths := map[string]string{"gpt-tunnel-gatewayd": r.Config.Controller.GatewayBinary, "gpt-tunnel": filepath.Join(filepath.Dir(r.Config.Controller.GatewayBinary), "gpt-tunnel"), "gpt-tunnelctl": filepath.Join(filepath.Dir(r.Config.Controller.GatewayBinary), "gpt-tunnelctl")}
	backupDir, err := os.MkdirTemp(filepath.Join(r.Config.Controller.PIDDir, "upgrades"), "backup-")
	if err != nil {
		return Result{}, err
	}
	defer func() {
		if err == nil {
			_ = os.RemoveAll(backupDir)
		}
	}()
	old := map[string][]byte{}
	oldHashes := map[string]string{}
	for name, dst := range paths {
		b, e := os.ReadFile(dst)
		if e != nil {
			return Result{}, e
		}
		old[name] = b
		oldHashes[name] = hashBytes(b)
		if e = fsutil.WriteFileAtomic(filepath.Join(backupDir, name), b, 0o700); e != nil {
			return Result{}, e
		}
		backup, e := os.ReadFile(filepath.Join(backupDir, name))
		if e != nil || hashBytes(backup) != oldHashes[name] {
			return Result{}, fmt.Errorf("backup checksum verification failed for %s", name)
		}
	}
	if err := replaceAll(release, paths); err != nil {
		_ = restoreAll(paths, old)
		return Result{Status: "UPGRADE_ROLLED_BACK", SourceRoot: root, SourceSHA: sha, Previous: installed, Target: target, TunnelPID: before.Tunnel.PID, Rollback: true, Error: err.Error()}, fmt.Errorf("upgrade rolled back: %w", err)
	}
	rollback := func(cause error) (Result, error) {
		for name, dst := range paths {
			_ = fsutil.WriteFileAtomic(dst, old[name], 0o755)
		}
		_ = r.ConfigController().StopGatewayForUpgrade()
		_ = r.ConfigController().RestartGatewayAfterUpgrade()
		return Result{Status: "UPGRADE_ROLLED_BACK", SourceRoot: root, SourceSHA: sha, Previous: installed, Target: target, TunnelPID: before.Tunnel.PID, Rollback: true, Error: cause.Error()}, fmt.Errorf("upgrade rolled back: %w", cause)
	}
	if err := r.ConfigController().RestartGatewayAfterUpgrade(); err != nil {
		return rollback(err)
	}
	after, err := r.ConfigController().Status(ctx)
	if err != nil {
		return rollback(err)
	}
	if after.Gateway.PID == before.Gateway.PID || after.Tunnel.PID != before.Tunnel.PID || !after.Gateway.Running || !after.Tunnel.Running || !after.GatewayReady || !after.TunnelReady {
		return rollback(fmt.Errorf("post-upgrade process or readiness invariant failed"))
	}
	if err := smoke(ctx, r.Config); err != nil {
		return rollback(err)
	}
	_ = os.RemoveAll(backupDir)
	return Result{Status: "UPGRADE_COMPLETE", SourceRoot: root, SourceSHA: sha, Previous: installed, Target: target, GatewayPID: after.Gateway.PID, TunnelPID: after.Tunnel.PID}, nil
}

func (r Runner) ConfigController() controller.Controller {
	return controller.Controller{Config: r.Config, ConfigPath: r.ConfigPath}
}
func parseVersion(v string) (string, error) {
	if !semverRE.MatchString(v) {
		return "", fmt.Errorf("invalid target version")
	}
	return v, nil
}
func compareVersion(a, b string) int {
	var x, y [3]int
	fmt.Sscanf(a, "%d.%d.%d", &x[0], &x[1], &x[2])
	fmt.Sscanf(b, "%d.%d.%d", &y[0], &y[1], &y[2])
	for i := 0; i < 3; i++ {
		if x[i] < y[i] {
			return -1
		}
		if x[i] > y[i] {
			return 1
		}
	}
	return 0
}
func installedVersion(path string) (string, error) {
	out, err := exec.Command(path, "--version").Output()
	if err != nil {
		return "", err
	}
	v := strings.TrimSpace(string(out))
	if !semverRE.MatchString(v) {
		return "", fmt.Errorf("invalid installed version")
	}
	return v, nil
}
func sourceRoot() (string, string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", "", err
	}
	root := wd
	for {
		if _, e := os.Stat(filepath.Join(root, ".git")); e == nil {
			break
		}
		p := filepath.Dir(root)
		if p == root {
			return "", "", fmt.Errorf("not inside a Git worktree")
		}
		root = p
	}
	out, err := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", "", err
	}
	sha := strings.TrimSpace(string(out))
	return root, sha, nil
}
func validateSource(root, sha string) error {
	if filepath.Base(root) != "gpt-tunnel-gateway" {
		return fmt.Errorf("unexpected repository root")
	}
	if !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(sha) {
		return fmt.Errorf("invalid source SHA")
	}
	branch, _ := exec.Command("git", "-C", root, "branch", "--show-current").Output()
	if strings.TrimSpace(string(branch)) != "main" {
		return fmt.Errorf("source must be on main")
	}
	remote, _ := exec.Command("git", "-C", root, "remote", "get-url", "origin").Output()
	if !strings.Contains(string(remote), "rceman/gpt-tunnel-gateway") {
		return fmt.Errorf("unexpected repository identity")
	}
	clean, _ := exec.Command("git", "-C", root, "status", "--porcelain", "--untracked-files=all").Output()
	if len(bytes.TrimSpace(clean)) != 0 {
		return fmt.Errorf("source worktree is dirty")
	}
	origin, _ := exec.Command("git", "-C", root, "rev-parse", "origin/main").Output()
	if strings.TrimSpace(string(origin)) != sha {
		return fmt.Errorf("source is not synchronized with origin/main")
	}
	b, err := os.ReadFile(filepath.Join(root, "VERSION"))
	if err != nil {
		return err
	}
	if !semverRE.MatchString(strings.TrimSpace(string(b))) {
		return fmt.Errorf("invalid source VERSION")
	}
	return nil
}
func buildRelease(ctx context.Context, root, dir string) error {
	cmd := exec.CommandContext(ctx, filepath.Join(root, "scripts", "build-release.sh"), dir)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("release build failed: %s", strings.TrimSpace(string(out)))
	}
	return nil
}
func validateRelease(dir, target string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	names := []string{}
	for _, e := range entries {
		names = append(names, e.Name())
		if e.Name() == "SHA256SUMS" {
			continue
		}
		if e.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("release symlink")
		}
		info, er := e.Info()
		if er != nil || !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
			return fmt.Errorf("invalid release artifact")
		}
		if e.Name() != "gpt-tunnel" && e.Name() != "gpt-tunnel-gatewayd" && e.Name() != "gpt-tunnelctl" {
			return fmt.Errorf("unexpected release artifact %s", e.Name())
		}
	}
	sort.Strings(names)
	if strings.Join(names, ",") != "SHA256SUMS,gpt-tunnel,gpt-tunnel-gatewayd,gpt-tunnelctl" {
		return fmt.Errorf("release output set mismatch")
	}
	lines, err := os.ReadFile(filepath.Join(dir, "SHA256SUMS"))
	if err != nil {
		return err
	}
	for _, line := range strings.Split(strings.TrimSpace(string(lines)), "\n") {
		f := strings.Fields(line)
		if len(f) != 2 || strings.HasPrefix(f[1], "/") {
			return fmt.Errorf("invalid checksum manifest")
		}
		data, e := os.ReadFile(filepath.Join(dir, f[1]))
		if e != nil {
			return e
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != f[0] {
			return fmt.Errorf("checksum mismatch")
		}
	}
	for _, name := range []string{"gpt-tunnel", "gpt-tunnel-gatewayd", "gpt-tunnelctl"} {
		v, e := installedVersion(filepath.Join(dir, name))
		if e != nil || v != target {
			return fmt.Errorf("release version mismatch")
		}
	}
	return nil
}
func replaceAll(dir string, paths map[string]string) error {
	for name, dst := range paths {
		if err := copyFile(filepath.Join(dir, name), dst); err != nil {
			return err
		}
	}
	return nil
}

func restoreAll(paths map[string]string, old map[string][]byte) error {
	var first error
	for name, dst := range paths {
		if err := fsutil.WriteFileAtomic(dst, old[name], 0o755); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func hashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(dst, data, 0o755)
}
func smoke(ctx context.Context, c config.Config) error {
	url := "http://" + c.ListenAddr + "/mcp"
	call := func(id int, method string, params any) (map[string]any, error) {
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		resp, e := http.DefaultClient.Do(req)
		if e != nil {
			return nil, e
		}
		defer resp.Body.Close()
		var v map[string]any
		e = json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&v)
		return v, e
	}
	init, err := call(1, "initialize", map[string]any{})
	if err != nil {
		return err
	}
	info := init["result"].(map[string]any)["serverInfo"].(map[string]any)
	if info["version"] != "0.2.3" {
		return fmt.Errorf("MCP version mismatch")
	}
	list, err := call(2, "tools/list", map[string]any{})
	if err != nil {
		return err
	}
	tools := list["result"].(map[string]any)["tools"].([]any)
	if len(tools) == 0 {
		return fmt.Errorf("no MCP tools")
	}
	ping, err := call(3, "tools/call", map[string]any{"name": "system_ping", "arguments": map[string]any{}, "_meta": map[string]any{"upgrade": true}})
	if err != nil {
		return err
	}
	if _, ok := ping["result"]; !ok {
		return fmt.Errorf("MCP ping failed")
	}
	cap, err := call(4, "tools/call", map[string]any{"name": "gateway_capabilities", "arguments": map[string]any{}})
	if err != nil {
		return err
	}
	if _, ok := cap["result"]; !ok {
		return fmt.Errorf("MCP capabilities failed")
	}
	return nil
}
