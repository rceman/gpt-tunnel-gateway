package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
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

type gatewayRecoveryReceipt struct {
	GatewayRecoveryResult
	Error string `json:"error,omitempty"`
}

// GatewayRecoveryFailure is returned when a retry observes a durable terminal
// failure. Its identity and message are stable across Gateway restarts.
type GatewayRecoveryFailure struct {
	OperationID string
	Cause       string
}

func (e GatewayRecoveryFailure) Error() string {
	if e.Cause == "" {
		return fmt.Sprintf("gateway recovery %s failed", e.OperationID)
	}
	return fmt.Sprintf("gateway recovery %s failed: %s", e.OperationID, e.Cause)
}

func (e GatewayRecoveryFailure) StructuredActionError() map[string]any {
	return map[string]any{
		"code":         "GATEWAY_RECOVERY_FAILED",
		"operation_id": e.OperationID,
		"message":      e.Cause,
	}
}

func normalizeGatewayRecoveryOperationID(operationID string) (string, error) {
	if operationID == "" {
		operationID = "gateway-recovery-" + strconv.FormatInt(time.Now().UTC().UnixNano(), 10)
	}
	if len(operationID) > runtime_log.MaxIdentifierBytes {
		return "", fmt.Errorf("operation_id exceeds %d bytes", runtime_log.MaxIdentifierBytes)
	}
	return operationID, nil
}

func readGatewayRecoveryReceipt(path, operationID string) (gatewayRecoveryReceipt, bool, error) {
	var receipt gatewayRecoveryReceipt
	if err := fsutil.ReadJSONBounded(path, 16<<10, &receipt); err != nil {
		if os.IsNotExist(err) {
			return gatewayRecoveryReceipt{}, false, nil
		}
		return gatewayRecoveryReceipt{}, false, err
	}
	if receipt.OperationID != operationID {
		return gatewayRecoveryReceipt{}, false, fmt.Errorf("gateway recovery receipt identity mismatch")
	}
	return receipt, true, nil
}

func gatewayRecoveryFailure(receipt gatewayRecoveryReceipt) error {
	return GatewayRecoveryFailure{OperationID: receipt.OperationID, Cause: receipt.Error}
}

func scheduleGatewayRecoveryWorker(c Controller, operationID string, release func(func())) {
	release(func() {
		if err := gatewayRecoveryWorkerLaunchFn(c, operationID); err != nil {
			c.recordGatewayRecoveryLaunchFailure(operationID, err)
		}
	})
}

// AcceptGatewayRecovery durably accepts a Gateway-only handoff without
// stopping the serving process. The caller must provide a response-release
// hook so the detached worker cannot terminate this HTTP server before the
// accepted receipt is flushed to the caller.
func (c Controller) AcceptGatewayRecovery(operationID string, release func(func())) (GatewayRecoveryResult, error) {
	if release == nil {
		return GatewayRecoveryResult{}, fmt.Errorf("gateway recovery requires an HTTP response release boundary")
	}
	operationID, err := normalizeGatewayRecoveryOperationID(operationID)
	if err != nil {
		return GatewayRecoveryResult{}, err
	}
	path := gatewayRecoveryPath(c.Config.StateDir, operationID)
	prior, exists, err := readGatewayRecoveryReceipt(path, operationID)
	if err != nil {
		return GatewayRecoveryResult{}, err
	}
	if exists {
		switch prior.Outcome {
		case "succeeded":
			return prior.GatewayRecoveryResult, nil
		case "failed":
			return prior.GatewayRecoveryResult, gatewayRecoveryFailure(prior)
		case "accepted", "in_progress":
			scheduleGatewayRecoveryWorker(c, operationID, release)
			return prior.GatewayRecoveryResult, nil
		default:
			return GatewayRecoveryResult{}, fmt.Errorf("gateway recovery receipt has invalid outcome")
		}
	}

	lock, err := lockfile.Acquire(c.Config.Controller.PIDDir, "controller")
	if err != nil {
		prior, exists, readErr := readGatewayRecoveryReceipt(path, operationID)
		if readErr != nil {
			return GatewayRecoveryResult{}, readErr
		}
		if exists && (prior.Outcome == "accepted" || prior.Outcome == "in_progress") {
			scheduleGatewayRecoveryWorker(c, operationID, release)
			return prior.GatewayRecoveryResult, nil
		}
		if exists && prior.Outcome == "succeeded" {
			return prior.GatewayRecoveryResult, nil
		}
		return GatewayRecoveryResult{}, err
	}
	prior, exists, err = readGatewayRecoveryReceipt(path, operationID)
	if err != nil {
		_ = lock.Release()
		return GatewayRecoveryResult{}, err
	}
	if exists {
		_ = lock.Release()
		switch prior.Outcome {
		case "succeeded":
			return prior.GatewayRecoveryResult, nil
		case "failed":
			return prior.GatewayRecoveryResult, gatewayRecoveryFailure(prior)
		case "accepted", "in_progress":
			scheduleGatewayRecoveryWorker(c, operationID, release)
			return prior.GatewayRecoveryResult, nil
		default:
			return GatewayRecoveryResult{}, fmt.Errorf("gateway recovery receipt has invalid outcome")
		}
	}
	old := c.process("gateway", mustEval(c.Config.Controller.GatewayBinary))
	tunnel := c.process("tunnel", mustEval(c.Config.Controller.TunnelClientBinary))
	result := GatewayRecoveryResult{
		OperationID:  operationID,
		OldPID:       old.PID,
		TunnelPID:    tunnel.PID,
		GatewayReady: false,
		Outcome:      "accepted",
	}
	if err := fsutil.WriteJSONAtomic(path, gatewayRecoveryReceipt{GatewayRecoveryResult: result}, 0o600); err != nil {
		_ = lock.Release()
		return GatewayRecoveryResult{}, err
	}
	c.runtimeEvent(runtime_log.Event{Timestamp: time.Now().UTC(), Level: "info", Component: "gateway", Event: "recovery_accepted", OperationID: operationID, PID: old.PID, Message: "gateway recovery accepted; awaiting response release"})
	_ = lock.Release()
	scheduleGatewayRecoveryWorker(c, operationID, release)
	return result, nil
}

