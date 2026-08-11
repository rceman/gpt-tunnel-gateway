package service

import (
	"context"
	"time"

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
	ExecutionModel       *string                             `json:"execution_model,omitempty"`
}

type ProjectConfigurationUpdateInput struct {
	ProjectID        string                    `json:"project_id"`
	ExpectedRevision int                       `json:"expected_revision"`
	Patch            ProjectConfigurationPatch `json:"patch"`
	UpdatedBy        string                    `json:"updated_by"`
	WriteOptions
}

type PlanUpdateInput struct {
	ProjectID        string    `json:"project_id"`
	Title            *string   `json:"title,omitempty"`
	Summary          *string   `json:"summary,omitempty"`
	CurrentObjective *string   `json:"current_objective,omitempty"`
	Queue            *[]string `json:"queue,omitempty"`
	ActiveTaskID     *string   `json:"active_task_id,omitempty"`
	ActiveRunID      *string   `json:"active_run_id,omitempty"`
	UpdatedBy        string    `json:"updated_by"`
	WriteOptions
}

type PlanSectionCreateInput struct {
	ProjectID        string `json:"project_id"`
	SectionID        string `json:"section_id"`
	Title            string `json:"title"`
	ShortDescription string `json:"short_description"`
	Description      string `json:"description"`
	UpdatedBy        string `json:"updated_by"`
	WriteOptions
}

type PlanSectionUpdateInput struct {
	ProjectID               string  `json:"project_id"`
	SectionID               string  `json:"section_id"`
	Title                   *string `json:"title,omitempty"`
	ShortDescription        *string `json:"short_description,omitempty"`
	Description             *string `json:"description,omitempty"`
	UpdatedBy               string  `json:"updated_by"`
	ExpectedSectionRevision int     `json:"expected_section_revision"`
	WriteOptions
}

type PlanSectionDeleteInput struct {
	ProjectID               string `json:"project_id"`
	SectionID               string `json:"section_id"`
	UpdatedBy               string `json:"updated_by"`
	ExpectedSectionRevision int    `json:"expected_section_revision"`
	WriteOptions
}

type ADRCreateInput struct {
	ADR model.ADR `json:"adr"`
	WriteOptions
}

type TaskCreateInput struct {
	ProjectID          string   `json:"project_id"`
	Slug               string   `json:"slug"`
	Title              string   `json:"title"`
	Objective          string   `json:"objective"`
	AcceptanceCriteria []string `json:"acceptance_criteria"`
	Constraints        []string `json:"constraints"`
	RequiredGates      []string `json:"required_gates,omitempty"`
	OperationClass     string   `json:"operation_class"`
	CreatedBy          string   `json:"created_by"`
	Supersedes         string   `json:"supersedes,omitempty"`
	WriteOptions
}

type TaskAuthoringCreateInput struct {
	ProjectID             string            `json:"project_id"`
	Title                 string            `json:"title"`
	Objective             string            `json:"objective"`
	AcceptanceCriteria    []string          `json:"acceptance_criteria"`
	Constraints           []string          `json:"constraints"`
	Priority              string            `json:"priority,omitempty"`
	Dependencies          []string          `json:"dependencies,omitempty"`
	PreparationReferences []string          `json:"preparation_references,omitempty"`
	Metadata              map[string]string `json:"metadata,omitempty"`
	ADRRelation           string            `json:"adr_relation"`
	ADRReferences         []string          `json:"adr_references,omitempty"`
	CreatedBy             string            `json:"created_by"`
	WriteOptions
}

type TaskAuthoringUpdateInput struct {
	ProjectID              string             `json:"project_id"`
	TaskID                 string             `json:"task_id"`
	ExpectedRevision       int                `json:"expected_revision"`
	ExpectedRevisionSHA256 string             `json:"expected_revision_sha256,omitempty"`
	Title                  *string            `json:"title,omitempty"`
	Objective              *string            `json:"objective,omitempty"`
	AcceptanceCriteria     *[]string          `json:"acceptance_criteria,omitempty"`
	Constraints            *[]string          `json:"constraints,omitempty"`
	Priority               *string            `json:"priority,omitempty"`
	Dependencies           *[]string          `json:"dependencies,omitempty"`
	PreparationReferences  *[]string          `json:"preparation_references,omitempty"`
	Metadata               *map[string]string `json:"metadata,omitempty"`
	ADRRelation            *string            `json:"adr_relation,omitempty"`
	ADRReferences          *[]string          `json:"adr_references,omitempty"`
	UpdatedBy              string             `json:"updated_by"`
	WriteOptions
}

