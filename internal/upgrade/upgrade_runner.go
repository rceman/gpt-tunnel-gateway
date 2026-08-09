package upgrade

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
)

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
		return Result{
			Status:           "UPGRADE_BLOCKED",
			SourceRoot:       root,
			SourceSHA:        sha,
			Previous:         installed,
			Target:           target,
			Blockers:         preflight.Blockers,
			GatewayPID:       preflight.GatewayPID,
			TunnelPID:        preflight.TunnelPID,
			InstalledVersion: installed,
			RunningVersion:   preflight.RunningVersion,
			VersionMatch:     preflight.VersionMatch,
		}, fmt.Errorf("upgrade preflight blocked by %d issue(s)", len(preflight.Blockers))
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
	return Result{
		Status:           "UPGRADE_COMPLETE",
		TransactionID:    tx.TransactionID,
		SourceRoot:       root,
		SourceSHA:        sha,
		Previous:         installed,
		Target:           target,
		InstalledVersion: target,
		RunningVersion:   after.RunningVersion,
		VersionMatch:     after.VersionMatch,
		GatewayPID:       after.Gateway.PID,
		TunnelPID:        after.Tunnel.PID,
	}, nil
}

func (r Runner) ConfigController() upgradeController {
	return newUpgradeControllerFn(r.Config, r.ConfigPath)
}
