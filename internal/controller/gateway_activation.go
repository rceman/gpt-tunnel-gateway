package controller

import (
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
)

const (
	GatewayForwardRecoveryNotAttempted     = "not_attempted"
	GatewayForwardRecoveryNotNeeded        = "not_needed"
	GatewayForwardRecoveryPreviousRestored = "previous_restored"
	GatewayForwardRecoveryServingCandidate = "serving_candidate"
	GatewayForwardRecoveryRequired         = "required"
)

// GatewayActivation is the server-owned handoff between a verified artifact
// set and the running Gateway. The controller lock covers stop, replacement,
// start, readiness, and verification so another lifecycle cannot interleave.
type GatewayActivation struct {
	ValidateBeforeStop func() error
	SnapshotState      func() error
	RestoreState       func() error
	Replace            func() error
	Restore            func() error
	Verify             func() error
	VerifyRollback     func() error
}

type GatewayActivationOutcome struct {
	FailurePhase              string
	DurableSnapshotAvailable  bool
	DurableStateRestored      bool
	PreviousArtifactsRestored bool
	PreviousGatewayRestarted  bool
	ForwardRecovery           string
	TargetStartup             *GatewayStartupDiagnostics
	RollbackStartup           *GatewayStartupDiagnostics
	ForwardStartup            *GatewayStartupDiagnostics
}

// ActivateGateway performs a Gateway-only self-activation. Tunnel is never
// touched. A failed candidate restores matching state and artifacts before
// restarting the previous Gateway, or enters compatible-forward recovery.
func (c Controller) ActivateGateway(activation GatewayActivation) (GatewayActivationOutcome, error) {
	outcome := GatewayActivationOutcome{ForwardRecovery: GatewayForwardRecoveryNotAttempted}
	if activation.ValidateBeforeStop == nil || activation.SnapshotState == nil || activation.RestoreState == nil || activation.Replace == nil || activation.Restore == nil || activation.Verify == nil || activation.VerifyRollback == nil {
		outcome.FailurePhase = "activation_configuration"
		return outcome, fmt.Errorf("gateway activation callbacks are incomplete")
	}
	lock, err := lockfile.Acquire(c.Config.Controller.PIDDir, "controller")
	if err != nil {
		outcome.FailurePhase = "controller_lock"
		return outcome, err
	}
	defer lock.Release()
	if err := activation.ValidateBeforeStop(); err != nil {
		outcome.FailurePhase = "preflight"
		return outcome, fmt.Errorf("gateway activation preflight: %w", err)
	}
	if err := restartGatewayStopFn(c); err != nil {
		outcome.FailurePhase = "gateway_stop"
		return outcome, err
	}
	rollback := func(cause error, candidateMayHaveRun bool) (GatewayActivationOutcome, error) {
		if err := restartGatewayStopFn(c); err != nil {
			outcome.ForwardRecovery = GatewayForwardRecoveryRequired
			return outcome, fmt.Errorf("gateway activation compatible-forward recovery required; candidate could not be stopped and previous binary was not started: stop=%v; original failure: %w", err, cause)
		}
		if candidateMayHaveRun {
			if err := activation.RestoreState(); err != nil {
				return c.recoverGatewayForward(activation, outcome, cause, err, true)
			}
			outcome.DurableStateRestored = true
		}
		if err := activation.Restore(); err != nil {
			return c.recoverGatewayForward(activation, outcome, cause, err, true)
		}
		outcome.PreviousArtifactsRestored = true
		startup, startErr := c.startGatewayAfterStopDiagnostics()
		outcome.RollbackStartup = &startup
		if startErr == nil {
			startErr = activation.VerifyRollback()
		}
		if startErr == nil {
			outcome.PreviousGatewayRestarted = true
			outcome.ForwardRecovery = GatewayForwardRecoveryPreviousRestored
			return outcome, cause
		}
		if err := restartGatewayStopFn(c); err != nil {
			outcome.ForwardRecovery = GatewayForwardRecoveryRequired
			return outcome, fmt.Errorf("gateway activation compatible-forward recovery required; previous runtime could not be stopped after rollback proof failed: stop=%v; rollback=%v; original failure: %w", err, startErr, cause)
		}
		return c.recoverGatewayForward(activation, outcome, cause, startErr, true)
	}
	if err := activation.SnapshotState(); err != nil {
		outcome.FailurePhase = "durable_snapshot_capture"
		return rollback(fmt.Errorf("capture pre-mutation durable snapshot: %w", err), false)
	}
	outcome.DurableSnapshotAvailable = true
	if err := activation.Replace(); err != nil {
		outcome.FailurePhase = "artifact_replacement"
		return rollback(err, false)
	}
	startup, startErr := c.startGatewayAfterStopDiagnostics()
	outcome.TargetStartup = &startup
	if startErr != nil {
		outcome.FailurePhase = "gateway_restart_readiness"
		return rollback(fmt.Errorf("candidate startup failed: %w", startErr), true)
	}
	if err := activation.Verify(); err != nil {
		outcome.FailurePhase = "post_cutover_verification"
		return rollback(err, true)
	}
	outcome.ForwardRecovery = GatewayForwardRecoveryNotNeeded
	return outcome, nil
}

func (c Controller) recoverGatewayForward(activation GatewayActivation, outcome GatewayActivationOutcome, cause, recoveryCause error, stopped bool) (GatewayActivationOutcome, error) {
	outcome.ForwardRecovery = GatewayForwardRecoveryRequired
	if !stopped {
		if err := restartGatewayStopFn(c); err != nil {
			return outcome, fmt.Errorf("gateway activation compatible-forward recovery required; stop=%v; rollback=%v; original failure: %w", err, recoveryCause, cause)
		}
	}
	if err := activation.Replace(); err != nil {
		return outcome, fmt.Errorf("gateway activation compatible-forward recovery required; candidate artifact install failed=%v; rollback=%v; original failure: %w", err, recoveryCause, cause)
	}
	startup, startErr := c.startGatewayAfterStopDiagnostics()
	outcome.ForwardStartup = &startup
	if startErr == nil {
		startErr = activation.Verify()
	}
	if startErr != nil {
		stopErr := restartGatewayStopFn(c)
		return outcome, fmt.Errorf("gateway activation compatible-forward recovery required; candidate proof=%v stop=%v; previous binary was not started; rollback=%v; original failure: %w", startErr, stopErr, recoveryCause, cause)
	}
	outcome.ForwardRecovery = GatewayForwardRecoveryServingCandidate
	return outcome, fmt.Errorf("activation failed; compatible-forward recovery is serving the candidate; previous binary was not started; rollback=%v; original failure: %w", recoveryCause, cause)
}
