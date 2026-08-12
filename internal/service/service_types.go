package service

import (
	"context"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/activation"
	"github.com/rceman/gpt-tunnel-gateway/internal/airelay"
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/gates"
	"github.com/rceman/gpt-tunnel-gateway/internal/gitx"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/pagination"
)

const (
	DefaultPublicCollectionLimit = pagination.DefaultLimit
	MaxPublicCollectionLimit     = pagination.MaxLimit
)

func PublicCollectionLimit(requested, configured int) (int, error) {
	return pagination.Limit(requested, configured)
}

type Service struct {
	Config                config.Config
	ConfigPath            string
	Hub                   hub.Store
	Git                   gitx.Runner
	Airelay               airelay.Client
	clock                 func() time.Time
	gateExecutor          func(context.Context, string, []string) ([]model.CompletionGateResult, error)
	gateExecutorWithScope func(context.Context, string, []string, gates.TestScope) ([]model.CompletionGateResult, error)
	taskActivator         func(context.Context, config.ProjectConfig, string) (TaskActivationResult, error)
	runtimeSourceProver   func(context.Context, config.ProjectConfig, string) (TaskActivationResult, error)
}

func New(c config.Config) *Service {
	executor := gates.NewExecutor()
	return &Service{
		Config:     c,
		ConfigPath: config.DefaultPath(),
		Hub:        hub.Store{Config: c},
		Git:        gitx.Runner{MaxReadBytes: c.MaxReadBytes, MaxDiffBytes: c.MaxDiffBytes, MaxListItems: c.MaxListItems},
		Airelay:    airelay.Client{Command: c.AirelayCommand, Timeout: time.Duration(c.DispatchTimeoutSeconds) * time.Second, MaxMessageBytes: 256},
		gateExecutor: func(ctx context.Context, root string, names []string) ([]model.CompletionGateResult, error) {
			return executor.Execute(ctx, root, names)
		},
		gateExecutorWithScope: func(ctx context.Context, root string, names []string, scope gates.TestScope) ([]model.CompletionGateResult, error) {
			return executor.ExecuteWithScope(ctx, root, names, scope)
		},
		taskActivator: func(ctx context.Context, project config.ProjectConfig, source string) (TaskActivationResult, error) {
			return activateTaskSource(ctx, c, config.DefaultPath(), project, source)
		},
		runtimeSourceProver: func(ctx context.Context, project config.ProjectConfig, source string) (TaskActivationResult, error) {
			result, err := activation.ProveSource(ctx, c, config.DefaultPath(), project, source)
			if err != nil {
				return TaskActivationResult{}, err
			}
			return TaskActivationResult{
				SourceHead: result.SourceHead,
				Activation: result.Activation,
				Smoke:      result.Smoke,
				TunnelPID:  result.TunnelPID,
				GatewayPID: result.GatewayPID,
			}, nil
		},
	}
}

type TaskActivationResult struct {
	SourceHead string `json:"source_head"`
	Activation string `json:"activation"`
	Smoke      string `json:"smoke"`
	TunnelPID  int    `json:"tunnel_pid,omitempty"`
	GatewayPID int    `json:"gateway_pid,omitempty"`
}

type WriteOptions struct {
	ExpectedHubRevision string `json:"expected_hub_revision"`
}

type ProjectRegisterInput struct {
	Project model.Project `json:"project"`
	WriteOptions
}

type CollectionPageInput struct {
	Limit  int    `json:"limit,omitempty"`
	Cursor string `json:"cursor,omitempty"`
}

type TaskTrainCreateInput struct {
	ProjectID       string                 `json:"project_id"`
	TrainID         string                 `json:"train_id,omitempty"`
	TaskIDs         []string               `json:"task_ids"`
	ExecutionGroups []model.ExecutionGroup `json:"execution_groups,omitempty"`
	BaseRevision    string                 `json:"base_revision,omitempty"`
	LaneBranch      string                 `json:"lane_branch,omitempty"`
	CreatedBy       string                 `json:"created_by"`
	WriteOptions
}

