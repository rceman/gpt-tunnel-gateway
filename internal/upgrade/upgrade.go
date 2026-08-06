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
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/controller"
	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

var semverRE = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
var binaryOrder = []string{"gpt-tunnel-gatewayd", "gpt-tunnel", "gpt-tunnelctl"}

type Result struct {
	Status           string             `json:"status"`
	TransactionID    string             `json:"transaction_id,omitempty"`
	SourceRoot       string             `json:"source_root"`
	SourceSHA        string             `json:"source_sha"`
	Previous         string             `json:"previous_version"`
	Target           string             `json:"target_version"`
	InstalledVersion string             `json:"installed_version,omitempty"`
	RunningVersion   string             `json:"running_version,omitempty"`
	VersionMatch     bool               `json:"version_match"`
	GatewayPID       int                `json:"gateway_pid"`
	TunnelPID        int                `json:"tunnel_pid"`
	Rollback         bool               `json:"rollback"`
	Blockers         []PreflightBlocker `json:"blockers,omitempty"`
	Error            string             `json:"error,omitempty"`
}

type Runner struct {
	Config     config.Config
	ConfigPath string
	Target     string
}

type upgradeController interface {
	Status(context.Context) (controller.Status, error)
	Doctor(context.Context) error
	RestartGatewayAfterUpgrade() error
	StopGatewayForUpgrade() error
}

type startupDiagnosticController interface {
	RestartGatewayAfterUpgradeDiagnostics() (controller.GatewayStartupDiagnostics, error)
}

type liveUpgradeController struct{ controller.Controller }

var (
	sourceRootFn               = sourceRoot
	validateSourceFn           = validateSource
	validateInstalledRuntimeFn = validateInstalledRuntime
	validateTunnelEnvFn        = controller.ValidateTunnelEnv
	buildReleaseFn             = buildRelease
	validateReleaseFn          = validateRelease
	preflightFn                = func(ctx context.Context, c config.Config, path string) (InspectResult, error) {
		return Inspect(ctx, c, path)
	}
	newUpgradeControllerFn = func(c config.Config, path string) upgradeController {
		return liveUpgradeController{controller.Controller{Config: c, ConfigPath: path}}
	}
	smokeFn                     = smoke
	persistStartupDiagnosticsFn = writeTransaction
)

func cleanupRollbackBackup(path string) error {
	if path == "" {
		return fmt.Errorf("rollback backup missing")
	}
	return removeUpgradeBackup(path)
}

