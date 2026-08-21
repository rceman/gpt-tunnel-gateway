package controller

import (
	"fmt"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
)

// GatewayActivation is the server-owned handoff between a verified artifact
// set and the running Gateway. The controller lock covers stop, replacement,
// start, readiness, and verification so another lifecycle can interleave.
type GatewayActivation struct {
	Replace func() error
	Restore func() error
	Verify  func() error
}

// ActivateGateway performs one Gateway-only self-activation. Tunnel is never
// touched. Any failure after stop restores the previous artifacts and starts
// the previous Gateway before returning the original failure.
func (c Controller) ActivateGateway(activation GatewayActivation) error {
	if activation.Replace == nil || activation.Restore == nil || activation.Verify == nil {
		return fmt.Errorf("gateway activation callbacks are incomplete")
	}
	lock, err := lockfile.Acquire(c.Config.Controller.PIDDir, "controller")
	if err != nil {
		return err
	}
	defer lock.Release()
	if err := restartGatewayStopFn(c); err != nil {
		return err
	}
	rollback := func(cause error) error {
		stopErr := restartGatewayStopFn(c)
		restoreErr := activation.Restore()
		startErr := restartGatewayStartFn(c)
		waitErr := error(nil)
		if startErr == nil {
			waitErr = restartGatewayWaitFn(c.gatewayReadyURL(), true, gatewayActivationReadyTimeout)
		}
		if stopErr != nil || restoreErr != nil || startErr != nil || waitErr != nil {
			return fmt.Errorf("gateway activation rollback failed: stop=%v restore=%v start=%v ready=%v; original failure: %w", stopErr, restoreErr, startErr, waitErr, cause)
		}
		return cause
	}
	if err := activation.Replace(); err != nil {
		return rollback(err)
	}
	if err := restartGatewayStartFn(c); err != nil {
		return rollback(err)
	}
	if err := restartGatewayWaitFn(c.gatewayReadyURL(), true, gatewayActivationReadyTimeout); err != nil {
		return rollback(err)
	}
	if err := activation.Verify(); err != nil {
		return rollback(err)
	}
	return nil
}

const gatewayActivationReadyTimeout = 30 * time.Second
