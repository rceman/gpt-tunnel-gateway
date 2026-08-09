package upgrade

import (
	"context"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/controller"
)

func cleanupRollbackBackup(path string) error {
	if path == "" {
		return fmt.Errorf("rollback backup missing")
	}
	return removeUpgradeBackup(path)
}

func (r Runner) rollback(ctx context.Context, root, sha, target, previous string, before controller.Status, protectedPaths []string, protectedHashes map[string]string, paths map[string]string, old map[string][]byte, oldHashes, oldVersions map[string]string, backupDir string, cause error) (Result, error) {
	if err := restoreAll(paths, old); err != nil {
		return Result{
			Status: "UPGRADE_ROLLBACK_FAILED",
			Error:  "rollback restore failed",
		}, fmt.Errorf("rollback restore failed: %w", err)
	}
	if err := verifyHashes(paths, oldHashes); err != nil {
		return Result{
			Status: "UPGRADE_ROLLBACK_FAILED",
			Error:  "rollback binary proof failed",
		}, err
	}
	for _, name := range binaryOrder {
		v, err := installedVersion(paths[name])
		if err != nil || v != oldVersions[name] {
			return Result{
				Status: "UPGRADE_ROLLBACK_FAILED",
				Error:  "rollback version proof failed",
			}, fmt.Errorf("rollback version proof failed")
		}
	}
	ctl := r.ConfigController()
	if err := ctl.StopGatewayForUpgrade(); err != nil {
		return Result{
			Status: "UPGRADE_ROLLBACK_FAILED",
			Error:  "rollback gateway stop failed",
		}, err
	}
	if err := ctl.RestartGatewayAfterUpgrade(); err != nil {
		return Result{
			Status: "UPGRADE_ROLLBACK_FAILED",
			Error:  "rollback gateway restart failed",
		}, err
	}
	rolled, err := ctl.Status(ctx)
	if err != nil || !rolled.Gateway.Running || !rolled.Tunnel.Running || !rolled.GatewayReady || !rolled.TunnelReady || rolled.Tunnel.PID != before.Tunnel.PID || rolled.Gateway.PID == before.Gateway.PID {
		return Result{
			Status: "UPGRADE_ROLLBACK_FAILED",
			Error:  "rollback process or readiness proof failed",
		}, fmt.Errorf("rollback process or readiness proof failed")
	}
	if err := ctl.Doctor(ctx); err != nil {
		return Result{
			Status: "UPGRADE_ROLLBACK_FAILED",
			Error:  "rollback doctor proof failed",
		}, err
	}
	if err := verifyInstalledProof(paths, previous, oldHashes, protectedPaths, protectedHashes, before.Tunnel.PID, rolled); err != nil {
		return Result{
			Status: "UPGRADE_ROLLBACK_FAILED",
			Error:  "rollback identity or protected-file proof failed",
		}, err
	}
	if err := smokeFn(ctx, r.Config, previous, target); err != nil {
		return Result{
			Status: "UPGRADE_ROLLBACK_FAILED",
			Error:  "rollback MCP proof failed",
		}, err
	}
	if err := cleanupRollbackBackup(backupDir); err != nil {
		return Result{
			Status: "UPGRADE_ROLLBACK_FAILED",
			Error:  "rollback backup cleanup failed",
		}, fmt.Errorf("rollback backup cleanup failed: %w", err)
	}
	return Result{
		Status:     "UPGRADE_ROLLED_BACK",
		SourceRoot: root,
		SourceSHA:  sha,
		Previous:   previous,
		Target:     target,
		GatewayPID: rolled.Gateway.PID,
		TunnelPID:  rolled.Tunnel.PID,
		Rollback:   true,
		Error:      sanitizeError(cause),
	}, fmt.Errorf("upgrade rolled back")
}
