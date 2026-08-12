package controller

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
)

func TestSuperviseRecoversAbsentGatewayWithoutRestartingTunnel(t *testing.T) {
	oldStatus, oldStart, oldReady, oldStop, oldSleep := recoveryStatusFn, recoveryStartFn, recoveryReadyFn, recoveryStopFn, recoverySleepFn
	defer func() {
		recoveryStatusFn, recoveryStartFn, recoveryReadyFn, recoveryStopFn, recoverySleepFn = oldStatus, oldStart, oldReady, oldStop, oldSleep
	}()
	c := Controller{Config: config.Config{Controller: config.ControllerConfig{PIDDir: t.TempDir()}}}
	calls, statuses := 0, 0
	recoveryStatusFn = func(context.Context, Controller) (Status, error) {
		statuses++
		if statuses <= 2 {
			return Status{
				Tunnel: ProcessStatus{
					Running:       true,
					PID:           41,
					IdentityValid: true,
				},
				TunnelReady: true,
			}, nil
		}
		return Status{
			Gateway: ProcessStatus{
				Running:       true,
				IdentityValid: true,
			},
			Tunnel: ProcessStatus{
				Running:       true,
				PID:           41,
				IdentityValid: true,
			},
			GatewayReady: true,
			TunnelReady:  true,
			VersionMatch: true,
		}, nil
	}
	recoveryStartFn = func(Controller) error { calls++; return nil }
	recoveryReadyFn = func(Controller) error { return nil }
	recoveryStopFn = func(Controller) error { t.Fatal("recovery stopped the existing tunnel or gateway"); return nil }
	recoverySleepFn = func(time.Duration) {}
	if err := c.Supervise(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("gateway starts = %d, want 1", calls)
	}
	data, err := os.ReadFile(c.gatewayRecoveryPath())
	if err != nil || !strings.Contains(string(data), `"status": "recovered"`) {
		t.Fatalf("recovery state = %s, err=%v", data, err)
	}
}

func TestRecoverGatewayIsBoundedAndPersistsDegraded(t *testing.T) {
	oldStatus, oldStart, oldReady, oldStop, oldSleep := recoveryStatusFn, recoveryStartFn, recoveryReadyFn, recoveryStopFn, recoverySleepFn
	defer func() {
		recoveryStatusFn, recoveryStartFn, recoveryReadyFn, recoveryStopFn, recoverySleepFn = oldStatus, oldStart, oldReady, oldStop, oldSleep
	}()
	c := Controller{Config: config.Config{Controller: config.ControllerConfig{PIDDir: t.TempDir()}}}
	starts, stops := 0, 0
	recoveryStatusFn = func(context.Context, Controller) (Status, error) {
		return Status{
			Tunnel: ProcessStatus{
				Running:       true,
				PID:           41,
				IdentityValid: true,
			},
			TunnelReady: true,
		}, nil
	}
	recoveryStartFn = func(Controller) error { starts++; return nil }
	recoveryReadyFn = func(Controller) error { return os.ErrDeadlineExceeded }
	recoveryStopFn = func(Controller) error { stops++; return nil }
	recoverySleepFn = func(time.Duration) {}
	err := c.RecoverGateway(context.Background())
	if err == nil || !strings.Contains(err.Error(), "degraded") {
		t.Fatalf("recovery error = %v", err)
	}
	if starts != maxGatewayRecoveryAttempts || stops != maxGatewayRecoveryAttempts {
		t.Fatalf("attempts starts=%d stops=%d, want %d", starts, stops, maxGatewayRecoveryAttempts)
	}
	data, readErr := os.ReadFile(c.gatewayRecoveryPath())
	if readErr != nil || !strings.Contains(string(data), `"status": "degraded"`) || !strings.Contains(string(data), "i/o timeout") {
		t.Fatalf("degraded state = %s, err=%v", data, readErr)
	}
}

func TestSuperviseHealthyRuntimeIsNoOp(t *testing.T) {
	oldStatus, oldStart := recoveryStatusFn, recoveryStartFn
	defer func() { recoveryStatusFn, recoveryStartFn = oldStatus, oldStart }()
	c := Controller{Config: config.Config{Controller: config.ControllerConfig{PIDDir: t.TempDir()}}}
	starts := 0
	recoveryStatusFn = func(context.Context, Controller) (Status, error) {
		return Status{
			Gateway: ProcessStatus{
				Running:       true,
				IdentityValid: true,
			},
			Tunnel: ProcessStatus{
				Running:       true,
				IdentityValid: true,
			},
			GatewayReady: true,
			TunnelReady:  true,
			VersionMatch: true,
		}, nil
	}
	recoveryStartFn = func(Controller) error { starts++; return nil }
	if err := c.Supervise(context.Background()); err != nil || starts != 0 {
		t.Fatalf("healthy supervision err=%v starts=%d", err, starts)
	}
}

func TestStatusProjectsDurableGatewayDegradedState(t *testing.T) {
	c := Controller{Config: config.Config{Controller: config.ControllerConfig{PIDDir: t.TempDir(), GatewayBinary: "/bin/true", TunnelClientBinary: "/bin/true", TunnelHealthListenAddr: "127.0.0.1:1"}, ListenAddr: "127.0.0.1:2"}}
	if err := c.writeGatewayRecoveryState(gatewayRecoveryState{
		Status:    "degraded",
		Reason:    "Gateway absent",
		UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	status, err := c.Status(context.Background())
	if err != nil || !status.Degraded || status.DegradedReason != "Gateway absent" {
		t.Fatalf("status=%+v err=%v", status, err)
	}
}