type TaskAuthoringReadyInput struct {
	ProjectID              string `json:"project_id"`
	TaskID                 string `json:"task_id"`
	ExpectedRevision       int    `json:"expected_revision"`
	ExpectedRevisionSHA256 string `json:"expected_revision_sha256,omitempty"`
	ReadyBy                string `json:"ready_by"`
	WriteOptions
}

type TaskAuthoringListInput struct {
	ProjectID string `json:"project_id"`
	Status    string `json:"status,omitempty"`
	Limit     int    `json:"limit,omitempty"`
}

type TaskAuthoringListResult struct {
	Tasks []model.TaskAuthoring `json:"tasks"`
}

type DispatchInput struct {
	TaskID               string `json:"task_id"`
	TrainID              string `json:"train_id,omitempty"`
	LaneBranch           string `json:"lane_branch,omitempty"`
	AgentID              string `json:"agent_id,omitempty"`
	RecommendedReasoning string `json:"recommended_reasoning,omitempty"`
	ResolvedReasoning    string `json:"resolved_reasoning,omitempty"`
	AgentFallback        bool   `json:"agent_fallback,omitempty"`
	AgentFallbackReason  string `json:"agent_fallback_reason,omitempty"`
	WriteOptions
}

type AgentResolveInput struct {
	ProjectID            string
	Role                 string
	AgentID              string
	RecommendedReasoning string
	RequireUsable        bool
}

type ResolvedAgent struct {
	ProjectID          string
	AgentID            string
	Role               string
	RequestedReasoning string
	ResolvedReasoning  string
	SessionKey         string
	Profile            string
	Fallback           bool
	FallbackReason     string
}

type TaskMarkMergeReadyInput struct {
	TaskID string `json:"task_id"`
	WriteOptions
}

// TaskReviewInput is the semantic, one-shot Delivery review payload. All
// repository, run, gate, and source fields are derived by the service from
// the exact terminal run; callers cannot supply machine-owned proof.
type TaskReviewInput struct {
	TaskID                  string                      `json:"task_id"`
	RunID                   string                      `json:"run_id"`
	Outcome                 string                      `json:"outcome"`
	Findings                []model.ReviewFinding       `json:"findings"`
	ScopeCoverage           []model.ReviewScopeCoverage `json:"scope_coverage"`
	UnexpectedSurfaces      []string                    `json:"unexpected_surfaces,omitempty"`
	HistoricalCompatibility []string                    `json:"historical_compatibility,omitempty"`
	ProhibitedActions       []string                    `json:"prohibited_actions,omitempty"`
}

type TaskIntegrationInput struct {
	TaskID string `json:"task_id"`
}

// TaskIntegrationReceipt is the compact server-owned result of one
// integration attempt. Runtime activation is represented explicitly so a
// post-merge activation failure cannot be mistaken for a successful merge.
type TaskIntegrationReceipt struct {
	TaskID              string `json:"task_id"`
	ReviewedHead        string `json:"reviewed_head"`
	IntegrationHead     string `json:"integration_head"`
	RuntimeSourceHead   string `json:"runtime_source_head"`
	PreActivation       string `json:"pre_activation"`
	PreSmoke            string `json:"pre_smoke"`
	PostActivation      string `json:"post_activation"`
	PostSmoke           string `json:"post_smoke"`
	Merged              bool   `json:"merged"`
	NextAction          string `json:"next_action"`
	ActivationBlocker   string `json:"activation_blocker,omitempty"`
	IntegrationConflict string `json:"integration_conflict,omitempty"`
}

type TaskDeferInput struct {
	TaskID string `json:"task_id"`
	Reason string `json:"reason"`
	WriteOptions
}

type TaskMarkMergedInput struct {
	TaskID          string `json:"task_id"`
	IntegrationHead string `json:"integration_head"`
	WriteOptions
}

