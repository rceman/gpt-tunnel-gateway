package upgrade

import (
	"context"
	"regexp"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/controller"
)

var semverRE = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
var binaryOrder = []string{"gpt-tunnel-gatewayd", "gpt-tunnel", "gpt-tunnelctl"}

type Result struct {
	Status           string             `json:"status"`
	TransactionID    string             `json:"transaction_id,omitempty"`
	SourceRoot       string             `json:"source_root"`
	SourceSHA        string             `json:"source_sha"`
	Previous         string             `json:"previous_version"`
	Target           string             `json:"target_version"`
	InstalledVersion string             `json:"installed_version,omitempty"`
	RunningVersion   string             `json:"running_version,omitempty"`
	VersionMatch     bool               `json:"version_match"`
	GatewayPID       int                `json:"gateway_pid"`
	TunnelPID        int                `json:"tunnel_pid"`
	Rollback         bool               `json:"rollback"`
	Blockers         []PreflightBlocker `json:"blockers,omitempty"`
	Error            string             `json:"error,omitempty"`
}

type Runner struct {
	Config     config.Config
	ConfigPath string
	Target     string
}

type upgradeController interface {
	Status(context.Context) (controller.Status, error)
	Doctor(context.Context) error
	RestartGatewayAfterUpgrade() error
	StopGatewayForUpgrade() error
}

type startupDiagnosticController interface {
	RestartGatewayAfterUpgradeDiagnostics() (controller.GatewayStartupDiagnostics, error)
}

type liveUpgradeController struct{ controller.Controller }
