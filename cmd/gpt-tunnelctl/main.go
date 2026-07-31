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
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/controller"
	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/upgrade"
)

var version = "0.4.0"

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
		upgradeRuntime()
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
	fmt.Fprintln(os.Stderr, "usage: gpt-tunnelctl {install|init-config|upgrade|start|stop|restart|restart-gateway|status|doctor|logs [gateway|tunnel|all] [lines]|version}")
	os.Exit(2)
}
func fatal(err error) { fmt.Fprintln(os.Stderr, "gpt-tunnelctl:", err); os.Exit(1) }
func output(v any)    { b, _ := json.MarshalIndent(v, "", "  "); fmt.Println(string(b)) }