type FinalizeInput struct {
	RunID          string               `json:"run_id"`
	Summary        string               `json:"summary,omitempty"`
	AgentFeedback  *model.AgentFeedback `json:"agent_feedback,omitempty"`
	CompletionFile string               `json:"completion_file,omitempty"`
	WriteOptions
}

type CompletionWriteInput struct {
	RunID          string `json:"run_id"`
	CompletionFile string `json:"completion_file"`
}

type CompletionWriteResult struct {
	Status    string `json:"status"`
	Path      string `json:"path"`
	ProjectID string `json:"project_id"`
	TaskID    string `json:"task_id"`
	RunID     string `json:"run_id"`
}

type OperationResult struct {
	Hub       hub.TransactionResult `json:"hub"`
	ProjectID string                `json:"project_id,omitempty"`
	TaskID    string                `json:"task_id,omitempty"`
	RunID     string                `json:"run_id,omitempty"`
	Status    string                `json:"status"`
}

type ProjectStatus struct {
	Project              model.Project               `json:"project"`
	Local                config.ProjectConfig        `json:"local"`
	Worktree             gitx.WorktreeStatus         `json:"worktree"`
	Plan                 model.PlanStatus            `json:"plan"`
	HubRevision          string                      `json:"hub_revision"`
	Progress             ProjectProgress             `json:"progress"`
	WorkflowPolicy       ProjectWorkflowPolicyStatus `json:"workflow_policy"`
	ProjectConfiguration ProjectConfigurationStatus  `json:"project_configuration"`
}

type ProjectConfigurationStatus struct {
	State         string                      `json:"state"`
	Revision      int                         `json:"revision"`
	Configuration *model.ProjectConfiguration `json:"configuration,omitempty"`
	Conflicts     []string                    `json:"conflicts"`
}

type ProjectWorkflowPolicyStatus struct {
	State                string                 `json:"state"`
	Revision             int                    `json:"revision"`
	WorkflowStage        string                 `json:"workflow_stage"`
	IntegrationBranch    string                 `json:"integration_branch"`
	AgentWaitForCI       bool                   `json:"agent_wait_for_ci"`
	CI                   model.WorkflowPolicyCI `json:"ci"`
	Gates                []string               `json:"gates"`
	ActiveOperationClass string                 `json:"active_operation_class"`
	ActiveCIMode         string                 `json:"active_ci_mode"`
	CIBlocking           bool                   `json:"ci_blocking"`
	Conflicts            []string               `json:"conflicts"`
	CorrectiveAction     string                 `json:"corrective_action"`
}

type TaskRecord struct {
	Task            model.Task                   `json:"task"`
	State           model.TaskState              `json:"state"`
	CurrentRevision *model.TaskRevision          `json:"current_revision,omitempty"`
	RunSummaries    []model.RunReviewSummary     `json:"run_summaries"`
	WorkflowPolicy  *model.ProjectWorkflowPolicy `json:"workflow_policy,omitempty"`
}

const (
	DefaultTaskListLimit = 10
	MaxTaskListLimit     = 10
)

type TaskListInput struct {
	ProjectID string `json:"project_id"`
	Query     string `json:"query,omitempty"`
	Status    string `json:"status,omitempty"`
	Limit     int    `json:"limit,omitempty"`
	Cursor    string `json:"cursor,omitempty"`
}

type TaskListResult struct {
	Tasks      []TaskRecord `json:"tasks"`
	NextCursor string       `json:"next_cursor"`
	HasMore    bool         `json:"has_more"`
}

type TaskPacket struct {
	Task            model.Task                  `json:"task"`
	CurrentRevision *model.TaskRevision         `json:"current_revision,omitempty"`
	Run             model.Run                   `json:"run"`
	RunSummaries    []model.RunReviewSummary    `json:"run_summaries"`
	Project         model.Project               `json:"project"`
	Plan            model.Plan                  `json:"plan"`
	WorkflowPolicy  model.ProjectWorkflowPolicy `json:"workflow_policy"`
	RepositoryRoot  string                      `json:"repository_root"`
	// CompletionPath is an internal diagnostic value only. The Agent packet
	// never instructs callers to use it; RunWriteCompletion derives the only
	// legal destination from StateDir and the canonical Run ID.
	CompletionPath  string `json:"-"`
	FinalizeCommand string `json:"finalize_command"`
	Text            string `json:"text"`
}
