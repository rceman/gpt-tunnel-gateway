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
	case "start":
		err = ctl.Start()
	case "stop":
		err = ctl.Stop()
	case "restart":
		err = ctl.Restart()
	case "restart-gateway":
		var result controller.GatewayRecoveryResult
		result, err = ctl.RestartGatewayRecovery("")
		if err == nil {
			output(result)
		}
	case "status":
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
	if err := activation.SmokeCandidate(ctx, c, *gateway, target); err != nil {
		emitActivationFailure("candidate_smoke", err, nil, nil)
	}
	paths := releaseartifacts.Paths(c.Controller.GatewayBinary)
	old, err := releaseartifacts.SnapshotAll(paths)
	if err != nil {
		emitActivationFailure("artifact_snapshot", err, nil, nil)
	}
	if err := releaseartifacts.ReplaceAll(releaseDir, paths, old); err != nil {
		emitActivationFailure("artifact_replacement", err, nil, nil)
	}
	rollback := func(failedPhase string, cause error, restartDiagnostics *controller.GatewayStartupDiagnostics) {
		restoreErr := releaseartifacts.RestoreAll(paths, old)
		rollbackDiagnostics, restartErr := ctl.RestartGatewayAfterUpgradeDiagnostics()
		rollbackState := &activationRollbackDiagnostics{
			ArtifactsRestored: restoreErr == nil,
			GatewayRestarted:  restartErr == nil,
		}
		if restoreErr != nil {
			rollbackState.RestoreError = boundedError(restoreErr)
		}
		if restartErr != nil {
			rollbackState.RestartError = boundedError(restartErr)
		}
		rollbackState.RestartDiagnostics = startupDiagnostics(rollbackDiagnostics)
		if restoreErr != nil || restartErr != nil {
			emitActivationFailure("rollback", fmt.Errorf("rollback failed after %s: restore=%v restart=%v; original failure: %v", failedPhase, restoreErr, restartErr, cause), rollbackState, restartDiagnostics)
		}
		emitActivationFailure(failedPhase, cause, rollbackState, restartDiagnostics)
	}
	before, err := ctl.Status(ctx)
	if err != nil {
		rollback("preflight", err, nil)
	}
	restartDiagnostics, restartErr := ctl.RestartGatewayAfterUpgradeDiagnostics()
	if restartErr != nil {
		rollback("gateway_restart_readiness", fmt.Errorf("gateway candidate startup failed: %w", restartErr), &restartDiagnostics)
	}
	after, err := ctl.Status(ctx)
	if err != nil {
		rollback("gateway_restart_readiness", err, &restartDiagnostics)
	}
	if after.Tunnel.PID != before.Tunnel.PID || !after.GatewayReady || !after.TunnelReady || !after.VersionMatch {
		rollback("gateway_restart_readiness", fmt.Errorf("candidate readiness or Tunnel identity proof failed"), &restartDiagnostics)
	}
	if err := ctl.Doctor(ctx); err != nil {
		rollback("doctor", err, &restartDiagnostics)
	}
	output(after)
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
	ArtifactsRestored  bool                          `json:"artifacts_restored"`
	GatewayRestarted   bool                          `json:"gateway_restarted"`
	RestoreError       string                        `json:"restore_error,omitempty"`
	RestartError       string                        `json:"restart_error,omitempty"`
	RestartDiagnostics *activationStartupDiagnostics `json:"restart_diagnostics,omitempty"`
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