type TaskTrainPollInput struct {
	ProjectID string `json:"project_id"`
	TrainID   string `json:"train_id,omitempty"`
	Cursor    string `json:"cursor,omitempty"`
}

type TaskTrainStatus struct {
	ProjectID        string `json:"project_id"`
	TrainID          string `json:"train_id"`
	Status           string `json:"status"`
	CurrentIndex     int    `json:"current_index"`
	TaskCount        int    `json:"task_count"`
	CurrentTaskID    string `json:"current_task_id,omitempty"`
	CurrentRunID     string `json:"current_run_id,omitempty"`
	CurrentTaskState string `json:"current_task_state,omitempty"`
	CurrentRunStatus string `json:"current_run_status,omitempty"`
	AgentState       string `json:"agent_state,omitempty"`
	WaitReason       string `json:"wait_reason,omitempty"`
	NextTaskID       string `json:"next_task_id,omitempty"`
	Tail             string `json:"tail,omitempty"`
	NextCursor       string `json:"next_cursor,omitempty"`
	HasMore          bool   `json:"has_more"`
}

type ProjectListPageResult struct {
	Projects   []model.Project `json:"projects"`
	NextCursor string          `json:"next_cursor"`
	HasMore    bool            `json:"has_more"`
}

type RunListPageResult struct {
	Runs       []model.Run `json:"runs"`
	NextCursor string      `json:"next_cursor"`
	HasMore    bool        `json:"has_more"`
}

type ADRListPageResult struct {
	ADRs       []model.ADR `json:"adrs"`
	NextCursor string      `json:"next_cursor"`
	HasMore    bool        `json:"has_more"`
}

type TaskRevisionListPageResult struct {
	Revisions  []model.TaskRevision `json:"revisions"`
	NextCursor string               `json:"next_cursor"`
	HasMore    bool                 `json:"has_more"`
}

type PlanHistoryPageResult struct {
	History    []map[string]string `json:"history"`
	NextCursor string              `json:"next_cursor"`
	HasMore    bool                `json:"has_more"`
}

type ProjectIdentifiersAdoptInput struct {
	ProjectID   string `json:"project_id"`
	ProjectCode string `json:"project_code"`
	WriteOptions
}

type ProjectWorkflowPolicyInput struct {
	Policy model.ProjectWorkflowPolicy `json:"policy"`
	WriteOptions
}

type AgentRegisterInput struct {
	Agent model.Agent `json:"agent"`
	WriteOptions
}

type AgentUpdateInput struct {
	ProjectID            string    `json:"project_id"`
	AgentID              string    `json:"agent_id"`
	Enabled              *bool     `json:"enabled,omitempty"`
	Role                 *string   `json:"role,omitempty"`
	RecommendedReasoning *string   `json:"recommended_reasoning,omitempty"`
	Capabilities         *[]string `json:"capabilities,omitempty"`
	UpdatedBy            string    `json:"updated_by"`
	WriteOptions
}

type AgentDisableInput struct {
	ProjectID string `json:"project_id"`
	AgentID   string `json:"agent_id"`
	UpdatedBy string `json:"updated_by"`
	WriteOptions
}

type ProjectConfigurationPatch struct {
	AgentRouting         *model.ProjectAgentRouting          `json:"agent_routing,omitempty"`
	Watcher              *model.ProjectConfigurationWatcher  `json:"watcher,omitempty"`
	Workflow             *model.ProjectConfigurationWorkflow `json:"workflow,omitempty"`
	ActivationProfileRef *string                             `json:"activation_profile_ref,omitempty"`
}

type ProjectConfigurationUpdateInput struct {
	ProjectID        string                    `json:"project_id"`
	ExpectedRevision int                       `json:"expected_revision"`
	Patch            ProjectConfigurationPatch `json:"patch"`
	UpdatedBy        string                    `json:"updated_by"`
	WriteOptions
}
