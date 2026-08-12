package controller

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
)

const (
	maxGatewayRecoveryAttempts = 3
	recoveryStateFile          = "gateway-recovery.json"
)

type gatewayRecoveryState struct {
	Status    string    `json:"status"`
	Attempts  int       `json:"attempts"`
	Reason    string    `json:"reason,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (c Controller) gatewayRecoveryPath() string {
	return filepath.Join(c.Config.Controller.PIDDir, recoveryStateFile)
}

func (c Controller) writeGatewayRecoveryState(state gatewayRecoveryState) error {
	if err := fsutil.EnsureDir(c.Config.Controller.PIDDir, 0o700); err != nil {
		return err
	}
	return fsutil.WriteJSONAtomic(c.gatewayRecoveryPath(), state, 0o600)
}

func (c Controller) readGatewayRecoveryState() (gatewayRecoveryState, error) {
	var state gatewayRecoveryState
	err := fsutil.ReadJSONBounded(c.gatewayRecoveryPath(), 16<<10, &state)
	if os.IsNotExist(err) {
		return gatewayRecoveryState{}, nil
	}
	return state, err
}

func (c Controller) Supervise(ctx context.Context) error {
	status, err := recoveryStatusFn(ctx, c)
	if err != nil {
		return err
	}
	if status.Gateway.Running && status.GatewayReady && status.Tunnel.Running && status.TunnelReady && status.VersionMatch {
		return c.writeGatewayRecoveryState(gatewayRecoveryState{
			Status:    "healthy",
			UpdatedAt: time.Now().UTC(),
		})
	}
	if !status.Tunnel.Running || !status.Tunnel.IdentityValid || !status.TunnelReady {
		return c.markGatewayDegraded(0, "healthy Tunnel is required for Gateway-only recovery")
	}
	if status.Gateway.Running {
		return c.markGatewayDegraded(0, "Gateway is running but not ready; refusing automatic restart")
	}
	return c.RecoverGateway(ctx)
}

func (c Controller) RecoverGateway(ctx context.Context) error {
	lock, err := lockfile.Acquire(c.Config.Controller.PIDDir, "controller-recovery")
	if err != nil {
		return err
	}
	defer lock.Release()

	initial, err := recoveryStatusFn(ctx, c)
	if err != nil {
		return err
	}
	if initial.Gateway.Running && initial.GatewayReady && initial.Tunnel.Running && initial.TunnelReady && initial.VersionMatch {
		return c.writeGatewayRecoveryState(gatewayRecoveryState{
			Status:    "healthy",
			UpdatedAt: time.Now().UTC(),
		})
	}
	if !initial.Tunnel.Running || !initial.Tunnel.IdentityValid || !initial.TunnelReady {
		return c.markGatewayDegraded(0, "healthy Tunnel is required for Gateway-only recovery")
	}
	if initial.Gateway.Running {
		return c.markGatewayDegraded(0, "Gateway is running but not ready; refusing automatic restart")
	}
	tunnelPID := initial.Tunnel.PID
	var lastErr error
	for attempt := 1; attempt <= maxGatewayRecoveryAttempts; attempt++ {
		if attempt > 1 {
			recoverySleepFn(time.Duration(attempt-1) * 100 * time.Millisecond)
		}
		if err := recoveryStartFn(c); err != nil {
			lastErr = err
			continue
		}
		if err := recoveryReadyFn(c); err != nil {
			lastErr = err
			_ = recoveryStopFn(c)
			continue
		}
		final, statusErr := recoveryStatusFn(ctx, c)
		if statusErr != nil {
			lastErr = statusErr
			_ = recoveryStopFn(c)
			continue
		}
		if final.Tunnel.PID != tunnelPID || !final.Tunnel.Running || !final.Tunnel.IdentityValid || !final.TunnelReady {
			lastErr = fmt.Errorf("Tunnel identity changed during Gateway recovery")
			_ = recoveryStopFn(c)
			continue
		}
		if final.Gateway.Running && final.Gateway.IdentityValid && final.GatewayReady && final.VersionMatch {
			return c.writeGatewayRecoveryState(gatewayRecoveryState{
				Status:    "recovered",
				Attempts:  attempt,
				UpdatedAt: time.Now().UTC(),
			})
		}
		lastErr = fmt.Errorf("Gateway did not converge to ready")
		_ = recoveryStopFn(c)
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("Gateway recovery exhausted")
	}
	return c.markGatewayDegraded(maxGatewayRecoveryAttempts, lastErr.Error())
}

func (c Controller) markGatewayDegraded(attempts int, reason string) error {
	state := gatewayRecoveryState{
		Status:    "degraded",
		Attempts:  attempts,
		Reason:    reason,
		UpdatedAt: time.Now().UTC(),
	}
	if err := c.writeGatewayRecoveryState(state); err != nil {
		return fmt.Errorf("Gateway degraded (%s); persist state: %w", reason, err)
	}
	return fmt.Errorf("Gateway degraded after %d recovery attempts: %s", attempts, reason)
}
