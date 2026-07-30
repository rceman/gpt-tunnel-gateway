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
	"syscall"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/controller"
	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
)

var semverRE = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
var binaryOrder = []string{"gpt-tunnel-gatewayd", "gpt-tunnel", "gpt-tunnelctl"}

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
	if info, statErr := os.Lstat(r.ConfigPath); statErr != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
		return Result{}, fmt.Errorf("config must be an owner-only regular file")
	} else if st, ok := info.Sys().(*syscall.Stat_t); !ok || st.Uid != uint32(os.Getuid()) {
		return Result{}, fmt.Errorf("config owner mismatch")
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
	sourceVersion, err := os.ReadFile(filepath.Join(root, "VERSION"))
	if err != nil || strings.TrimSpace(string(sourceVersion)) != target {
		return Result{}, fmt.Errorf("source VERSION does not equal target version")
	}
	installed, err := validateInstalledRuntime(r.Config)
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
	for _, name := range []string{"gateway", "tunnel"} {
		if err := validatePIDFile(filepath.Join(r.Config.Controller.PIDDir, name+".pid")); err != nil {
			return Result{}, err
		}
	}
	protectedPaths := []string{r.ConfigPath, r.Config.Controller.TunnelEnvFile, r.Config.Controller.TunnelClientBinary}
	protectedHashes := map[string]string{}
	for _, path := range protectedPaths {
		h, hashErr := fileHash(path)
		if hashErr != nil {
			return Result{}, hashErr
		}
		protectedHashes[path] = h
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
	for _, name := range binaryOrder {
		dst := paths[name]
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
		if restoreErr := restoreAll(paths, old); restoreErr != nil {
			return Result{Status: "UPGRADE_ROLLBACK_FAILED", Error: restoreErr.Error()}, fmt.Errorf("replacement and rollback failed: %w", restoreErr)
		}
		if verifyErr := verifyHashes(paths, oldHashes); verifyErr != nil {
			return Result{Status: "UPGRADE_ROLLBACK_FAILED", Error: verifyErr.Error()}, verifyErr
		}
		return Result{Status: "UPGRADE_ROLLED_BACK", SourceRoot: root, SourceSHA: sha, Previous: installed, Target: target, TunnelPID: before.Tunnel.PID, Rollback: true, Error: err.Error()}, fmt.Errorf("upgrade rolled back: %w", err)
	}
	rollback := func(cause error) (Result, error) {
		if err := restoreAll(paths, old); err != nil {
			return Result{Status: "UPGRADE_ROLLBACK_FAILED", Error: err.Error()}, fmt.Errorf("rollback restore failed: %w", err)
		}
		if err := verifyHashes(paths, oldHashes); err != nil {
			return Result{Status: "UPGRADE_ROLLBACK_FAILED", Error: err.Error()}, err
		}
		if err := r.ConfigController().StopGatewayForUpgrade(); err != nil {
			return Result{Status: "UPGRADE_ROLLBACK_FAILED", Error: err.Error()}, err
		}
		if err := r.ConfigController().RestartGatewayAfterUpgrade(); err != nil {
			return Result{Status: "UPGRADE_ROLLBACK_FAILED", Error: err.Error()}, err
		}
		rolled, statusErr := r.ConfigController().Status(ctx)
		if statusErr != nil || !rolled.Gateway.Running || !rolled.GatewayReady || !rolled.Tunnel.Running || !rolled.TunnelReady || rolled.Tunnel.PID != before.Tunnel.PID {
			return Result{Status: "UPGRADE_ROLLBACK_FAILED", Error: "rollback readiness or tunnel identity proof failed"}, fmt.Errorf("rollback proof failed")
		}
		if err := r.ConfigController().Doctor(ctx); err != nil {
			return Result{Status: "UPGRADE_ROLLBACK_FAILED", Error: err.Error()}, err
		}
		for _, path := range protectedPaths {
			h, hashErr := fileHash(path)
			if hashErr != nil || h != protectedHashes[path] {
				return Result{Status: "UPGRADE_ROLLBACK_FAILED", Error: "protected runtime hash changed"}, fmt.Errorf("protected runtime hash changed")
			}
		}
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

func validateInstalledRuntime(c config.Config) (string, error) {
	home, _ := os.UserHomeDir()
	canonicalDir := filepath.Join(home, ".local", "bin")
	paths := []string{filepath.Join(canonicalDir, "gpt-tunnel-gatewayd"), filepath.Join(canonicalDir, "gpt-tunnel"), filepath.Join(canonicalDir, "gpt-tunnelctl")}
	if filepath.Clean(c.Controller.GatewayBinary) != paths[0] {
		return "", fmt.Errorf("gateway binary is not at canonical install path")
	}
	if info, err := os.Lstat(c.Controller.TunnelClientBinary); err != nil || !info.Mode().IsRegular() || info.Mode()&0o111 == 0 || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("tunnel-client is not a regular executable")
	}
	versions := make([]string, len(paths))
	for i, path := range paths {
		info, err := os.Lstat(path)
		if err != nil {
			return "", fmt.Errorf("installed binary is not available")
		}
		uidOK := false
		if st, ok := info.Sys().(*syscall.Stat_t); ok {
			uidOK = uint32(os.Getuid()) == st.Uid
		}
		if !info.Mode().IsRegular() || info.Mode()&0o111 == 0 || info.Mode()&os.ModeSymlink != 0 || !uidOK {
			return "", fmt.Errorf("installed binary is not a current-user executable regular file")
		}
		versions[i], err = installedVersion(path)
		if err != nil {
			return "", err
		}
	}
	if versions[0] != versions[1] || versions[0] != versions[2] {
		return "", fmt.Errorf("installed binary versions disagree")
	}
	return versions[2], nil
}

func runGit(root string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out)), nil
}

