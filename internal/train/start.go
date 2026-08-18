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

// PacketMaterializer writes and validates the exact Task packet for an
// item-local Attempt.
// The service adapter supplies the Task/Hub-owned content while Train owns
// when it must exist before dispatch.
type PacketMaterializer func(context.Context, model.TrainV2, model.TrainV2Item, model.TrainV2Attempt, RuntimeBinding) (AgentTaskPacket, error)

type StartDependencies struct {
	Hub                              hub.Store
	Git                              gitx.Runner
	Airelay                          airelay.Client
	ProjectConfig                    config.ProjectConfig
	Project                          model.Project
	Policy                           model.ProjectWorkflowPolicy
	Train                            model.TrainV2
	GatewayID                        string
	ProjectCode                      string
	SessionOrigin                    string
	StateDir                         string
	MaterializePacket                PacketMaterializer
	ReadTask                         func(context.Context, string, string) (model.TaskAuthoring, error)
	ReadTaskInWorktree               func(string, string, string) (model.TaskAuthoring, error)
	ValidateTaskMembershipInWorktree func(string, string, string) error
	Now                              func() time.Time
}

type StartResult struct {
	Record       model.TrainV2StartRecord `json:"record"`
	ItemPosition int                      `json:"item_position"`
	Attempt      model.TrainV2Attempt     `json:"attempt"`
	Runtime      RuntimeBinding           `json:"runtime"`
}
