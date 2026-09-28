package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/activation"
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/controller"
	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/releaseartifacts"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	if os.Args[1] == "version" || os.Args[1] == "--version" {
		fmt.Println(version)
		return
	}
	if os.Args[1] == "--source-sha" {
		fmt.Println(releaseartifacts.BuildSourceRevision)
		return
	}
	if os.Args[1] == "daemon-start" || os.Args[1] == "daemon-stop" || os.Args[1] == "gateway-start" || os.Args[1] == "gateway-stop" || os.Args[1] == "gateway-restart" {
		daemonLifecycle(os.Args[1])
		return
	}
	if os.Args[1] == "install" {
		install(os.Args[2:])
		return
	}
	if os.Args[1] == "install-and-restart-gateway" {
		installAndRestartGateway(os.Args[2:])
		return
	}
	if os.Args[1] == "init-config" {
		initConfig(os.Args[2:])
		return
	}
	if os.Args[1] == "upgrade" {
		if err := dispatchUpgrade(os.Args[2:], upgradeRuntime, upgradeInspect, upgradeStatus); err != nil {
			fatal(err)
		}
		return
	}
	path := config.DefaultPath()
	c, err := config.Load(path)
	if err != nil {
		fatal(err)
	}
	ctl := controller.Controller{Config: c, ConfigPath: path}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	switch os.Args[1] {
	case "runtime-status":
		var st controller.Status
		st, err = ctl.Status(ctx)
		if err == nil {
			output(st)
		}
	case "doctor":
		err = ctl.Doctor(ctx)
		if err == nil {
			fmt.Println("doctor: ok")
		}
	case "diagnose-startup":
		result := ctl.DiagnoseStartup(ctx)
		output(result)
		if result.ErrorCode != "" {
			os.Exit(1)
		}
	case "state":
		stateCommand(ctx, c)
	case "logs":
		name := "all"
		lines := 100
		if len(os.Args) > 2 {
			name = os.Args[2]
		}
		if len(os.Args) > 3 {
			lines, _ = strconv.Atoi(os.Args[3])
		}
		var text string
		text, err = ctl.Logs(name, lines)
		if err == nil {
			fmt.Print(text)
		}
	default:
		usage()
	}
	if err != nil {
		fatal(err)
	}
}

