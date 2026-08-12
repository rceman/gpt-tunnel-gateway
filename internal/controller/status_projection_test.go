package controller

import "testing"

func TestProjectRuntimeStatus(t *testing.T) {
	base := Status{
		Gateway: ProcessStatus{
			Running:       true,
			IdentityValid: true,
		},
		Tunnel: ProcessStatus{
			Running:       true,
			IdentityValid: true,
		},
		GatewayReady: true,
		TunnelReady:  true,
		VersionMatch: true,
	}
	tests := []struct {
		name       string
		status     Status
		recovery   gatewayRecoveryState
		state      string
		degraded   bool
		mcp        bool
		reason     string
		recoveryAt int
	}{
		{name: "healthy", status: base, state: "healthy", mcp: true},
		{name: "gateway down", status: func() Status { s := base; s.Gateway.Running = false; return s }(), state: "mcp-unavailable", degraded: true, reason: "gateway-not-running"},
		{name: "tunnel down", status: func() Status { s := base; s.Tunnel.Running = false; return s }(), state: "partial", degraded: true, mcp: true, reason: "tunnel-not-running"},
		{name: "recovery failed", status: base, recovery: gatewayRecoveryState{
			Status:   "degraded",
			Attempts: 3,
			Reason:   "readiness timeout with secret=hidden",
		}, state: "degraded", degraded: true, mcp: true, reason: "readiness timeout with secret=[redacted]", recoveryAt: 3},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := projectRuntimeStatus(test.status, test.recovery)
			if got.State != test.state || got.Degraded != test.degraded || got.MCPAvailable != test.mcp || got.Reason != test.reason || got.RecoveryAttempts != test.recoveryAt {
				t.Fatalf("projection=%+v", got)
			}
			if len(got.Reason) > 256 || len(got.ComponentReasons) > 2 {
				t.Fatalf("unbounded projection=%+v", got)
			}
		})
	}
}

func TestProjectRuntimeStatusBoundsRecoveryReason(t *testing.T) {
	status := projectRuntimeStatus(Status{}, gatewayRecoveryState{
		Status: "degraded",
		Reason: string(make([]byte, 1024)),
	})
	if len(status.Reason) != 256 || status.Reason[len(status.Reason)-3:] != "..." {
		t.Fatalf("reason length=%d value=%q", len(status.Reason), status.Reason)
	}
}
