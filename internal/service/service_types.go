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
	Config       config.Config
	Hub          hub.Store
	Git          gitx.Runner
	Airelay      airelay.Client
	clock        func() time.Time
	gateExecutor func(context.Context, string, []string) ([]model.CompletionGateResult, error)
}

func New(c config.Config) *Service {
	executor := gates.NewExecutor()
	return &Service{
		Config:  c,
		Hub:     hub.Store{Config: c},
		Git:     gitx.Runner{MaxReadBytes: c.MaxReadBytes, MaxDiffBytes: c.MaxDiffBytes, MaxListItems: c.MaxListItems},
		Airelay: airelay.Client{Command: c.AirelayCommand, Timeout: time.Duration(c.DispatchTimeoutSeconds) * time.Second, MaxMessageBytes: 256},
		gateExecutor: func(ctx context.Context, root string, names []string) ([]model.CompletionGateResult, error) {
			return executor.Execute(ctx, root, names)
		},
	}
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
	ProjectID string   `json:"project_id"`
	TaskIDs   []string `json:"task_ids"`
	CreatedBy string   `json:"created_by"`
	WriteOptions
}

type TaskTrainPollInput struct {
	ProjectID string `json:"project_id"`
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

type DispatchInput struct {
	TaskID string `json:"task_id"`
	WriteOptions
}

type TaskMarkMergeReadyInput struct {
	TaskID string `json:"task_id"`
	WriteOptions
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
	RunID          string `json:"run_id"`
	CompletionFile string `json:"completion_file,omitempty"`
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
	Project        model.Project               `json:"project"`
	Local          config.ProjectConfig        `json:"local"`
	Worktree       gitx.WorktreeStatus         `json:"worktree"`
	Plan           model.PlanStatus            `json:"plan"`
	HubRevision    string                      `json:"hub_revision"`
	Progress       ProjectProgress             `json:"progress"`
	WorkflowPolicy ProjectWorkflowPolicyStatus `json:"workflow_policy"`
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
