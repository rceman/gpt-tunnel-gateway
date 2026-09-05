package main

import (
	"context"
	"fmt"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/controller"
)

func daemon(ctx context.Context, args []string) {
	if len(args) != 1 {
		fatal(fmt.Errorf("usage: gpt-tunnel daemon {install|status|restart|remove}"))
	}
	path := config.DefaultPath()
	c, err := config.Load(path)
	if err != nil {
		fatal(err)
	}
	ctl := controller.Controller{Config: c, ConfigPath: path}
	operationContext, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	switch args[0] {
	case "install":
		result, err := ctl.DaemonInstall(operationContext)
		if err != nil {
			fatal(err)
		}
		output(result)
	case "status":
		result, err := ctl.DaemonStatus(operationContext)
		if err != nil {
			fatal(err)
		}
		output(result)
	case "restart":
		result, err := ctl.DaemonRestart(operationContext)
		if err != nil {
			fatal(err)
		}
		output(result)
	case "remove":
		if err := ctl.DaemonRemove(operationContext); err != nil {
			fatal(err)
		}
	default:
		fatal(fmt.Errorf("usage: gpt-tunnel daemon {install|status|restart|remove}"))
	}
}
