package controller

import (
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
	Gateway          ProcessStatus `json:"gateway"`
	Tunnel           ProcessStatus `json:"tunnel"`
	GatewayReady     bool          `json:"gateway_ready"`
	TunnelReady      bool          `json:"tunnel_ready"`
	InstalledVersion string        `json:"installed_version,omitempty"`
	RunningVersion   string        `json:"running_version,omitempty"`
	VersionMatch     bool          `json:"version_match"`
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
	restartGatewayStopFn  = func(c Controller) error { return c.stopProcess("gateway", c.Config.Controller.GatewayBinary) }
	restartGatewayStartFn = func(c Controller) error {
		return c.startProcess("gateway", c.Config.Controller.GatewayBinary, []string{"--config", c.ConfigPath}, []string{"GPT_TUNNEL_CONFIG=" + c.ConfigPath})
	}
	restartGatewayWaitFn = waitURL
)