func (c Controller) launchGatewayRecoveryWorker(operationID string) error {
	binary := c.Config.Controller.GatewayBinary
	if binary == "" {
		return fmt.Errorf("gateway binary is not configured")
	}
	if _, err := filepath.EvalSymlinks(binary); err != nil {
		return fmt.Errorf("resolve gateway recovery worker: %w", err)
	}
	workingDir, err := c.gatewayWorkingDir()
	if err != nil {
		return err
	}
	if err := fsutil.EnsureDir(c.Config.Controller.LogDir, 0o700); err != nil {
		return err
	}
	log, err := os.OpenFile(filepath.Join(c.Config.Controller.LogDir, "gateway-recovery-worker.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer log.Close()
	cmd := exec.Command(binary, "--config", c.ConfigPath, "--gateway-recovery-worker", operationID)
	cmd.Dir = workingDir
	cmd.Env = processEnv([]string{"GPT_TUNNEL_CONFIG=" + c.ConfigPath})
	cmd.Stdin = nil
	cmd.Stdout = log
	cmd.Stderr = log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start gateway recovery worker: %w", err)
	}
	// The worker owns its own lifecycle and will outlive the serving Gateway.
	// Release the parent-side process handle; the worker's controller lock is
	// the single-flight/idempotency barrier for the actual restart.
	_ = cmd.Process.Release()
	return nil
}

func (c Controller) recordGatewayRecoveryLaunchFailure(operationID string, cause error) {
	lock, err := lockfile.Acquire(c.Config.Controller.PIDDir, "controller")
	if err != nil {
		return
	}
	defer lock.Release()
	path := gatewayRecoveryPath(c.Config.StateDir, operationID)
	receipt, exists, err := readGatewayRecoveryReceipt(path, operationID)
	if err != nil || !exists || receipt.Outcome == "succeeded" || receipt.Outcome == "failed" {
		return
	}
	receipt.Outcome = "failed"
	receipt.Error = boundedGatewayRecoveryError(cause)
	if err := fsutil.WriteJSONAtomic(path, receipt, 0o600); err != nil {
		return
	}
	c.runtimeEvent(runtime_log.Event{Timestamp: time.Now().UTC(), Level: "error", Component: "gateway", Event: "recovery_failed", OperationID: operationID, Message: "gateway recovery worker could not start", Error: receipt.Error})
}

func boundedGatewayRecoveryError(err error) string {
	if err == nil {
		return ""
	}
	const maxBytes = 2048
	message := err.Error()
	if len(message) > maxBytes {
		return message[:maxBytes]
	}
	return message
}

func (c Controller) RestartGatewayRecovery(operationID string) (GatewayRecoveryResult, error) {
	operationID, err := normalizeGatewayRecoveryOperationID(operationID)
	if err != nil {
		return GatewayRecoveryResult{}, err
	}
	lock, err := lockfile.Acquire(c.Config.Controller.PIDDir, "controller")
	if err != nil {
		return GatewayRecoveryResult{}, err
	}
	defer lock.Release()
	path := gatewayRecoveryPath(c.Config.StateDir, operationID)
	prior, exists, err := readGatewayRecoveryReceipt(path, operationID)
	if err != nil {
		return GatewayRecoveryResult{}, err
	}
	if exists {
		switch prior.Outcome {
		case "succeeded":
			return prior.GatewayRecoveryResult, nil
		case "failed":
			return prior.GatewayRecoveryResult, gatewayRecoveryFailure(prior)
		case "accepted", "in_progress":
			// A worker crash releases the controller lock. Taking it here is
			// the bounded takeover path for the durable accepted intent.
		default:
			return GatewayRecoveryResult{}, fmt.Errorf("gateway recovery receipt has invalid outcome")
		}
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
	if err := fsutil.WriteJSONAtomic(path, gatewayRecoveryReceipt{GatewayRecoveryResult: result}, 0o600); err != nil {
		return GatewayRecoveryResult{}, err
	}
	c.runtimeEvent(runtime_log.Event{Timestamp: time.Now().UTC(), Level: "info", Component: "gateway", Event: "recovery_start", OperationID: operationID, PID: old.PID, Message: "gateway recovery started"})
	finish := func(outcome string, cause error) (GatewayRecoveryResult, error) {
		result.Outcome = outcome
		if current := c.process("gateway", mustEval(c.Config.Controller.GatewayBinary)); current.Running {
			result.NewPID = current.PID
		}
		result.GatewayReady = checkURL(context.Background(), c.gatewayReadyURL())
		receipt := gatewayRecoveryReceipt{GatewayRecoveryResult: result}
		if cause != nil {
			receipt.Error = boundedGatewayRecoveryError(cause)
		}
		if writeErr := fsutil.WriteJSONAtomic(path, receipt, 0o600); writeErr != nil {
			if cause == nil {
				cause = fmt.Errorf("persist gateway recovery result: %w", writeErr)
				result.Outcome = "failed"
				receipt.GatewayRecoveryResult = result
				receipt.Error = boundedGatewayRecoveryError(cause)
				_ = fsutil.WriteJSONAtomic(path, receipt, 0o600)
			}
		}
		level := "info"
		if cause != nil {
			level = "warn"
		}
		c.runtimeEvent(runtime_log.Event{Timestamp: time.Now().UTC(), Level: level, Component: "gateway", Event: "recovery_finish", OperationID: operationID, PID: result.NewPID, Message: result.Outcome})
		return result, cause
	}
	backup := filepath.Join(c.Config.Controller.PIDDir, "gateway.recovery-backup-"+strconv.Itoa(old.PID))
	if old.Running {
		if err := copyExecutable(filepath.Join("/proc", strconv.Itoa(old.PID), "exe"), backup); err != nil {
			return finish("failed", fmt.Errorf("snapshot running gateway: %w", err))
		}
		defer os.Remove(backup)
	}
	if err := gatewayRecoveryStopFn(c); err != nil {
		return finish("failed", err)
	}
	startErr := gatewayRecoveryStartFn(c)
	if startErr == nil {
		startErr = gatewayRecoveryWaitFn(c.gatewayReadyURL(), true, 30*time.Second)
	}
	if startErr == nil && c.process("tunnel", mustEval(c.Config.Controller.TunnelClientBinary)).PID != tunnel.PID {
		startErr = fmt.Errorf("gateway recovery changed the Tunnel process")
	}
	if startErr == nil {
		_ = workingDir
		return finish("succeeded", nil)
	}
	_ = gatewayRecoveryStopFn(c)
	if old.Running {
		if err := copyExecutable(backup, c.Config.Controller.GatewayBinary); err == nil {
			if err = gatewayRecoveryStartFn(c); err == nil {
				_ = gatewayRecoveryWaitFn(c.gatewayReadyURL(), true, 30*time.Second)
			}
		}
	}
	return finish("failed", startErr)
}

func gatewayRecoveryPath(stateDir, operationID string) string {
	digest := sha256.Sum256([]byte(operationID))
	return filepath.Join(stateDir, "gateway-recovery", hex.EncodeToString(digest[:])+".json")
}
