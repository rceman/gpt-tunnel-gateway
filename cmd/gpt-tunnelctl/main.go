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

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/controller"
	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
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
	if os.Args[1] == "install" {
		install(os.Args[2:])
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
		err = ctl.RestartGateway()
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
	case "reconcile-orphan-run":
		stateReconcileOrphanRun(ctx, s)
	default:
		usage()
	}
}

func stateReconcileOrphanRun(ctx context.Context, s *service.Service) {
	input := service.OrphanRunReconcileInput{
		ProjectID: "gpt-tunnel-gateway",
		RunID:     "GTW-TSK185-RUN1",
		Actor:     "gpt-tunnelctl",
		Reason:    "explicit recovery of the GTW-TSK185-RUN1 orphan run before gateway recovery",
	}
	modeSet := false
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
		case "--expected-hub-revision":
			input.ExpectedHubRevision = value
		case "--expected-original-sha256":
			input.ExpectedOriginalSHA256 = value
		case "--actor":
			input.Actor = value
		case "--session":
			input.Session = value
		case "--reason":
			input.Reason = value
		default:
			usage()
		}
		i++
	}
	if !modeSet || strings.TrimSpace(input.Reason) == "" {
		usage()
	}
	result, err := s.ReconcileOrphanRun(ctx, input)
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
	fmt.Fprintln(os.Stderr, "usage: gpt-tunnelctl {install|init-config|upgrade [inspect|status]|start|stop|restart|restart-gateway|status|doctor|diagnose-startup|state {check|repair --dry-run|repair --apply|reconcile-orphan-run --dry-run|reconcile-orphan-run --apply}|logs [gateway|tunnel|all] [lines]|version}")
	os.Exit(2)
}
func fatal(err error) { fmt.Fprintln(os.Stderr, "gpt-tunnelctl:", err); os.Exit(1) }
func output(v any)    { b, _ := json.MarshalIndent(v, "", "  "); fmt.Println(string(b)) }
