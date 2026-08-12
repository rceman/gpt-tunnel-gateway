package controller

import (
	"context"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
)

type Controller struct {
	Config     config.Config
	ConfigPath string
}
type ProcessStatus struct {
	Name               string `json:"name"`
	Running            bool   `json:"running"`
	PID                int    `json:"pid,omitempty"`
	Executable         string `json:"executable,omitempty"`
	ExpectedExecutable string `json:"expected_executable"`
	IdentityValid      bool   `json:"identity_valid"`
	IdentityReason     string `json:"identity_reason,omitempty"`
	StartTimeTicks     uint64 `json:"start_time_ticks,omitempty"`
}
type Status struct {
	Gateway          ProcessStatus     `json:"gateway"`
	Tunnel           ProcessStatus     `json:"tunnel"`
	GatewayReady     bool              `json:"gateway_ready"`
	TunnelReady      bool              `json:"tunnel_ready"`
	InstalledVersion string            `json:"installed_version,omitempty"`
	RunningVersion   string            `json:"running_version,omitempty"`
	VersionMatch     bool              `json:"version_match"`
	Degraded         bool              `json:"degraded"`
	DegradedReason   string            `json:"degraded_reason,omitempty"`
	State            string            `json:"state"`
	Reason           string            `json:"reason,omitempty"`
	ComponentReasons map[string]string `json:"component_reasons,omitempty"`
	MCPAvailable     bool              `json:"mcp_available"`
	RecoveryStatus   string            `json:"recovery_status,omitempty"`
	RecoveryAttempts int               `json:"recovery_attempts,omitempty"`
}

type GatewayStartupDiagnostics struct {
	Phase                string
	CaptureStatus        string
	TargetPID            int
	TargetProcessRunning bool
	TargetProcessExited  bool
	ProcessStateError    error
	AliveButUnready      bool
	Elapsed              time.Duration
	ReadinessPassed      bool
	Error                error
	LogDelta             string
	LogDeltaTruncated    bool
	LogCaptureError      error
}

type pidRecord struct {
	PID            int    `json:"pid"`
	StartTimeTicks uint64 `json:"start_time_ticks"`
	UID            uint32 `json:"uid"`
	InstanceToken  string `json:"instance_token"`
}

var (
	startStatusFn  = func(ctx context.Context, c Controller) (Status, error) { return c.Status(ctx) }
	startGatewayFn = func(c Controller) error {
		return c.startProcess("gateway", c.Config.Controller.GatewayBinary, []string{"--config", c.ConfigPath}, []string{"GPT_TUNNEL_CONFIG=" + c.ConfigPath})
	}
	startTunnelFn = func(c Controller, env []string) error {
		return c.startProcess("tunnel", c.Config.Controller.TunnelClientBinary, []string{"run"}, env)
	}
	startGatewayReadyFn   = func(c Controller) error { return waitURL(c.gatewayReadyURL(), true, 30*time.Second) }
	startTunnelReadyFn    = func(c Controller) error { return waitURL(c.tunnelReadyURL(), true, 30*time.Second) }
	recoveryStatusFn      = func(ctx context.Context, c Controller) (Status, error) { return c.Status(ctx) }
	recoveryStartFn       = func(c Controller) error { return startGatewayFn(c) }
	recoveryReadyFn       = func(c Controller) error { return startGatewayReadyFn(c) }
	recoveryStopFn        = func(c Controller) error { return c.stopProcess("gateway", c.Config.Controller.GatewayBinary) }
	recoverySleepFn       = time.Sleep
	restartGatewayStopFn  = func(c Controller) error { return c.stopProcess("gateway", c.Config.Controller.GatewayBinary) }
	restartGatewayStartFn = func(c Controller) error {
		return c.startProcess("gateway", c.Config.Controller.GatewayBinary, []string{"--config", c.ConfigPath}, []string{"GPT_TUNNEL_CONFIG=" + c.ConfigPath})
	}
	restartGatewayWaitFn = waitURL
)
