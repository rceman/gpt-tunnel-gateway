package debug

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/activation"
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/controller"
	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
)

type ActivationResult struct {
	OperationID string `json:"operation_id"`
	SourceHead  string `json:"source_head"`
	Activation  string `json:"activation"`
	Smoke       string `json:"smoke"`
	TunnelPID   int    `json:"tunnel_pid"`
	GatewayPID  int    `json:"gateway_pid"`
	Outcome     string `json:"outcome"`
}

type activationReceipt struct {
	ActivationResult
	Error string `json:"error,omitempty"`
}

type ActivationFailure struct {
	OperationID string
	Cause       string
}

// Activate delegates the established exact-source artifact pipeline. Debug
// receipt and worker ownership remain in this package; activation keeps only
// the reusable build/replace/rollback mechanics.
func Activate(ctx context.Context, c config.Config, configPath string, project config.ProjectConfig, sourceHead string) (ActivationResult, error) {
	result, err := activation.DebugActivate(ctx, c, configPath, project, sourceHead)
	return ActivationResult{
		SourceHead: result.SourceHead, Activation: result.Activation, Smoke: result.Smoke,
		TunnelPID: result.TunnelPID, GatewayPID: result.GatewayPID,
	}, err
}

func (e ActivationFailure) Error() string {
	if e.Cause == "" {
		return fmt.Sprintf("gateway debug activation %s failed", e.OperationID)
	}
	return fmt.Sprintf("gateway debug activation %s failed: %s", e.OperationID, e.Cause)
}

func (e ActivationFailure) StructuredActionError() map[string]any {
	return map[string]any{"code": "GATEWAY_DEBUG_ACTIVATION_FAILED", "operation_id": e.OperationID, "message": e.Cause}
}

var workerLaunchFn = func(c controller.Controller, operationID, sourceHead string) error {
	return c.LaunchDetachedGatewayWorker([]string{"--config", c.ConfigPath, "--gateway-debug-activation-worker", operationID, sourceHead}, "gateway-debug-activation-worker.log")
}

func operationID(sourceHead string) string {
	return "gateway-debug-activation-" + sourceHead
}

func receiptPath(stateDir, id string) string {
	digest := sha256.Sum256([]byte(id))
	return filepath.Join(stateDir, "gateway-debug-activation", hex.EncodeToString(digest[:])+".json")
}

func readReceipt(path, id string) (activationReceipt, bool, error) {
	var receipt activationReceipt
	if err := fsutil.ReadJSONBounded(path, 16<<10, &receipt); err != nil {
		if os.IsNotExist(err) {
			return activationReceipt{}, false, nil
		}
		return activationReceipt{}, false, err
	}
	if receipt.OperationID != id {
		return activationReceipt{}, false, fmt.Errorf("gateway debug activation receipt identity mismatch")
	}
	return receipt, true, nil
}

func failure(receipt activationReceipt) error {
	return ActivationFailure{OperationID: receipt.OperationID, Cause: receipt.Error}
}

// AcceptActivation records the bounded accepted receipt before releasing the
// detached worker. The source identity is also the idempotency identity.
func AcceptActivation(c config.Config, configPath, sourceHead string, release func(func())) (ActivationResult, error) {
	if release == nil {
		return ActivationResult{}, fmt.Errorf("debug activation requires an HTTP response release boundary")
	}
	if len(sourceHead) != 40 {
		return ActivationResult{}, fmt.Errorf("debug activation source must be an exact commit")
	}
	if _, err := hex.DecodeString(sourceHead); err != nil {
		return ActivationResult{}, fmt.Errorf("debug activation source must be an exact commit")
	}
	ctl := controller.Controller{Config: c, ConfigPath: configPath}
	id := operationID(sourceHead)
	path := receiptPath(c.StateDir, id)
	prior, exists, err := readReceipt(path, id)
	if err != nil {
		return ActivationResult{}, err
	}
	if exists {
		switch prior.Outcome {
		case "succeeded":
			return prior.ActivationResult, nil
		case "failed":
			return prior.ActivationResult, failure(prior)
		case "accepted", "in_progress":
			return prior.ActivationResult, nil
		default:
			return ActivationResult{}, fmt.Errorf("gateway debug activation receipt has invalid outcome")
		}
	}
	lock, err := lockfile.Acquire(c.Controller.PIDDir, "debug-activation")
	if err != nil {
		prior, exists, readErr := readReceipt(path, id)
		if readErr != nil {
			return ActivationResult{}, readErr
		}
		if exists {
			switch prior.Outcome {
			case "succeeded":
				return prior.ActivationResult, nil
			case "failed":
				return prior.ActivationResult, failure(prior)
			case "accepted", "in_progress":
				return prior.ActivationResult, nil
			}
		}
		return ActivationResult{}, err
	}
	prior, exists, err = readReceipt(path, id)
	if err != nil {
		_ = lock.Release()
		return ActivationResult{}, err
	}
	if exists {
		_ = lock.Release()
		switch prior.Outcome {
		case "succeeded":
			return prior.ActivationResult, nil
		case "failed":
			return prior.ActivationResult, failure(prior)
		case "accepted", "in_progress":
			return prior.ActivationResult, nil
		default:
			return ActivationResult{}, fmt.Errorf("gateway debug activation receipt has invalid outcome")
		}
	}
	old := ctl.ProcessStatus("gateway")
	tunnel := ctl.ProcessStatus("tunnel")
	result := ActivationResult{OperationID: id, SourceHead: sourceHead, Activation: "accepted", Smoke: "pending", GatewayPID: old.PID, TunnelPID: tunnel.PID, Outcome: "accepted"}
	if err := fsutil.WriteJSONAtomic(path, activationReceipt{ActivationResult: result}, 0o600); err != nil {
		_ = lock.Release()
		return ActivationResult{}, err
	}
	_ = lock.Release()
	scheduleWorker(ctl, id, sourceHead, release)
	return result, nil
}

