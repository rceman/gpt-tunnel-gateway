package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/upgrade"
)

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
