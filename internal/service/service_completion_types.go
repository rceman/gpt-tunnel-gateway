package service

import (
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/gitx"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

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
	TrainV2              *TrainV2ProjectStatus       `json:"train_v2,omitempty"`
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
