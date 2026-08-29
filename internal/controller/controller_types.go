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
	Name                   string `json:"name"`
	Running                bool   `json:"running"`
	PID                    int    `json:"pid,omitempty"`
	Executable             string `json:"executable,omitempty"`
	ExpectedExecutable     string `json:"expected_executable"`
	CommandLine            string `json:"command_line,omitempty"`
	ExpectedCommandLine    string `json:"expected_command_line,omitempty"`
	ActualUID              uint32 `json:"actual_uid,omitempty"`
	ExpectedUID            uint32 `json:"expected_uid,omitempty"`
	IdentityValid          bool   `json:"identity_valid"`
	IdentityReason         string `json:"identity_reason,omitempty"`
	StartTimeTicks         uint64 `json:"start_time_ticks,omitempty"`
	ExpectedStartTimeTicks uint64 `json:"expected_start_time_ticks,omitempty"`
	ActualStartTimeTicks   uint64 `json:"actual_start_time_ticks,omitempty"`
}

// RuntimeIdentity is the server-owned, read-only proof used by CLI and MCP
// status surfaces. It contains the running Gateway identity, the complete
// installed control artifact set, and optional build provenance without
// accepting caller-selected PIDs or paths.
type RuntimeIdentity struct {
	GatewayPID                   int               `json:"gateway_pid,omitempty"`
	RunningExecutablePath        string            `json:"running_executable_path,omitempty"`
	RunningExecutableSHA256      string            `json:"running_executable_sha256,omitempty"`
	InstalledGatewaySHA256       string            `json:"installed_gateway_sha256,omitempty"`
	InstalledCLISHA256           string            `json:"installed_cli_sha256,omitempty"`
	InstalledCTLSHA256           string            `json:"installed_ctl_sha256,omitempty"`
	InstalledArtifactVersions    map[string]string `json:"installed_artifact_versions,omitempty"`
	ArtifactSetCoherent          bool              `json:"artifact_set_coherent"`
	RunningGatewayMatchesInstall bool              `json:"running_gateway_matches_installed"`
	InstalledVersion             string            `json:"installed_version,omitempty"`
	RunningVersion               string            `json:"running_version,omitempty"`
	VersionMatch                 bool              `json:"version_match"`
	GatewayReady                 bool              `json:"gateway_ready"`
	TunnelPID                    int               `json:"tunnel_pid,omitempty"`
	TunnelReady                  bool              `json:"tunnel_ready"`
	SourceSHA                    string            `json:"source_sha,omitempty"`
	SourceProvenanceAvailable    bool              `json:"source_provenance_available"`
	ExactSourceMatch             bool              `json:"exact_source_match"`
	ProvenanceReason             string            `json:"provenance_reason,omitempty"`
}
type Status struct {
	Gateway          ProcessStatus   `json:"gateway"`
	Tunnel           ProcessStatus   `json:"tunnel"`
	GatewayReady     bool            `json:"gateway_ready"`
	TunnelReady      bool            `json:"tunnel_ready"`
	InstalledVersion string          `json:"installed_version,omitempty"`
	RunningVersion   string          `json:"running_version,omitempty"`
	VersionMatch     bool            `json:"version_match"`
	RuntimeIdentity  RuntimeIdentity `json:"runtime_identity"`
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
	restartGatewayWaitFn          = waitURL
	gatewayRecoveryWorkerLaunchFn = func(c Controller, operationID string) error {
		return c.launchGatewayRecoveryWorker(operationID)
	}
	gatewayRecoveryStopFn  = func(c Controller) error { return c.stopProcess("gateway", c.Config.Controller.GatewayBinary) }
	gatewayRecoveryStartFn = func(c Controller) error {
		return c.startProcess("gateway", c.Config.Controller.GatewayBinary, []string{"--config", c.ConfigPath}, []string{"GPT_TUNNEL_CONFIG=" + c.ConfigPath})
	}
	gatewayRecoveryWaitFn = waitURL
)
