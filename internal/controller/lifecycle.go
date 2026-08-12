package controller

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
)

func (c Controller) startProcess(name, binary string, args, env []string) error {
	expected, err := filepath.EvalSymlinks(binary)
	if err != nil {
		return err
	}
	existing := c.process(name, expected)
	if existing.Running {
		return fmt.Errorf("%s already running", name)
	}
	if err := fsutil.EnsureDir(c.Config.Controller.PIDDir, 0o700); err != nil {
		return err
	}
	if err := fsutil.EnsureDir(c.Config.Controller.LogDir, 0o700); err != nil {
		return err
	}
	log, err := os.OpenFile(c.logPath(name), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer log.Close()
	cmd := exec.Command(binary, args...)
	cmd.Env = processEnv(env)
	cmd.Stdin = nil
	cmd.Stdout = log
	cmd.Stderr = log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	startTime, startErr := procStartTime(cmd.Process.Pid)
	if startErr != nil {
		_ = cmd.Process.Kill()
		return startErr
	}
	record := pidRecord{
		PID:            cmd.Process.Pid,
		StartTimeTicks: startTime,
		UID:            uint32(os.Getuid()),
		InstanceToken:  fmt.Sprintf("%d-%d", cmd.Process.Pid, startTime),
	}
	if err := fsutil.WriteJSONAtomic(c.pidPath(name), record, 0o600); err != nil {
		_ = cmd.Process.Kill()
		return err
	}
	return nil
}
func (c Controller) stopProcess(name, expected string) error {
	expected, _ = filepath.EvalSymlinks(expected)
	p := c.process(name, expected)
	if !p.Running {
		if p.PID != 0 && p.Executable != expected {
			return fmt.Errorf("refusing to signal unexpected executable %s", p.Executable)
		}
		_ = os.Remove(c.pidPath(name))
		return nil
	}
	if err := syscall.Kill(p.PID, syscall.SIGTERM); err != nil {
		return err
	}
	deadline := time.Now().Add(5 * time.Second)
	for alive(p.PID) && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if alive(p.PID) {
		_ = syscall.Kill(p.PID, syscall.SIGKILL)
	}
	_ = os.Remove(c.pidPath(name))
	return nil
}
func (c Controller) Start() error {
	lock, err := lockfile.Acquire(c.Config.Controller.PIDDir, "controller")
	if err != nil {
		return err
	}
	defer lock.Release()
	ctx := context.Background()
	initial, err := startStatusFn(ctx, c)
	if err != nil {
		return err
	}
	if initial.Gateway.Running && !initial.Gateway.IdentityValid {
		return fmt.Errorf("gateway is running with invalid process identity: %s", initial.Gateway.IdentityReason)
	}
	if initial.Tunnel.Running && !initial.Tunnel.IdentityValid {
		return fmt.Errorf("tunnel is running with invalid process identity: %s", initial.Tunnel.IdentityReason)
	}
	if initial.Gateway.Running && !initial.GatewayReady {
		return fmt.Errorf("gateway is running but not ready")
	}
	if initial.Tunnel.Running && !initial.TunnelReady {
		return fmt.Errorf("tunnel is running but not ready")
	}
	startedGateway := false
	startedTunnel := false
	if !initial.Gateway.Running {
		if err := startGatewayFn(c); err != nil {
			return err
		}
		startedGateway = true
		if err := startGatewayReadyFn(c); err != nil {
			_ = c.stopProcess("gateway", c.Config.Controller.GatewayBinary)
			return err
		}
	}
	if !initial.Tunnel.Running {
		env, err := readTunnelEnv(c.Config.Controller.TunnelEnvFile)
		if err != nil {
			if startedGateway {
				_ = c.stopProcess("gateway", c.Config.Controller.GatewayBinary)
			}
			return err
		}
		env = append(env, "MCP_SERVER_URL=http://"+c.Config.ListenAddr+"/mcp", "HEALTH_LISTEN_ADDR="+c.Config.Controller.TunnelHealthListenAddr)
		if err := startTunnelFn(c, env); err != nil {
			if startedGateway {
				_ = c.stopProcess("gateway", c.Config.Controller.GatewayBinary)
			}
			return err
		}
		startedTunnel = true
		if err := startTunnelReadyFn(c); err != nil {
			_ = c.stopProcess("tunnel", c.Config.Controller.TunnelClientBinary)
			if startedGateway {
				_ = c.stopProcess("gateway", c.Config.Controller.GatewayBinary)
			}
			return err
		}
	}
	final, err := startStatusFn(ctx, c)
	if err != nil {
		if startedTunnel {
			_ = c.stopProcess("tunnel", c.Config.Controller.TunnelClientBinary)
		}
		if startedGateway {
			_ = c.stopProcess("gateway", c.Config.Controller.GatewayBinary)
		}
		return err
	}
	if !final.Gateway.Running || !final.Gateway.IdentityValid || !final.GatewayReady || !final.Tunnel.Running || !final.Tunnel.IdentityValid || !final.TunnelReady || !final.VersionMatch {
		if startedTunnel {
			_ = c.stopProcess("tunnel", c.Config.Controller.TunnelClientBinary)
		}
		if startedGateway {
			_ = c.stopProcess("gateway", c.Config.Controller.GatewayBinary)
		}
		return fmt.Errorf("runtime did not converge to ready Gateway and Tunnel")
	}
	return nil
}
func (c Controller) Stop() error {
	lock, err := lockfile.Acquire(c.Config.Controller.PIDDir, "controller")
	if err != nil {
		return err
	}
	defer lock.Release()
	if err := c.stopProcess("tunnel", c.Config.Controller.TunnelClientBinary); err != nil {
		return err
	}
	return c.stopProcess("gateway", c.Config.Controller.GatewayBinary)
}
func (c Controller) Restart() error {
	if err := c.Stop(); err != nil {
		return err
	}
	return c.Start()
}
