package controller

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
)

func TestActivateGatewayRestoresMatchingDurableStateAndArtifacts(t *testing.T) {
	oldStop, oldStart, oldWait := restartGatewayStopFn, restartGatewayStartFn, restartGatewayWaitFn
	defer func() { restartGatewayStopFn, restartGatewayStartFn, restartGatewayWaitFn = oldStop, oldStart, oldWait }()
	var steps []string
	artifact, state, snapshot := "previous", "previous-state", ""
	restartGatewayStopFn = func(Controller) error { steps = append(steps, "stop"); return nil }
	restartGatewayStartFn = func(Controller) error {
		steps = append(steps, "start:"+artifact)
		if artifact == "candidate" {
			state = "candidate-state"
		}
		return nil
	}
	restartGatewayWaitFn = func(string, bool, time.Duration) error { steps = append(steps, "ready"); return nil }
	c := Controller{Config: config.Config{Controller: config.ControllerConfig{PIDDir: t.TempDir()}}}
	outcome, err := c.ActivateGateway(GatewayActivation{
		ValidateBeforeStop: func() error { steps = append(steps, "preflight"); return nil },
		SnapshotState: func() error {
			steps = append(steps, "snapshot")
			snapshot = state
			return nil
		},
		RestoreState: func() error {
			steps = append(steps, "restore-state")
			state = snapshot
			return nil
		},
		Replace: func() error { steps = append(steps, "replace-candidate"); artifact = "candidate"; return nil },
		Restore: func() error { steps = append(steps, "restore-previous"); artifact = "previous"; return nil },
		Verify: func() error {
			steps = append(steps, "verify-candidate")
			if artifact != "candidate" || state != "candidate-state" {
				return fmt.Errorf("candidate state was not migrated")
			}
			return fmt.Errorf("post-cutover proof failed")
		},
		VerifyRollback: func() error {
			steps = append(steps, "verify-previous")
			if artifact != "previous" || state != "previous-state" {
				return fmt.Errorf("previous binary and durable state do not match")
			}
			return nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "post-cutover proof failed") {
		t.Fatalf("activation error=%v", err)
	}
	if outcome.FailurePhase != "post_cutover_verification" || !outcome.DurableSnapshotAvailable || !outcome.DurableStateRestored || !outcome.PreviousArtifactsRestored || !outcome.PreviousGatewayRestarted || outcome.ForwardRecovery != "previous_restored" {
		t.Fatalf("rollback outcome=%+v", outcome)
	}
	if artifact != "previous" || state != "previous-state" {
		t.Fatalf("final artifact=%q durable state=%q", artifact, state)
	}
	if got, want := strings.Join(steps, ","), "preflight,stop,snapshot,replace-candidate,start:candidate,ready,verify-candidate,stop,restore-state,restore-previous,start:previous,ready,verify-previous"; got != want {
		t.Fatalf("activation steps=%q want=%q", got, want)
	}
}

func TestActivateGatewayKeepsCandidateWhenDurableRollbackIsUnsafe(t *testing.T) {
	oldStop, oldStart, oldWait := restartGatewayStopFn, restartGatewayStartFn, restartGatewayWaitFn
	defer func() { restartGatewayStopFn, restartGatewayStartFn, restartGatewayWaitFn = oldStop, oldStart, oldWait }()
	artifact, state := "previous", "previous-state"
	starts := make([]string, 0, 2)
	restartGatewayStopFn = func(Controller) error { return nil }
	restartGatewayStartFn = func(Controller) error {
		starts = append(starts, artifact)
		if artifact == "candidate" {
			state = "candidate-state"
		}
		return nil
	}
	restartGatewayWaitFn = func(string, bool, time.Duration) error { return nil }
	c := Controller{Config: config.Config{Controller: config.ControllerConfig{PIDDir: t.TempDir()}}}
	verifyCalls := 0
	previousRestoreCalls := 0
	previousVerifyCalls := 0
	outcome, err := c.ActivateGateway(GatewayActivation{
		ValidateBeforeStop: func() error { return nil },
		SnapshotState:      func() error { return nil },
		RestoreState:       func() error { return fmt.Errorf("snapshot restore failed") },
		Replace:            func() error { artifact = "candidate"; return nil },
		Restore:            func() error { previousRestoreCalls++; artifact = "previous"; return nil },
		Verify: func() error {
			verifyCalls++
			if verifyCalls == 1 {
				return fmt.Errorf("post-cutover verification failed")
			}
			if artifact != "candidate" || state != "candidate-state" {
				return fmt.Errorf("compatible candidate did not remain installed")
			}
			return nil
		},
		VerifyRollback: func() error { previousVerifyCalls++; return nil },
	})
	if err == nil || !strings.Contains(err.Error(), "compatible-forward recovery is serving the candidate") {
		t.Fatalf("activation recovery error=%v", err)
	}
	if outcome.FailurePhase != "post_cutover_verification" || outcome.ForwardRecovery != "serving_candidate" || previousRestoreCalls != 0 || previousVerifyCalls != 0 || len(starts) != 2 || starts[0] != "candidate" || starts[1] != "candidate" {
		t.Fatalf("forward recovery outcome=%+v previous_restore=%d previous_verify=%d starts=%v", outcome, previousRestoreCalls, previousVerifyCalls, starts)
	}
}

func TestActivateGatewayReportsDurableSnapshotCaptureFailure(t *testing.T) {
	oldStop, oldStart, oldWait := restartGatewayStopFn, restartGatewayStartFn, restartGatewayWaitFn
	defer func() { restartGatewayStopFn, restartGatewayStartFn, restartGatewayWaitFn = oldStop, oldStart, oldWait }()
	var steps []string
	restartGatewayStopFn = func(Controller) error { steps = append(steps, "stop"); return nil }
	restartGatewayStartFn = func(Controller) error { steps = append(steps, "start"); return nil }
	restartGatewayWaitFn = func(string, bool, time.Duration) error { steps = append(steps, "ready"); return nil }
	c := Controller{Config: config.Config{Controller: config.ControllerConfig{PIDDir: t.TempDir()}}}
	outcome, err := c.ActivateGateway(GatewayActivation{
		ValidateBeforeStop: func() error { steps = append(steps, "preflight"); return nil },
		SnapshotState:      func() error { steps = append(steps, "snapshot"); return fmt.Errorf("online backup failed") },
		RestoreState:       func() error { t.Fatal("state restore ran without a durable snapshot"); return nil },
		Replace:            func() error { t.Fatal("candidate replacement ran without a durable snapshot"); return nil },
		Restore:            func() error { steps = append(steps, "restore-previous"); return nil },
		Verify:             func() error { t.Fatal("candidate verification ran"); return nil },
		VerifyRollback:     func() error { steps = append(steps, "verify-previous"); return nil },
	})
	if err == nil || !strings.Contains(err.Error(), "online backup failed") {
		t.Fatalf("snapshot failure=%v", err)
	}
	if outcome.FailurePhase != "durable_snapshot_capture" || outcome.DurableSnapshotAvailable || outcome.DurableStateRestored || !outcome.PreviousArtifactsRestored || !outcome.PreviousGatewayRestarted || outcome.ForwardRecovery != GatewayForwardRecoveryPreviousRestored {
		t.Fatalf("snapshot failure outcome=%+v", outcome)
	}
	if got, want := strings.Join(steps, ","), "preflight,stop,snapshot,stop,restore-previous,start,ready,verify-previous"; got != want {
		t.Fatalf("activation steps=%q want=%q", got, want)
	}
}

func TestActivateGatewayDoesNotStopOnPreflightFailure(t *testing.T) {
	oldStop := restartGatewayStopFn
	defer func() { restartGatewayStopFn = oldStop }()
	stops := 0
	restartGatewayStopFn = func(Controller) error { stops++; return nil }
	c := Controller{Config: config.Config{Controller: config.ControllerConfig{PIDDir: t.TempDir()}}}
	outcome, err := c.ActivateGateway(GatewayActivation{
		ValidateBeforeStop: func() error { return fmt.Errorf("candidate migration blocker") },
		SnapshotState:      func() error { t.Fatal("snapshot ran before preflight"); return nil },
		RestoreState:       func() error { return nil },
		Replace:            func() error { return nil },
		Restore:            func() error { return nil },
		Verify:             func() error { return nil },
		VerifyRollback:     func() error { return nil },
	})
	if err == nil || stops != 0 || outcome.FailurePhase != "preflight" {
		t.Fatalf("preflight error=%v stop calls=%d outcome=%+v", err, stops, outcome)
	}
}
