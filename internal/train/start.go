package train

import (
	"context"
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

// AgentTaskPacket is the bounded, Gateway-materialized input handed to an
// Agent for one Train Run. Both paths are server-derived; callers cannot
// provide either path.
type AgentTaskPacket struct {
	Path         string
	WorktreePath string
}

// PacketMaterializer writes and validates the exact Task packet for a Run.
// The service adapter supplies the Task/Hub-owned content while Train owns
// when it must exist before dispatch.
type PacketMaterializer func(context.Context, model.Run, RuntimeBinding) (AgentTaskPacket, error)

type StartDependencies struct {
	Hub               hub.Store
	Git               gitx.Runner
	Airelay           airelay.Client
	ProjectConfig     config.ProjectConfig
	Project           model.Project
	Policy            model.ProjectWorkflowPolicy
	Train             model.TrainV2
	GatewayID         string
	StateDir          string
	MaterializePacket PacketMaterializer
	Now               func() time.Time
}

type StartResult struct {
	Record  model.TrainV2StartRecord `json:"record"`
	Run     model.Run                `json:"run"`
	Runtime RuntimeBinding           `json:"runtime"`
}