func (r Runner) Run(ctx context.Context) (result Result, runErr error) {
	root, sha, err := sourceRootFn()
	if err != nil {
		return Result{}, err
	}
	if err := validateSourceFn(root, sha); err != nil {
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
	installed, err := validateInstalledRuntimeFn(r.Config)
	if err != nil {
		return Result{}, err
	}
	if compareVersion(target, installed) <= 0 {
		return Result{}, fmt.Errorf("target version %s is not newer than installed version %s", target, installed)
	}
	if err := validateTunnelEnvFn(r.Config.Controller.TunnelEnvFile); err != nil {
		return Result{}, err
	}
	preflight, err := preflightFn(ctx, r.Config, r.ConfigPath)
	if err != nil {
		return Result{}, err
	}
	if preflight.Status != "ready" {
		return Result{Status: "UPGRADE_BLOCKED", SourceRoot: root, SourceSHA: sha, Previous: installed, Target: target, Blockers: preflight.Blockers, GatewayPID: preflight.GatewayPID, TunnelPID: preflight.TunnelPID, InstalledVersion: installed, RunningVersion: preflight.RunningVersion, VersionMatch: preflight.VersionMatch}, fmt.Errorf("upgrade preflight blocked by %d issue(s)", len(preflight.Blockers))
	}
	ctl := r.ConfigController()
	before, err := ctl.Status(ctx)
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
	tx, err := newTransaction(r.Config, installed, target, sha)
	if err != nil {
		return Result{}, err
	}
	tx.TargetReleaseSHA = sha
	defer func() {
		if runErr != nil {
			if result.Status == "UPGRADE_ROLLED_BACK" {
				_ = tx.complete(r.Config, result.Status)
			} else {
				_ = tx.fail(r.Config, runErr)
			}
		}
	}()
	tx.GatewayPIDBefore, tx.TunnelPIDBefore = before.Gateway.PID, before.Tunnel.PID
	tx.ConfigSHABefore, _ = fileHash(r.ConfigPath)
	if hubRevision, hubErr := serviceHubRevision(ctx, r.Config); hubErr == nil {
		tx.OldHubSHA = hubRevision
	}
	if err := tx.phase(r.Config, "prepare"); err != nil {
		return Result{}, err
	}
	lock, err := lockfile.Acquire(filepath.Join(r.Config.Controller.PIDDir, "upgrades"), "upgrade")
	if err != nil {
		return Result{}, err
	}
	defer func() {
		if releaseErr := lock.Release(); releaseErr != nil && runErr == nil {
			runErr = fmt.Errorf("release upgrade lock: %w", releaseErr)
		}
	}()
	release, err := os.MkdirTemp("/tmp", "gpt-tunnel-upgrade-")
	if err != nil {
		return Result{}, err
	}
	defer func() {
		if cleanupErr := os.RemoveAll(release); cleanupErr != nil {
			if result.Status != "" {
				result.Status = "UPGRADE_ROLLBACK_FAILED"
				result.Error = "release cleanup failed"
			}
			runErr = fmt.Errorf("release cleanup failed: %w", cleanupErr)
		}
	}()
	if err := buildReleaseFn(ctx, root, release); err != nil {
		return Result{}, err
	}
	if err := tx.phase(r.Config, "backup"); err != nil {
		return Result{}, err
	}
	if err := validateReleaseFn(release, target); err != nil {
		return Result{}, err
	}
	paths := map[string]string{"gpt-tunnel-gatewayd": r.Config.Controller.GatewayBinary, "gpt-tunnel": filepath.Join(filepath.Dir(r.Config.Controller.GatewayBinary), "gpt-tunnel"), "gpt-tunnelctl": filepath.Join(filepath.Dir(r.Config.Controller.GatewayBinary), "gpt-tunnelctl")}
	backupDir, err := os.MkdirTemp(filepath.Join(r.Config.Controller.PIDDir, "upgrades"), "backup-")
	if err != nil {
		return Result{}, err
	}
	old := map[string][]byte{}
	oldHashes := map[string]string{}
	oldVersions := map[string]string{}
	targetHashes := map[string]string{}
	for _, name := range binaryOrder {
		dst := paths[name]
		b, e := os.ReadFile(dst)
		if e != nil {
			return Result{}, e
		}
		old[name] = b
		oldHashes[name] = hashBytes(b)
		oldVersions[name], err = installedVersion(dst)
		if err != nil {
			return Result{}, err
		}
		targetHashes[name], err = fileHash(filepath.Join(release, name))
		if err != nil {
			return Result{}, err
		}
		if e = fsutil.WriteFileAtomic(filepath.Join(backupDir, name), b, 0o700); e != nil {
			return Result{}, e
		}
		backup, e := os.ReadFile(filepath.Join(backupDir, name))
		if e != nil || hashBytes(backup) != oldHashes[name] {
			return Result{}, fmt.Errorf("backup checksum verification failed for %s", name)
		}
		tx.InstalledChecksumsBefore[name] = oldHashes[name]
		tx.ArtifactChecksums[name] = targetHashes[name]
	}
	tx.BackupPath, tx.RollbackAvailable = backupDir, true
	if err := tx.phase(r.Config, "migrate"); err != nil {
		return Result{}, err
	}
	// Inspect is the complete target-state gate. A successful inspection means
	// no persisted migration is required for this transaction; legacy state is
	// handled only by the explicit cutover/state-repair operations before
	// activation. Recording the no-op keeps the durable transaction phase
	// ordering explicit without adding a compatibility reader or hidden write.
	tx.MigrationOperations = append(tx.MigrationOperations, "target-state already validated; no persisted migration required")
	if err := tx.phase(r.Config, "validate"); err != nil {
		return Result{}, err
	}
	if err := replaceAll(release, paths, old); err != nil {
		return r.rollback(ctx, root, sha, target, installed, before, protectedPaths, protectedHashes, paths, old, oldHashes, oldVersions, backupDir, err)
	}
	if err := tx.phase(r.Config, "activate"); err != nil {
		return Result{}, err
	}
	startupDiagnostics, restartErr := restartGatewayAfterUpgrade(ctl)
	if restartErr != nil {
		tx.TargetStartup = targetStartupDiagnostics(startupDiagnostics)
		tx.PrimaryError = sanitizeError(restartErr)
		tx.CurrentPhase = "rollback_pending"
		if diagnosticErr := persistStartupDiagnosticsFn(r.Config, tx); diagnosticErr != nil {
			tx.TargetStartup.DiagnosticCaptureError = sanitizeError(diagnosticErr)
			tx.TargetStartup.CaptureStatus = "failed"
			restartErr = fmt.Errorf("target diagnostic persistence failed: %v; %w", diagnosticErr, restartErr)
			tx.PrimaryError = sanitizeError(restartErr)
			_ = writeTransaction(r.Config, tx)
		}
		return r.rollback(ctx, root, sha, target, installed, before, protectedPaths, protectedHashes, paths, old, oldHashes, oldVersions, backupDir, restartErr)
	}
	if err := tx.phase(r.Config, "verify"); err != nil {
		return r.rollback(ctx, root, sha, target, installed, before, protectedPaths, protectedHashes, paths, old, oldHashes, oldVersions, backupDir, err)
	}
	after, err := ctl.Status(ctx)
	if err != nil {
		return r.rollback(ctx, root, sha, target, installed, before, protectedPaths, protectedHashes, paths, old, oldHashes, oldVersions, backupDir, err)
	}
	if after.Gateway.PID == before.Gateway.PID || after.Tunnel.PID != before.Tunnel.PID || !after.Gateway.Running || !after.Tunnel.Running || !after.GatewayReady || !after.TunnelReady {
		return r.rollback(ctx, root, sha, target, installed, before, protectedPaths, protectedHashes, paths, old, oldHashes, oldVersions, backupDir, fmt.Errorf("post-upgrade process or readiness invariant failed"))
	}
	if err := ctl.Doctor(ctx); err != nil {
		return r.rollback(ctx, root, sha, target, installed, before, protectedPaths, protectedHashes, paths, old, oldHashes, oldVersions, backupDir, err)
	}
	if err := verifyInstalledProof(paths, target, targetHashes, protectedPaths, protectedHashes, before.Tunnel.PID, after); err != nil {
		return r.rollback(ctx, root, sha, target, installed, before, protectedPaths, protectedHashes, paths, old, oldHashes, oldVersions, backupDir, err)
	}
	if err := smokeFn(ctx, r.Config, target, installed); err != nil {
		return r.rollback(ctx, root, sha, target, installed, before, protectedPaths, protectedHashes, paths, old, oldHashes, oldVersions, backupDir, err)
	}
	if err := removeUpgradeBackup(backupDir); err != nil {
		return Result{}, fmt.Errorf("remove upgrade backup: %w", err)
	}
	tx.GatewayPIDAfter, tx.TunnelPIDAfter = after.Gateway.PID, after.Tunnel.PID
	tx.InstalledChecksumsAfter = targetHashes
	tx.ConfigSHAAfter, _ = fileHash(r.ConfigPath)
	tx.RollbackAvailable = false
	if hubRevision, hubErr := serviceHubRevision(ctx, r.Config); hubErr == nil {
		tx.NewHubSHA = hubRevision
	}
	if err := tx.complete(r.Config, "UPGRADE_COMPLETE"); err != nil {
		return Result{}, err
	}
	return Result{Status: "UPGRADE_COMPLETE", TransactionID: tx.TransactionID, SourceRoot: root, SourceSHA: sha, Previous: installed, Target: target, InstalledVersion: target, RunningVersion: after.RunningVersion, VersionMatch: after.VersionMatch, GatewayPID: after.Gateway.PID, TunnelPID: after.Tunnel.PID}, nil
}

func restartGatewayAfterUpgrade(ctl upgradeController) (controller.GatewayStartupDiagnostics, error) {
	if diagnosticCtl, ok := ctl.(startupDiagnosticController); ok {
		return diagnosticCtl.RestartGatewayAfterUpgradeDiagnostics()
	}
	err := ctl.RestartGatewayAfterUpgrade()
	return controller.GatewayStartupDiagnostics{ReadinessPassed: err == nil, Error: err}, err
}

func targetStartupDiagnostics(in controller.GatewayStartupDiagnostics) *TargetStartupDiagnostics {
	captureStatus := in.CaptureStatus
	if in.ProcessStateError != nil && captureStatus == "captured" {
		captureStatus = "partial"
	}
	d := &TargetStartupDiagnostics{
		Phase:                in.Phase,
		CaptureStatus:        captureStatus,
		TargetPID:            in.TargetPID,
		TargetProcessRunning: in.TargetProcessRunning,
		TargetProcessExited:  in.TargetProcessExited,
		ProcessStateError:    sanitizeError(in.ProcessStateError),
		AliveButUnready:      in.AliveButUnready,
		ElapsedMilliseconds:  in.Elapsed.Milliseconds(),
		ReadinessPassed:      in.ReadinessPassed,
		LogDelta:             sanitizeLogDelta(in.LogDelta),
		LogDeltaTruncated:    in.LogDeltaTruncated,
	}
	if in.Error != nil {
		d.Error = sanitizeError(in.Error)
	}
	if in.LogCaptureError != nil {
		d.DiagnosticCaptureError = sanitizeError(in.LogCaptureError)
	}
	return d
}

func sanitizeLogDelta(value string) string {
	lines := strings.Split(value, "\n")
	for i, line := range lines {
		lines[i] = sanitizeStatusText(line)
	}
	value = strings.Join(lines, "\n")
	const maxLogDelta = 16 << 10
	if len(value) > maxLogDelta {
		value = value[len(value)-maxLogDelta:]
		if newline := strings.IndexByte(value, '\n'); newline >= 0 {
			value = value[newline+1:]
		} else {
			value = ""
		}
	}
	return value
}

func (r Runner) rollback(ctx context.Context, root, sha, target, previous string, before controller.Status, protectedPaths []string, protectedHashes map[string]string, paths map[string]string, old map[string][]byte, oldHashes, oldVersions map[string]string, backupDir string, cause error) (Result, error) {
	if err := restoreAll(paths, old); err != nil {
		return Result{Status: "UPGRADE_ROLLBACK_FAILED", Error: "rollback restore failed"}, fmt.Errorf("rollback restore failed: %w", err)
	}
	if err := verifyHashes(paths, oldHashes); err != nil {
		return Result{Status: "UPGRADE_ROLLBACK_FAILED", Error: "rollback binary proof failed"}, err
	}
	for _, name := range binaryOrder {
		v, err := installedVersion(paths[name])
		if err != nil || v != oldVersions[name] {
			return Result{Status: "UPGRADE_ROLLBACK_FAILED", Error: "rollback version proof failed"}, fmt.Errorf("rollback version proof failed")
		}
	}
	ctl := r.ConfigController()
	if err := ctl.StopGatewayForUpgrade(); err != nil {
		return Result{Status: "UPGRADE_ROLLBACK_FAILED", Error: "rollback gateway stop failed"}, err
	}
	if err := ctl.RestartGatewayAfterUpgrade(); err != nil {
		return Result{Status: "UPGRADE_ROLLBACK_FAILED", Error: "rollback gateway restart failed"}, err
	}
	rolled, err := ctl.Status(ctx)
	if err != nil || !rolled.Gateway.Running || !rolled.Tunnel.Running || !rolled.GatewayReady || !rolled.TunnelReady || rolled.Tunnel.PID != before.Tunnel.PID || rolled.Gateway.PID == before.Gateway.PID {
		return Result{Status: "UPGRADE_ROLLBACK_FAILED", Error: "rollback process or readiness proof failed"}, fmt.Errorf("rollback process or readiness proof failed")
	}
	if err := ctl.Doctor(ctx); err != nil {
		return Result{Status: "UPGRADE_ROLLBACK_FAILED", Error: "rollback doctor proof failed"}, err
	}
	if err := verifyInstalledProof(paths, previous, oldHashes, protectedPaths, protectedHashes, before.Tunnel.PID, rolled); err != nil {
		return Result{Status: "UPGRADE_ROLLBACK_FAILED", Error: "rollback identity or protected-file proof failed"}, err
	}
	if err := smokeFn(ctx, r.Config, previous, target); err != nil {
		return Result{Status: "UPGRADE_ROLLBACK_FAILED", Error: "rollback MCP proof failed"}, err
	}
	if err := cleanupRollbackBackup(backupDir); err != nil {
		return Result{Status: "UPGRADE_ROLLBACK_FAILED", Error: "rollback backup cleanup failed"}, fmt.Errorf("rollback backup cleanup failed: %w", err)
	}
	return Result{Status: "UPGRADE_ROLLED_BACK", SourceRoot: root, SourceSHA: sha, Previous: previous, Target: target, GatewayPID: rolled.Gateway.PID, TunnelPID: rolled.Tunnel.PID, Rollback: true, Error: sanitizeError(cause)}, fmt.Errorf("upgrade rolled back")
}

func (r Runner) ConfigController() upgradeController {
	return newUpgradeControllerFn(r.Config, r.ConfigPath)
}
func serviceHubRevision(ctx context.Context, c config.Config) (string, error) {
	return service.New(c).Hub.RemoteRevision(ctx)
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
	if err := validateOwnedExecutable(c.Controller.TunnelClientBinary, "tunnel-client"); err != nil {
		return "", err
	}
	versions := make([]string, len(paths))
	for i, path := range paths {
		if err := validateOwnedExecutable(path, "installed binary"); err != nil {
			return "", err
		}
		var err error
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

func validateOwnedExecutable(path, label string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("%s is not available", label)
	}
	if !info.Mode().IsRegular() || info.Mode()&0o111 == 0 || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is not a regular executable", label)
	}
	if st, ok := info.Sys().(*syscall.Stat_t); !ok || st.Uid != uint32(os.Getuid()) {
		return fmt.Errorf("%s owner mismatch", label)
	}
	return nil
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
		message := strings.TrimSpace(string(out))
		if len(message) > 240 {
			message = message[:240]
		}
		return fmt.Errorf("release build failed: %s", message)
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

var (
	stageCopy           = stageOne
	stageRename         = os.Rename
	stageRemove         = os.Remove
	stageSyncDir        = syncDir
	removeUpgradeBackup = os.RemoveAll
)

func replaceAll(dir string, paths map[string]string, old map[string][]byte) error {
	staged := make(map[string]string, len(binaryOrder))
	cleanup := func() error {
		var first error
		for _, name := range binaryOrder {
			if path := staged[name]; path != "" {
				if err := stageRemove(path); err != nil && !os.IsNotExist(err) && first == nil {
					first = err
				}
			}
		}
		return first
	}
	for _, name := range binaryOrder {
		path, err := stageCopy(filepath.Join(dir, name), paths[name])
		if err != nil {
			if cleanErr := cleanup(); cleanErr != nil {
				return fmt.Errorf("stage failed: %v; cleanup failed: %w", err, cleanErr)
			}
			return err
		}
		srcHash, hashErr := fileHash(filepath.Join(dir, name))
		stagedHash, stagedErr := fileHash(path)
		if hashErr != nil || stagedErr != nil || srcHash != stagedHash {
			if cleanErr := cleanup(); cleanErr != nil {
				return fmt.Errorf("staged checksum verification failed; cleanup failed: %w", cleanErr)
			}
			return fmt.Errorf("staged checksum verification failed for %s", name)
		}
		if _, err := installedVersion(path); err != nil {
			if cleanErr := cleanup(); cleanErr != nil {
				return fmt.Errorf("staged version verification failed; cleanup failed: %w", cleanErr)
			}
			return fmt.Errorf("staged version verification failed for %s", name)
		}
		staged[name] = path
	}
	for _, name := range binaryOrder {
		if err := stageRename(staged[name], paths[name]); err != nil {
			restoreErr := restoreAll(paths, old)
			cleanErr := cleanup()
			if restoreErr != nil {
				return fmt.Errorf("commit failed: %v; restore failed: %w", err, restoreErr)
			}
			if cleanErr != nil {
				return fmt.Errorf("commit failed: %v; staging cleanup failed: %w", err, cleanErr)
			}
			return err
		}
		staged[name] = ""
		if err := stageSyncDir(filepath.Dir(paths[name])); err != nil {
			restoreErr := restoreAll(paths, old)
			if restoreErr != nil {
				cleanErr := cleanup()
				if cleanErr != nil {
					return fmt.Errorf("commit directory sync failed: %v; restore failed: %w; cleanup failed: %v", err, restoreErr, cleanErr)
				}
				return fmt.Errorf("commit directory sync failed: %v; restore failed: %w", err, restoreErr)
			}
			if cleanErr := cleanup(); cleanErr != nil {
				return fmt.Errorf("commit directory sync failed: %v; cleanup failed: %w", err, cleanErr)
			}
			return err
		}
	}
	return cleanup()
}

func stageOne(src, dst string) (string, error) {
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	data, readErr := io.ReadAll(in)
	closeInErr := in.Close()
	if readErr != nil {
		return "", readErr
	}
	if closeInErr != nil {
		return "", closeInErr
	}
	out, err := os.CreateTemp(filepath.Dir(dst), ".gpt-tunnel-upgrade-stage-")
	if err != nil {
		return "", err
	}
	path := out.Name()
	cleanup := func(primary error) (string, error) {
		closeErr := out.Close()
		removeErr := stageRemove(path)
		if primary != nil {
			if closeErr != nil {
				primary = fmt.Errorf("%w; close failed: %v", primary, closeErr)
			}
			if removeErr != nil && !os.IsNotExist(removeErr) {
				primary = fmt.Errorf("%w; remove failed: %v", primary, removeErr)
			}
			return "", primary
		}
		if closeErr != nil {
			return "", closeErr
		}
		if removeErr != nil {
			return "", removeErr
		}
		return "", nil
	}
	if _, err := out.Write(data); err != nil {
		return cleanup(err)
	}
	if err := out.Chmod(0o755); err != nil {
		return cleanup(err)
	}
	if err := out.Sync(); err != nil {
		return cleanup(err)
	}
	if err := out.Close(); err != nil {
		return cleanup(err)
	}
	if _, err := os.Stat(path); err != nil {
		removeErr := stageRemove(path)
		if removeErr != nil && !os.IsNotExist(removeErr) {
			return "", fmt.Errorf("staged file stat failed: %v; remove failed: %w", err, removeErr)
		}
		return "", err
	}
	return path, nil
}

func syncDir(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	syncErr := f.Sync()
	closeErr := f.Close()
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}

func restoreAll(paths map[string]string, old map[string][]byte) error {
	var first error
	for _, name := range binaryOrder {
		dst := paths[name]
		if err := writeAtomicStrict(dst, old[name]); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func writeAtomicStrict(dst string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(dst), ".gpt-tunnel-restore-")
	if err != nil {
		return err
	}
	tmp := f.Name()
	cleanup := func(primary error) error {
		closeErr := f.Close()
		removeErr := os.Remove(tmp)
		if primary != nil {
			if closeErr != nil {
				primary = fmt.Errorf("%w; close failed: %v", primary, closeErr)
			}
			if removeErr != nil && !os.IsNotExist(removeErr) {
				primary = fmt.Errorf("%w; remove failed: %v", primary, removeErr)
			}
			return primary
		}
		if closeErr != nil {
			return closeErr
		}
		return removeErr
	}
	if _, err = f.Write(data); err != nil {
		return cleanup(err)
	}
	if err = f.Chmod(0o755); err != nil {
		return cleanup(err)
	}
	if err = f.Sync(); err != nil {
		return cleanup(err)
	}
	if err = f.Close(); err != nil {
		removeErr := os.Remove(tmp)
		if removeErr != nil && !os.IsNotExist(removeErr) {
			return fmt.Errorf("%w; remove failed: %v", err, removeErr)
		}
		return err
	}
	if err = os.Rename(tmp, dst); err != nil {
		removeErr := os.Remove(tmp)
		if removeErr != nil && !os.IsNotExist(removeErr) {
			return fmt.Errorf("%w; remove failed: %v", err, removeErr)
		}
		return err
	}
	return syncDir(filepath.Dir(dst))
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
func verifyInstalledProof(paths map[string]string, version string, expectedHashes map[string]string, protectedPaths []string, protectedHashes map[string]string, tunnelPID int, status controller.Status) error {
	for _, name := range binaryOrder {
		got, err := fileHash(paths[name])
		if err != nil {
			return err
		}
		if got != expectedHashes[name] {
			return fmt.Errorf("binary hash proof failed")
		}
		v, err := installedVersion(paths[name])
		if err != nil || v != version {
			return fmt.Errorf("binary version proof failed")
		}
	}
	if status.Tunnel.PID != tunnelPID || !status.Gateway.Running || !status.Gateway.IdentityValid || status.Gateway.Executable != paths["gpt-tunnel-gatewayd"] || status.RunningVersion != version || !status.VersionMatch {
		return fmt.Errorf("runtime identity proof failed")
	}
	for _, path := range protectedPaths {
		got, err := fileHash(path)
		if err != nil || got != protectedHashes[path] {
			return fmt.Errorf("protected runtime hash changed")
		}
	}
	return nil
}

func sanitizeError(err error) string {
	if err == nil {
		return ""
	}
	s := sanitizeStatusText(err.Error())
	if len(s) > 240 {
		s = s[:240]
	}
	return s
}

func smoke(ctx context.Context, c config.Config, expectedVersion, previousVersion string) error {
	url := "http://" + c.ListenAddr + "/mcp"
	call := func(id int, method string, params any) (map[string]any, error) {
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
		callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		req, _ := http.NewRequestWithContext(callCtx, http.MethodPost, url, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		resp, e := (&http.Client{Timeout: 5 * time.Second}).Do(req)
		if e != nil {
			return nil, e
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("MCP HTTP status %d", resp.StatusCode)
		}
		var v map[string]any
		e = json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&v)
		if e != nil || v["jsonrpc"] != "2.0" || v["error"] != nil {
			return nil, fmt.Errorf("invalid MCP JSON-RPC response")
		}
		gotID, ok := v["id"].(float64)
		if !ok || int(gotID) != id {
			return nil, fmt.Errorf("MCP response id mismatch")
		}
		if _, ok := v["result"].(map[string]any); !ok {
			return nil, fmt.Errorf("MCP result missing")
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
	protocolVersion, protocolOK := result["protocolVersion"].(string)
	if !ok || info["version"] != expectedVersion || !protocolOK || protocolVersion == "" {
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
		name, ok := tool["name"].(string)
		if !ok || name == "" {
			return fmt.Errorf("tool name missing")
		}
		inputSchema, ok := tool["inputSchema"].(map[string]any)
		if !ok || inputSchema["type"] != "object" {
			return fmt.Errorf("tool input schema missing")
		}
		outputSchema, ok := tool["outputSchema"].(map[string]any)
		if !ok || outputSchema["type"] != "object" {
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
	isError, isErrorOK := pingResult["isError"].(bool)
	if !isErrorOK || isError {
		return fmt.Errorf("MCP ping returned error")
	}
	pingContent, ok := pingResult["structuredContent"].(map[string]any)
	if !ok || pingContent["version"] != expectedVersion || pingContent["gateway_id"] != c.GatewayID || pingContent["service"] != "gpt-tunnel-gatewayd" {
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
	capError, capErrorOK := capResult["isError"].(bool)
	if !capErrorOK || capError {
		return fmt.Errorf("MCP capabilities returned error")
	}
	structured, ok := capResult["structuredContent"].(map[string]any)
	if !ok {
		return fmt.Errorf("MCP capabilities structured content missing")
	}
	if structured["gateway_id"] != c.GatewayID || structured["hub_protocol_root"] != "gpt-tunnel/v1" || structured["hub_branch"] != c.Hub.Branch || structured["hub_managed_root"] != filepath.Join(c.StateDir, "hub", "repository") || expectedVersion == previousVersion {
		return fmt.Errorf("MCP capabilities mismatch")
	}
	return nil
}
