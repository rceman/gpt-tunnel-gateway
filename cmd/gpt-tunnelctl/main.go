package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/activation"
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/controller"
	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/releaseartifacts"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	"github.com/rceman/gpt-tunnel-gateway/internal/upgrade"
)

var version = "0.6.11"

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
func initConfig(args []string) {
	fs := flag.NewFlagSet("init-config", flag.ExitOnError)
	from := fs.String("from", "", "prepared JSON config")
	to := fs.String("to", config.DefaultPath(), "destination config")
	_ = fs.Parse(args)
	if *from == "" {
		fatal(fmt.Errorf("--from is required"))
	}
	if _, err := os.Stat(*to); err == nil {
		fatal(fmt.Errorf("refusing to overwrite %s", *to))
	}
	if _, err := config.Load(*from); err != nil {
		fatal(fmt.Errorf("validate source config: %w", err))
	}
	data, err := os.ReadFile(*from)
	if err != nil {
		fatal(err)
	}
	if err := fsutil.WriteFileAtomic(*to, data, 0o600); err != nil {
		fatal(err)
	}
	fmt.Println("created", *to)
}

func upgradeRuntime() {
	path := config.DefaultPath()
	c, err := config.Load(path)
	if err != nil {
		fatal(err)
	}
	r := upgrade.Runner{Config: c, ConfigPath: path}
	result, err := r.Run(context.Background())
	if err != nil {
		if upgradeResultShouldPrint(result.Status) {
			output(result)
		}
		fatal(err)
	}
	output(result)
}

func upgradeInspect() {
	path := config.DefaultPath()
	c, err := config.Load(path)
	if err != nil {
		fatal(err)
	}
	result, runErr := upgrade.Inspect(context.Background(), c, path)
	output(result)
	if runErr != nil {
		fatal(runErr)
	}
	if result.Status != "ready" {
		os.Exit(1)
	}
}

func upgradeStatus() {
	path := config.DefaultPath()
	c, err := config.Load(path)
	if err != nil {
		fatal(err)
	}
	result, runErr := upgrade.Status(c)
	output(result)
	if runErr != nil {
		fatal(runErr)
	}
}

func parseUpgradeArgs(args []string) (string, error) {
	switch len(args) {
	case 0:
		return "run", nil
	case 1:
		switch args[0] {
		case "inspect", "status":
			return args[0], nil
		}
	}
	return "", fmt.Errorf("invalid upgrade arguments; use upgrade, upgrade inspect, or upgrade status")
}

func dispatchUpgrade(args []string, run, inspect, status func()) error {
	action, err := parseUpgradeArgs(args)
	if err != nil {
		return err
	}
	switch action {
	case "inspect":
		inspect()
	case "status":
		status()
	default:
		run()
	}
	return nil
}

func stateCommand(ctx context.Context, c config.Config) {
	if len(os.Args) < 3 {
		usage()
	}
	s := service.New(c)
	switch os.Args[2] {
	case "check":
		result, err := s.StateCheck(ctx)
		if err != nil {
			fatal(err)
		}
		output(result)
		if !result.Valid {
			os.Exit(1)
		}
	case "repair":
		if len(os.Args) < 4 || (os.Args[3] != "--dry-run" && os.Args[3] != "--apply") {
			usage()
		}
		result, err := s.StateRepair(ctx, os.Args[3] == "--apply")
		if err != nil {
			fatal(err)
		}
		output(result)
	case "migrate-train-v2-attempts":
		stateMigrateTrainV2Attempts(ctx, s)
	case "retire-run-state":
		stateRetireRunState(ctx, s)
	case "migrate-train-v2-legacy":
		stateMigrateTrainV2Legacy(ctx, s)
	default:
		usage()
	}
}

