package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
	"github.com/rceman/gpt-tunnel-gateway/internal/runtime_log"
)

// GatewayRecoveryResult is the bounded, server-owned receipt for one Gateway
// handoff. The Tunnel is observed only and is never restarted by this path.
type GatewayRecoveryResult struct {
	OperationID  string `json:"operation_id"`
	OldPID       int    `json:"old_pid"`
	NewPID       int    `json:"new_pid"`
	TunnelPID    int    `json:"tunnel_pid"`
	GatewayReady bool   `json:"gateway_ready"`
	Outcome      string `json:"outcome"`
}

func (c Controller) RestartGatewayRecovery(operationID string) (GatewayRecoveryResult, error) {
	if operationID == "" {
		operationID = "gateway-recovery-" + strconv.FormatInt(time.Now().UTC().UnixNano(), 10)
	}
	if len(operationID) > runtime_log.MaxIdentifierBytes {
		return GatewayRecoveryResult{}, fmt.Errorf("operation_id exceeds %d bytes", runtime_log.MaxIdentifierBytes)
	}
	lock, err := lockfile.Acquire(c.Config.Controller.PIDDir, "controller")
	if err != nil {
		return GatewayRecoveryResult{}, err
	}
	defer lock.Release()
	path := gatewayRecoveryPath(c.Config.StateDir, operationID)
	var prior GatewayRecoveryResult
	if err := fsutil.ReadJSONBounded(path, 16<<10, &prior); err == nil && prior.OperationID == operationID && prior.Outcome == "succeeded" {
		return prior, nil
	}
	workingDir, err := c.gatewayWorkingDir()
	if err != nil {
		return GatewayRecoveryResult{
			OperationID: operationID,
			Outcome:     "failed",
		}, err
	}
	old := c.process("gateway", mustEval(c.Config.Controller.GatewayBinary))
	tunnel := c.process("tunnel", mustEval(c.Config.Controller.TunnelClientBinary))
	result := GatewayRecoveryResult{
		OperationID: operationID,
		OldPID:      old.PID,
		TunnelPID:   tunnel.PID,
		Outcome:     "in_progress",
	}
	if err := fsutil.WriteJSONAtomic(path, result, 0o600); err != nil {
		return GatewayRecoveryResult{}, err
	}
	c.runtimeEvent(runtime_log.Event{Timestamp: time.Now().UTC(), Level: "info", Component: "gateway", Event: "recovery_start", OperationID: operationID, PID: old.PID, Message: "gateway recovery started"})
	finish := func(outcome string, cause error) (GatewayRecoveryResult, error) {
		result.Outcome = outcome
		if current := c.process("gateway", mustEval(c.Config.Controller.GatewayBinary)); current.Running {
			result.NewPID = current.PID
		}
		result.GatewayReady = checkURL(context.Background(), c.gatewayReadyURL())
		_ = fsutil.WriteJSONAtomic(path, result, 0o600)
		level := "info"
		if cause != nil {
			level = "warn"
		}
		c.runtimeEvent(runtime_log.Event{Timestamp: time.Now().UTC(), Level: level, Component: "gateway", Event: "recovery_finish", OperationID: operationID, PID: result.NewPID, Message: outcome})
		return result, cause
	}
	backup := filepath.Join(c.Config.Controller.PIDDir, "gateway.recovery-backup-"+strconv.Itoa(old.PID))
	if old.Running {
		if err := copyExecutable(filepath.Join("/proc", strconv.Itoa(old.PID), "exe"), backup); err != nil {
			return finish("failed", fmt.Errorf("snapshot running gateway: %w", err))
		}
		defer os.Remove(backup)
	}
	if err := c.stopProcess("gateway", c.Config.Controller.GatewayBinary); err != nil {
		return finish("failed", err)
	}
	startErr := c.startProcess("gateway", c.Config.Controller.GatewayBinary, []string{"--config", c.ConfigPath}, []string{"GPT_TUNNEL_CONFIG=" + c.ConfigPath})
	if startErr == nil {
		startErr = waitURL(c.gatewayReadyURL(), true, 30*time.Second)
	}
	if startErr == nil && c.process("tunnel", mustEval(c.Config.Controller.TunnelClientBinary)).PID != tunnel.PID {
		startErr = fmt.Errorf("gateway recovery changed the Tunnel process")
	}
	if startErr == nil {
		_ = workingDir
		return finish("succeeded", nil)
	}
	_ = c.stopProcess("gateway", c.Config.Controller.GatewayBinary)
	if old.Running {
		if err := copyExecutable(backup, c.Config.Controller.GatewayBinary); err == nil {
			if err = c.startProcess("gateway", c.Config.Controller.GatewayBinary, []string{"--config", c.ConfigPath}, []string{"GPT_TUNNEL_CONFIG=" + c.ConfigPath}); err == nil {
				_ = waitURL(c.gatewayReadyURL(), true, 30*time.Second)
			}
		}
	}
	return finish("failed", startErr)
}

func gatewayRecoveryPath(stateDir, operationID string) string {
	digest := sha256.Sum256([]byte(operationID))
	return filepath.Join(stateDir, "gateway-recovery", hex.EncodeToString(digest[:])+".json")
}
