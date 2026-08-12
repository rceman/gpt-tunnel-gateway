package train

import (
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/airelay"
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/gitx"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

type StartInput struct {
	ProjectID           string
	TrainID             string
	StartedBy           string
	AgentID             string
	RequestedReasoning  string
	ResolvedReasoning   string
	ResolvedAgentID     string
	SessionKey          string
	AgentFallback       bool
	AgentFallbackReason string
	ExpectedHubRevision string
}

type StartDependencies struct {
	Hub           hub.Store
	Git           gitx.Runner
	Airelay       airelay.Client
	ProjectConfig config.ProjectConfig
	Project       model.Project
	Policy        model.ProjectWorkflowPolicy
	Train         model.TrainV2
	GatewayID     string
	StateDir      string
	Now           func() time.Time
}

type StartResult struct {
	Record  model.TrainV2StartRecord `json:"record"`
	Run     model.Run                `json:"run"`
	Runtime RuntimeBinding           `json:"runtime"`
}
