package controller

import (
	"context"
	"regexp"
	"strings"
)

type StartupDiagnosis struct {
	Phase           string `json:"phase"`
	ErrorCode       string `json:"error_code,omitempty"`
	FirstFatalLine  string `json:"first_fatal_line,omitempty"`
	ProcessExitCode *int   `json:"process_exit_code,omitempty"`
	Exited          bool   `json:"exited"`
	AliveButUnready bool   `json:"alive_but_unready"`
	Status          Status `json:"status"`
	GatewayLogTail  string `json:"gateway_log_tail,omitempty"`
	ConfigValidated bool   `json:"config_validated"`
	HubReachable    bool   `json:"hub_reachable"`
	ListenerReady   bool   `json:"listener_ready"`
}

var secretLineRE = regexp.MustCompile(`(?i)(CONTROL_PLANE_API_KEY|authorization|api[-_ ]?key|secret)[=:][^[:space:]]+`)

func sanitizeLogLine(line string) string {
	line = secretLineRE.ReplaceAllString(line, "$1=[redacted]")
	line = strings.Join(strings.Fields(line), " ")
	if len(line) > 512 {
		line = line[:512]
	}
	return line
}

func firstFatalLine(log string) string {
	for _, raw := range strings.Split(log, "\n") {
		line := sanitizeLogLine(raw)
		lower := strings.ToLower(line)
		if line != "" && (strings.Contains(lower, "fatal") || strings.Contains(lower, "error") || strings.Contains(lower, "failed") || strings.Contains(lower, "panic") || strings.Contains(lower, "missing")) {
			return line
		}
	}
	return ""
}

func (c Controller) DiagnoseStartup(ctx context.Context) StartupDiagnosis {
	result := StartupDiagnosis{
		Phase:           "CONFIG_LOAD",
		ConfigValidated: true,
	}
	status, err := c.Status(ctx)
	result.Status = status
	result.ListenerReady = status.GatewayReady
	result.HubReachable = status.GatewayReady
	if err != nil {
		result.ErrorCode = "STATUS_FAILED"
		result.FirstFatalLine = sanitizeLogLine(err.Error())
		return result
	}
	if !status.Gateway.Running {
		result.Phase = "PROCESS_VALIDATE"
		result.ErrorCode = "GATEWAY_NOT_RUNNING"
		result.Exited = true
	} else if !status.Gateway.IdentityValid {
		result.Phase = "PROCESS_VALIDATE"
		result.ErrorCode = "GATEWAY_PROCESS_IDENTITY_INVALID"
	} else if !status.GatewayReady {
		result.Phase = "READY"
		result.ErrorCode = "GATEWAY_NOT_READY"
		result.AliveButUnready = true
	} else if !status.VersionMatch {
		result.Phase = "MCP_INITIALIZE"
		result.ErrorCode = "VERSION_MISMATCH"
	} else {
		result.Phase = "READY"
	}
	if log, logErr := c.Logs("gateway", 100); logErr == nil {
		lines := strings.Split(log, "\n")
		for i, line := range lines {
			lines[i] = sanitizeLogLine(line)
		}
		result.GatewayLogTail = strings.Join(lines, "\n")
		if result.FirstFatalLine == "" {
			result.FirstFatalLine = firstFatalLine(result.GatewayLogTail)
		}
	}
	return result
}
