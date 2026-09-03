package controller

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
)

// LaunchDetachedGatewayWorker starts a bounded, host-local worker with its
// own session. The worker arguments and receipt ownership belong to the
// caller; this controller method only supplies safe process mechanics.
func (c Controller) LaunchDetachedGatewayWorker(args []string, logName string) error {
	binary := c.Config.Controller.GatewayBinary
	if binary == "" {
		return fmt.Errorf("gateway binary is not configured")
	}
	if _, err := filepath.EvalSymlinks(binary); err != nil {
		return fmt.Errorf("resolve detached gateway worker: %w", err)
	}
	workingDir, err := c.gatewayWorkingDir()
	if err != nil {
		return err
	}
	if err := fsutil.EnsureDir(c.Config.Controller.LogDir, 0o700); err != nil {
		return err
	}
	log, err := os.OpenFile(filepath.Join(c.Config.Controller.LogDir, logName), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer log.Close()
	cmd := exec.Command(binary, args...)
	cmd.Dir = workingDir
	cmd.Env = processEnv([]string{"GPT_TUNNEL_CONFIG=" + c.ConfigPath})
	cmd.Stdin = nil
	cmd.Stdout = log
	cmd.Stderr = log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start detached gateway worker: %w", err)
	}
	_ = cmd.Process.Release()
	return nil
}