func scheduleWorker(c controller.Controller, id, sourceHead string, release func(func())) {
	release(func() {
		if err := workerLaunchFn(c, id, sourceHead); err != nil {
			recordLaunchFailure(c.Config, id, sourceHead, err)
		}
	})
}

// RunActivation serializes the whole detached operation under the debug
// operation lock, so duplicate workers cannot stop, replace, or start twice.
func RunActivation(c config.Config, configPath, id, sourceHead string, execute func(context.Context) (ActivationResult, error)) (ActivationResult, error) {
	lock, err := lockfile.Acquire(c.Controller.PIDDir, "debug-activation")
	if err != nil {
		if lockfile.IsBusy(err) {
			return ActivationResult{}, nil
		}
		return ActivationResult{}, err
	}
	defer lock.Release()
	path := receiptPath(c.StateDir, id)
	prior, exists, err := readReceipt(path, id)
	if err != nil {
		return ActivationResult{}, err
	}
	if !exists || prior.SourceHead != sourceHead {
		return ActivationResult{}, fmt.Errorf("debug activation receipt is missing or does not match the requested source")
	}
	switch prior.Outcome {
	case "succeeded":
		return prior.ActivationResult, nil
	case "failed":
		return prior.ActivationResult, failure(prior)
	case "accepted", "in_progress":
		prior.Outcome = "in_progress"
		if err := fsutil.WriteJSONAtomic(path, prior, 0o600); err != nil {
			return ActivationResult{}, err
		}
	default:
		return ActivationResult{}, fmt.Errorf("gateway debug activation receipt has invalid outcome")
	}
	operationContext, cancel := context.WithTimeout(context.Background(), time.Duration(c.RunTimeoutSeconds)*time.Second)
	defer cancel()
	executed, executeErr := execute(operationContext)
	terminal := prior.ActivationResult
	terminal.Activation = executed.Activation
	terminal.Smoke = executed.Smoke
	terminal.TunnelPID = executed.TunnelPID
	terminal.GatewayPID = executed.GatewayPID
	terminal.Outcome = "succeeded"
	receipt := activationReceipt{ActivationResult: terminal}
	if executeErr != nil {
		terminal.Activation = "failed"
		terminal.Smoke = "failed"
		terminal.Outcome = "failed"
		receipt.ActivationResult = terminal
		receipt.Error = boundedError(executeErr)
	}
	if writeErr := fsutil.WriteJSONAtomic(path, receipt, 0o600); writeErr != nil {
		if executeErr == nil {
			executeErr = fmt.Errorf("persist debug activation result: %w", writeErr)
			terminal.Activation = "failed"
			terminal.Smoke = "failed"
			terminal.Outcome = "failed"
			receipt.ActivationResult = terminal
			receipt.Error = boundedError(executeErr)
			_ = fsutil.WriteJSONAtomic(path, receipt, 0o600)
		}
	}
	if executeErr != nil {
		return terminal, failure(receipt)
	}
	return terminal, nil
}

func recordLaunchFailure(c config.Config, id, sourceHead string, cause error) {
	lock, err := lockfile.Acquire(c.Controller.PIDDir, "debug-activation")
	if err != nil {
		return
	}
	defer lock.Release()
	path := receiptPath(c.StateDir, id)
	receipt, exists, err := readReceipt(path, id)
	if err != nil || !exists || receipt.SourceHead != sourceHead || receipt.Outcome == "succeeded" || receipt.Outcome == "failed" {
		return
	}
	receipt.Outcome = "failed"
	receipt.Activation = "failed"
	receipt.Smoke = "failed"
	receipt.Error = boundedError(cause)
	_ = fsutil.WriteJSONAtomic(path, receipt, 0o600)
}

func boundedError(err error) string {
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