func sourceRoot() (string, string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", "", err
	}
	root := wd
	for {
		if _, e := os.Lstat(filepath.Join(root, ".git")); e == nil {
			break
		}
		p := filepath.Dir(root)
		if p == root {
			return "", "", fmt.Errorf("not inside a Git worktree")
		}
		root = p
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil || canonical != root {
		return "", "", fmt.Errorf("source root must not be symlinked")
	}
	sha, err := runGit(root, "rev-parse", "HEAD")
	if err != nil {
		return "", "", err
	}
	return root, sha, nil
}
func validateSource(root, sha string) error {
	if filepath.Base(root) != "gpt-tunnel-gateway" {
		return fmt.Errorf("unexpected repository root")
	}
	if !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(sha) {
		return fmt.Errorf("invalid source SHA")
	}
	branch, err := runGit(root, "branch", "--show-current")
	if err != nil || branch != "main" {
		return fmt.Errorf("source must be on main")
	}
	remote, err := runGit(root, "remote", "get-url", "origin")
	if err != nil || remote != "git@github.com:rceman/gpt-tunnel-gateway.git" {
		return fmt.Errorf("unexpected repository identity")
	}
	clean, err := runGit(root, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return err
	}
	if clean != "" {
		return fmt.Errorf("source worktree is dirty")
	}
	origin, err := runGit(root, "rev-parse", "refs/remotes/origin/main")
	if err != nil {
		return err
	}
	if origin != sha {
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
	allowed := map[string]bool{"gpt-tunnel": true, "gpt-tunnel-gatewayd": true, "gpt-tunnelctl": true, "SHA256SUMS": true}
	for _, e := range entries {
		names = append(names, e.Name())
		if !allowed[e.Name()] {
			return fmt.Errorf("unexpected release artifact %s", e.Name())
		}
		if e.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("release symlink")
		}
		info, er := e.Info()
		if er != nil || !info.Mode().IsRegular() || (e.Name() != "SHA256SUMS" && info.Mode()&0o111 == 0) {
			return fmt.Errorf("invalid release artifact")
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
	manifest := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(lines)), "\n") {
		f := strings.Fields(line)
		if len(f) != 2 || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(f[0]) || strings.Contains(f[1], "/") || strings.Contains(f[1], "\\") || !allowed[f[1]] || f[1] == "SHA256SUMS" || manifest[f[1]] {
			return fmt.Errorf("invalid checksum manifest")
		}
		manifest[f[1]] = true
		data, e := os.ReadFile(filepath.Join(dir, f[1]))
		if e != nil {
			return e
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != f[0] {
			return fmt.Errorf("checksum mismatch")
		}
	}
	if len(manifest) != 3 {
		return fmt.Errorf("checksum manifest is incomplete")
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
	for _, name := range binaryOrder {
		dst := paths[name]
		if err := copyFile(filepath.Join(dir, name), dst); err != nil {
			return err
		}
	}
	return nil
}

func restoreAll(paths map[string]string, old map[string][]byte) error {
	var first error
	for _, name := range binaryOrder {
		dst := paths[name]
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
func fileHash(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return hashBytes(data), nil
}
func verifyHashes(paths map[string]string, expected map[string]string) error {
	for _, name := range binaryOrder {
		got, err := fileHash(paths[name])
		if err != nil || got != expected[name] {
			return fmt.Errorf("binary restoration checksum failed for %s", name)
		}
	}
	return nil
}
func validatePIDFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("invalid PID file")
	}
	if st, ok := info.Sys().(*syscall.Stat_t); !ok || st.Uid != uint32(os.Getuid()) {
		return fmt.Errorf("PID file owner mismatch")
	}
	return nil
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
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("MCP HTTP status %d", resp.StatusCode)
		}
		var v map[string]any
		e = json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&v)
		if e == nil && v["jsonrpc"] != "2.0" {
			return nil, fmt.Errorf("invalid JSON-RPC envelope")
		}
		return v, e
	}
	init, err := call(1, "initialize", map[string]any{})
	if err != nil {
		return err
	}
	result, ok := init["result"].(map[string]any)
	if !ok {
		return fmt.Errorf("initialize result missing")
	}
	info, ok := result["serverInfo"].(map[string]any)
	if !ok || info["version"] != "0.2.3" {
		return fmt.Errorf("MCP version mismatch")
	}
	list, err := call(2, "tools/list", map[string]any{})
	if err != nil {
		return err
	}
	listResult, ok := list["result"].(map[string]any)
	if !ok {
		return fmt.Errorf("tools/list result missing")
	}
	tools, ok := listResult["tools"].([]any)
	if !ok || len(tools) == 0 {
		return fmt.Errorf("no MCP tools")
	}
	for _, raw := range tools {
		tool, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("invalid tool descriptor")
		}
		if _, ok := tool["inputSchema"].(map[string]any); !ok {
			return fmt.Errorf("tool input schema missing")
		}
		if _, ok := tool["outputSchema"].(map[string]any); !ok {
			return fmt.Errorf("tool output schema missing")
		}
		annotations, ok := tool["annotations"].(map[string]any)
		if !ok {
			return fmt.Errorf("tool annotations missing")
		}
		for _, key := range []string{"readOnlyHint", "destructiveHint", "idempotentHint", "openWorldHint"} {
			if _, ok := annotations[key].(bool); !ok {
				return fmt.Errorf("tool annotation missing")
			}
		}
	}
	ping, err := call(3, "tools/call", map[string]any{"name": "system_ping", "arguments": map[string]any{}, "_meta": map[string]any{"upgrade": true}})
	if err != nil {
		return err
	}
	pingResult, ok := ping["result"].(map[string]any)
	if !ok {
		return fmt.Errorf("MCP ping failed")
	}
	if pingResult["isError"] == true {
		return fmt.Errorf("MCP ping returned error")
	}
	if _, ok := pingResult["structuredContent"].(map[string]any); !ok {
		return fmt.Errorf("MCP ping structured content missing")
	}
	cap, err := call(4, "tools/call", map[string]any{"name": "gateway_capabilities", "arguments": map[string]any{}})
	if err != nil {
		return err
	}
	capResult, ok := cap["result"].(map[string]any)
	if !ok {
		return fmt.Errorf("MCP capabilities failed")
	}
	structured, ok := capResult["structuredContent"].(map[string]any)
	if !ok {
		return fmt.Errorf("MCP capabilities structured content missing")
	}
	if structured["gateway_id"] != c.GatewayID || structured["hub_protocol_root"] != "gpt-tunnel/v1" || structured["hub_branch"] != c.Hub.Branch || structured["hub_managed_root"] != filepath.Join(c.StateDir, "hub", "repository") {
		return fmt.Errorf("MCP capabilities mismatch")
	}
	return nil
}
