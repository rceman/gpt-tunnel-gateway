package controller

import (
	"fmt"
	"strings"
)

func projectRuntimeStatus(status Status, recovery gatewayRecoveryState) Status {
	status.RecoveryStatus = recovery.Status
	status.RecoveryAttempts = recovery.Attempts
	status.ComponentReasons = map[string]string{}
	gatewayHealthy := status.Gateway.Running && status.Gateway.IdentityValid && status.GatewayReady
	tunnelHealthy := status.Tunnel.Running && status.Tunnel.IdentityValid && status.TunnelReady
	if !status.Gateway.Running {
		status.ComponentReasons["gateway"] = "gateway-not-running"
	} else if !status.Gateway.IdentityValid {
		status.ComponentReasons["gateway"] = "gateway-identity-invalid"
	} else if !status.GatewayReady {
		status.ComponentReasons["gateway"] = "gateway-not-ready"
	}
	if !status.Tunnel.Running {
		status.ComponentReasons["tunnel"] = "tunnel-not-running"
	} else if !status.Tunnel.IdentityValid {
		status.ComponentReasons["tunnel"] = "tunnel-identity-invalid"
	} else if !status.TunnelReady {
		status.ComponentReasons["tunnel"] = "tunnel-not-ready"
	}
	if !status.VersionMatch {
		status.ComponentReasons["mcp"] = "version-mismatch"
	}
	status.MCPAvailable = gatewayHealthy && status.VersionMatch
	switch {
	case recovery.Status == "degraded":
		status.State = "degraded"
		status.Reason = boundedStatusReason(recovery.Reason)
	case gatewayHealthy && tunnelHealthy && status.VersionMatch:
		status.State = "healthy"
	case !gatewayHealthy:
		status.State = "mcp-unavailable"
		status.Reason = status.ComponentReasons["gateway"]
	case !tunnelHealthy:
		status.State = "partial"
		status.Reason = status.ComponentReasons["tunnel"]
	default:
		status.State = "degraded"
		status.Reason = status.ComponentReasons["mcp"]
	}
	status.Degraded = status.State != "healthy"
	status.DegradedReason = status.Reason
	return status
}

func boundedStatusReason(reason string) string {
	reason = sanitizeLogLine(reason)
	reason = strings.Join(strings.Fields(reason), " ")
	if reason == "" {
		return "runtime-degraded"
	}
	if len(reason) > 256 {
		return fmt.Sprintf("%s...", reason[:253])
	}
	return reason
}