func stateMigrateTrainV2Legacy(ctx context.Context, s *service.Service) {
	input := service.TrainV2LegacyStateMigrationInput{}
	modeSet, projectSet := false, false
	for i := 3; i < len(os.Args); i++ {
		if os.Args[i] == "--dry-run" || os.Args[i] == "--apply" {
			if modeSet {
				usage()
			}
			modeSet = true
			input.Apply = os.Args[i] == "--apply"
			continue
		}
		if i+1 >= len(os.Args) {
			usage()
		}
		value := os.Args[i+1]
		switch os.Args[i] {
		case "--project":
			input.ProjectID, projectSet = value, true
		case "--expected-hub-revision":
			input.ExpectedHubRevision = value
		case "--reason":
			input.Reason = value
		case "--action":
			parts := strings.Split(value, ":")
			if len(parts) != 3 && len(parts) != 4 && len(parts) != 6 {
				fatal(fmt.Errorf("--action requires action:train_id:train_sha256[:integration_sha256[:mutation_id:mutation_sha256]]"))
			}
			action := service.TrainV2LegacyStateMigrationAction{Action: parts[0], TrainID: parts[1], TrainSHA256: parts[2]}
			if len(parts) == 4 {
				action.IntegrationSHA256 = parts[3]
			}
			if len(parts) == 6 {
				action.IntegrationSHA256, action.IntegrationMutationID, action.IntegrationMutationSHA256 = parts[3], parts[4], parts[5]
			}
			input.Actions = append(input.Actions, action)
		default:
			usage()
		}
		i++
	}
	if !modeSet || !projectSet || len(input.Actions) == 0 || (input.Apply && input.ExpectedHubRevision == "") {
		usage()
	}
	result, err := s.TrainV2MigrateLegacyState(ctx, input)
	if err != nil {
		fatal(err)
	}
	output(result)
}

func stateRetireRunState(ctx context.Context, s *service.Service) {
	input := service.RunRetirementInput{}
	modeSet, projectSet := false, false
	for i := 3; i < len(os.Args); i++ {
		if os.Args[i] == "--dry-run" || os.Args[i] == "--apply" {
			if modeSet {
				usage()
			}
			modeSet = true
			input.Apply = os.Args[i] == "--apply"
			continue
		}
		if i+1 >= len(os.Args) {
			usage()
		}
		value := os.Args[i+1]
		switch os.Args[i] {
		case "--project":
			input.ProjectID, projectSet = value, true
		case "--expected-hub-revision":
			input.ExpectedHubRevision = value
		default:
			usage()
		}
		i++
	}
	if !modeSet || !projectSet || (input.Apply && input.ExpectedHubRevision == "") {
		usage()
	}
	result, err := s.RetireRunRecords(ctx, input)
	if err != nil {
		fatal(err)
	}
	output(result)
}

func stateMigrateTrainV2Attempts(ctx context.Context, s *service.Service) {
	input := service.TrainV2AttemptMigrationInput{}
	modeSet, projectSet, trainSet := false, false, false
	for i := 3; i < len(os.Args); i++ {
		if os.Args[i] == "--dry-run" || os.Args[i] == "--apply" {
			if modeSet {
				usage()
			}
			modeSet = true
			input.Apply = os.Args[i] == "--apply"
			continue
		}
		if i+1 >= len(os.Args) {
			usage()
		}
		value := os.Args[i+1]
		switch os.Args[i] {
		case "--project":
			input.ProjectID, projectSet = value, true
		case "--train":
			input.TrainID, trainSet = value, true
		case "--expected-hub-revision":
			input.ExpectedHubRevision = value
		default:
			usage()
		}
		i++
	}
	if !modeSet || !projectSet || !trainSet {
		usage()
	}
	if input.Apply && input.ExpectedHubRevision == "" {
		fatal(fmt.Errorf("--expected-hub-revision is required with --apply"))
	}
	result, err := s.TrainV2MigrateAttempts(ctx, input)
	if err != nil {
		fatal(err)
	}
	output(result)
}

func upgradeResultShouldPrint(status string) bool {
	return status == "UPGRADE_ROLLED_BACK" || status == "UPGRADE_ROLLBACK_FAILED"
}
func copyExecutable(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("not a regular file: %s", src)
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".install-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, dst)
}
func usage() {
	fmt.Fprintln(os.Stderr, "usage: gpt-tunnelctl {install|init-config|upgrade [inspect|status]|start|stop|restart|restart-gateway|status|doctor|diagnose-startup|state {check|repair --dry-run|repair --apply|migrate-train-v2-attempts --project <project> --train <train> --dry-run|migrate-train-v2-attempts --project <project> --train <train> --apply|retire-run-state --project <project> --dry-run|retire-run-state --project <project> --apply|migrate-train-v2-legacy --project <project> --action action:train:sha[:opsha[:mutation:mutationsha]] --dry-run|migrate-train-v2-legacy --project <project> --action action:train:sha[:opsha[:mutation:mutationsha]] --apply --expected-hub-revision <sha>}|logs [gateway|tunnel|all] [lines]|version}")
	os.Exit(2)
}
func fatal(err error) { fmt.Fprintln(os.Stderr, "gpt-tunnelctl:", err); os.Exit(1) }
func output(v any)    { b, _ := json.MarshalIndent(v, "", "  "); fmt.Println(string(b)) }