// daemonLifecycle is intentionally hidden from the public CLI usage. It is
// invoked by canonical machine operations with their exact config path in
// GPT_TUNNEL_CONFIG. Full daemon actions delegate to Controller.Start/Stop;
// Gateway-only actions never touch the Tunnel process.
func daemonLifecycle(action string) {
	path := os.Getenv("GPT_TUNNEL_CONFIG")
	if path == "" {
		fatal(fmt.Errorf("GPT_TUNNEL_CONFIG is required for %s", action))
	}
	c, err := config.Load(path)
	if err != nil {
		fatal(err)
	}
	ctl := controller.Controller{Config: c, ConfigPath: path}
	if action == "daemon-start" {
		if err := ctl.Start(); err != nil {
			fatal(err)
		}
		return
	}
	if action == "daemon-stop" {
		if err := ctl.Stop(); err != nil {
			fatal(err)
		}
		return
	}
	if action == "gateway-stop" {
		if err := ctl.StopGatewayOnly(); err != nil {
			fatal(err)
		}
		return
	}
	if action == "gateway-start" {
		if err := ctl.StartGatewayOnly(); err != nil {
			fatal(err)
		}
		return
	}
	if action == "gateway-restart" {
		if _, err := ctl.RestartGatewayOnlyAfterUpgradeDiagnostics(); err != nil {
			fatal(err)
		}
		return
	}
	fatal(fmt.Errorf("unsupported hidden daemon action %q", action))
}
func install(args []string) {
	fs := flag.NewFlagSet("install", flag.ExitOnError)
	gateway := fs.String("gateway-bin", "", "built gpt-tunnel-gatewayd")
	cli := fs.String("cli-bin", "", "built gpt-tunnel")
	ctl := fs.String("ctl-bin", "", "built gpt-tunnelctl")
	home, _ := os.UserHomeDir()
	dest := fs.String("dest-dir", filepath.Join(home, ".local", "bin"), "installation directory")
	_ = fs.Parse(args)
	if *gateway == "" || *cli == "" || *ctl == "" {
		fatal(fmt.Errorf("all three binary paths are required"))
	}
	if err := fsutil.EnsureDir(*dest, 0o755); err != nil {
		fatal(err)
	}
	for src, name := range map[string]string{*gateway: "gpt-tunnel-gatewayd", *cli: "gpt-tunnel", *ctl: "gpt-tunnelctl"} {
		if err := copyExecutable(src, filepath.Join(*dest, name)); err != nil {
			fatal(err)
		}
	}
	fmt.Println("installed gpt-tunnel-gateway binaries")
}
func installAndRestartGateway(args []string) {
	fs := flag.NewFlagSet("install-and-restart-gateway", flag.ExitOnError)
	gateway := fs.String("gateway-bin", "", "built gpt-tunnel-gatewayd")
	cli := fs.String("cli-bin", "", "built gpt-tunnel")
	ctlBin := fs.String("ctl-bin", "", "built gpt-tunnelctl")
	_ = fs.Parse(args)
	if *gateway == "" || *cli == "" || *ctlBin == "" {
		fatal(fmt.Errorf("all three binary paths are required"))
	}
	configPath := config.DefaultPath()
	c, err := config.Load(configPath)
	if err != nil {
		fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	ctl := controller.Controller{Config: c, ConfigPath: configPath}
	target, err := releaseartifacts.BinaryVersion(*gateway)
	if err != nil {
		fatal(err)
	}
	releaseDir := filepath.Dir(*gateway)
	if err := releaseartifacts.ValidateRelease(releaseDir, target); err != nil {
		emitActivationFailure("artifact_validation", err, nil, nil)
	}
	targetSource, err := releaseSourceRevision(releaseDir)
	if err != nil {
		emitActivationFailure("artifact_source_proof", err, nil, nil)
	}
	if err := activation.SmokeCandidate(ctx, c, *gateway, target); err != nil {
		emitActivationFailure("candidate_smoke", err, nil, nil)
	}
	before, err := ctl.Status(ctx)
	if err != nil {
		emitActivationFailure("preflight", err, nil, nil)
	}
	if !healthyGatewayRuntime(before) {
		emitActivationFailure("preflight", fmt.Errorf("the existing Gateway and Tunnel are not healthy and mapped to the installed artifact set"), nil, nil)
	}
	paths := releaseartifacts.Paths(c.Controller.GatewayBinary)
	previousArtifacts, err := releaseartifacts.SnapshotAll(paths)
	if err != nil {
		emitActivationFailure("artifact_snapshot", err, nil, nil)
	}
	snapshot, err := activation.CreateRecoverySnapshot(c.Controller.PIDDir, releaseDir, target, targetSource, previousArtifacts)
	if err != nil {
		emitActivationFailure("recovery_snapshot_prepare", err, nil, nil)
	}
	var after controller.Status
	outcome, activationErr := ctl.ActivateGateway(controller.GatewayActivation{
		ValidateBeforeStop: func() error {
			current, err := ctl.Status(ctx)
			if err != nil {
				return err
			}
			if !healthyGatewayRuntime(current) || current.Gateway.PID != before.Gateway.PID || current.Tunnel.PID != before.Tunnel.PID || current.InstalledVersion != before.InstalledVersion {
				return fmt.Errorf("serving runtime changed after candidate preflight")
			}
			return snapshot.VerifyPrevious(paths)
		},
		SnapshotState: func() error {
			return snapshot.CaptureDurableState(ctx, c.StateDir)
		},
		RestoreState: func() error {
			return snapshot.RestoreDurableState(c.StateDir)
		},
		Replace: func() error {
			return snapshot.ReplaceCandidate(paths)
		},
		Restore: func() error {
			return snapshot.RestorePrevious(paths)
		},
		Verify: func() error {
			if err := snapshot.VerifyCandidate(paths); err != nil {
				return err
			}
			var statusErr error
			after, statusErr = ctl.Status(ctx)
			if statusErr != nil {
				return statusErr
			}
			if after.Tunnel.PID != before.Tunnel.PID || !healthyGatewayRuntime(after) || after.InstalledVersion != target || after.RunningVersion != target || !after.RuntimeIdentity.ExactSourceMatch || after.RuntimeIdentity.SourceSHA != targetSource || !after.RuntimeIdentity.ArtifactSetCoherent || !after.RuntimeIdentity.RunningGatewayMatchesInstall || after.RuntimeIdentity.RunningExecutableSHA256 != after.RuntimeIdentity.InstalledGatewaySHA256 {
				return fmt.Errorf("candidate readiness or installed artifact/source identity proof failed")
			}
			if err := ctl.Doctor(ctx); err != nil {
				return err
			}
			return activation.LiveMCPSmoke(ctx, c, target)
		},
		VerifyRollback: func() error {
			if err := snapshot.VerifyPrevious(paths); err != nil {
				return err
			}
			previous, err := ctl.Status(ctx)
			if err != nil {
				return err
			}
			if previous.Tunnel.PID != before.Tunnel.PID || !healthyGatewayRuntime(previous) || previous.InstalledVersion != before.InstalledVersion || previous.RunningVersion != before.RunningVersion || previous.RuntimeIdentity.InstalledGatewaySHA256 != before.RuntimeIdentity.InstalledGatewaySHA256 || !previous.RuntimeIdentity.RunningGatewayMatchesInstall || previous.RuntimeIdentity.RunningExecutableSHA256 != previous.RuntimeIdentity.InstalledGatewaySHA256 {
				return fmt.Errorf("previous Gateway artifact and Tunnel identity proof failed")
			}
			if before.RuntimeIdentity.ExactSourceMatch && (!previous.RuntimeIdentity.ExactSourceMatch || previous.RuntimeIdentity.SourceSHA != before.RuntimeIdentity.SourceSHA) {
				return fmt.Errorf("previous Gateway source identity proof failed")
			}
			if err := ctl.Doctor(ctx); err != nil {
				return err
			}
			return activation.LiveMCPSmoke(ctx, c, before.InstalledVersion)
		},
	})
	if activationErr != nil {
		preserveSnapshot := outcome.ForwardRecovery == controller.GatewayForwardRecoveryRequired || outcome.ForwardRecovery == controller.GatewayForwardRecoveryServingCandidate
		rollbackState := &activationRollbackDiagnostics{
			ArtifactsRestored:         outcome.PreviousArtifactsRestored,
			GatewayRestarted:          outcome.PreviousGatewayRestarted,
			DurableSnapshotAvailable:  outcome.DurableSnapshotAvailable,
			DurableStateRestored:      outcome.DurableStateRestored,
			ForwardRecovery:           outcome.ForwardRecovery,
			RecoverySnapshotAvailable: preserveSnapshot,
		}
		if preserveSnapshot {
			rollbackState.RecoverySnapshotID = snapshot.ID()
		} else if err := snapshot.Cleanup(); err != nil {
			rollbackState.RecoverySnapshotAvailable = true
			rollbackState.RecoverySnapshotID = snapshot.ID()
			rollbackState.RestoreError = boundedError(err)
		}
		if outcome.RollbackStartup != nil {
			rollbackState.RestartDiagnostics = startupDiagnostics(*outcome.RollbackStartup)
		}
		if outcome.ForwardStartup != nil {
			rollbackState.ForwardStartupDiagnostics = startupDiagnostics(*outcome.ForwardStartup)
		}
		phase := outcome.FailurePhase
		if phase == "" {
			phase = "gateway_cutover"
		}
		if outcome.ForwardRecovery == controller.GatewayForwardRecoveryRequired || outcome.ForwardRecovery == controller.GatewayForwardRecoveryServingCandidate {
			phase = "compatible_forward_recovery"
		}
		var targetStartup *controller.GatewayStartupDiagnostics
		if outcome.TargetStartup != nil {
			targetStartup = outcome.TargetStartup
		}
		emitActivationFailure(phase, activationErr, rollbackState, targetStartup)
	}
	if err := snapshot.Cleanup(); err != nil {
		fmt.Fprintf(os.Stderr, "gpt-tunnelctl: activation_snapshot_retained id=%s reason=cleanup_failed\n", snapshot.ID())
	}
	output(after)
}

func releaseSourceRevision(releaseDir string) (string, error) {
	var sourceRevision string
	for _, name := range releaseartifacts.BinaryNames {
		source, modified, err := releaseartifacts.BinarySourceRevision(filepath.Join(releaseDir, name))
		if err != nil || modified {
			return "", fmt.Errorf("release artifact %s has no exact source provenance", name)
		}
		if sourceRevision == "" {
			sourceRevision = source
		} else if source != sourceRevision {
			return "", fmt.Errorf("release artifacts do not share one source revision")
		}
	}
	if sourceRevision == "" {
		return "", fmt.Errorf("release source revision is unavailable")
	}
	return sourceRevision, nil
}

func healthyGatewayRuntime(status controller.Status) bool {
	identity := status.RuntimeIdentity
	return status.Gateway.Running && status.Tunnel.Running && status.GatewayReady && status.TunnelReady && status.VersionMatch && identity.ArtifactSetCoherent && identity.RunningGatewayMatchesInstall && identity.RunningExecutableSHA256 != "" && identity.RunningExecutableSHA256 == identity.InstalledGatewaySHA256
}

type activationFailureDiagnostics struct {
	SchemaVersion  int                            `json:"schema_version"`
	Phase          string                         `json:"phase"`
	Error          string                         `json:"error"`
	ErrorTruncated bool                           `json:"error_truncated"`
	Rollback       *activationRollbackDiagnostics `json:"rollback,omitempty"`
	Restart        *activationStartupDiagnostics  `json:"restart,omitempty"`
}
type activationRollbackDiagnostics struct {
	ArtifactsRestored         bool                          `json:"artifacts_restored"`
	GatewayRestarted          bool                          `json:"gateway_restarted"`
	DurableSnapshotAvailable  bool                          `json:"durable_snapshot_available"`
	DurableStateRestored      bool                          `json:"durable_state_restored"`
	ForwardRecovery           string                        `json:"forward_recovery"`
	RecoverySnapshotID        string                        `json:"recovery_snapshot_id,omitempty"`
	RecoverySnapshotAvailable bool                          `json:"recovery_snapshot_available"`
	RestoreError              string                        `json:"restore_error,omitempty"`
	RestartError              string                        `json:"restart_error,omitempty"`
	RestartDiagnostics        *activationStartupDiagnostics `json:"restart_diagnostics,omitempty"`
	ForwardStartupDiagnostics *activationStartupDiagnostics `json:"forward_startup_diagnostics,omitempty"`
}
type activationStartupDiagnostics struct {
	Phase                  string `json:"phase"`
	CaptureStatus          string `json:"capture_status"`
	TargetPID              int    `json:"target_pid,omitempty"`
	TargetProcessRunning   bool   `json:"target_process_running"`
	TargetProcessExited    bool   `json:"target_process_exited"`
	ProcessStateError      string `json:"process_state_error,omitempty"`
	AliveButUnready        bool   `json:"alive_but_unready"`
	ElapsedMilliseconds    int64  `json:"elapsed_ms"`
	ReadinessPassed        bool   `json:"readiness_passed"`
	Error                  string `json:"error,omitempty"`
	LogDelta               string `json:"log_delta,omitempty"`
	LogDeltaTruncated      bool   `json:"log_delta_truncated"`
	DiagnosticCaptureError string `json:"diagnostic_capture_error,omitempty"`
}

func boundedError(err error) string {
	value, _ := boundedErrorWithTruncation(err)
	return value
}
func boundedErrorWithTruncation(err error) (string, bool) {
	if err == nil {
		return "", false
	}
	return activation.BoundedDiagnosticOutput([]byte(err.Error()))
}
func startupDiagnostics(value controller.GatewayStartupDiagnostics) *activationStartupDiagnostics {
	result := &activationStartupDiagnostics{
		Phase:                value.Phase,
		CaptureStatus:        value.CaptureStatus,
		TargetPID:            value.TargetPID,
		TargetProcessRunning: value.TargetProcessRunning,
		TargetProcessExited:  value.TargetProcessExited,
		AliveButUnready:      value.AliveButUnready,
		ElapsedMilliseconds:  value.Elapsed.Milliseconds(),
		ReadinessPassed:      value.ReadinessPassed,
		LogDelta:             value.LogDelta,
		LogDeltaTruncated:    value.LogDeltaTruncated,
	}
	if value.ProcessStateError != nil {
		result.ProcessStateError = boundedError(value.ProcessStateError)
	}
	if value.Error != nil {
		result.Error = boundedError(value.Error)
	}
	if value.LogCaptureError != nil {
		result.DiagnosticCaptureError = boundedError(value.LogCaptureError)
	}
	return result
}
func emitActivationFailure(phase string, err error, rollback *activationRollbackDiagnostics, restart *controller.GatewayStartupDiagnostics) {
	errorText, truncated := boundedErrorWithTruncation(err)
	diagnostic := activationFailureDiagnostics{
		SchemaVersion:  1,
		Phase:          phase,
		Error:          errorText,
		ErrorTruncated: truncated,
		Rollback:       rollback,
	}
	if restart != nil {
		diagnostic.Restart = startupDiagnostics(*restart)
	}
	payload, marshalErr := json.Marshal(diagnostic)
	if marshalErr != nil {
		fatal(fmt.Errorf("activation failure diagnostics: %w; original failure: %s", marshalErr, errorText))
	}
	fmt.Fprintln(os.Stderr, "gpt-tunnelctl: activation_failure", string(payload))
	os.Exit(1)
}
