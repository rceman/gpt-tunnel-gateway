package service

import (
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/gitx"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

type OperationResult struct {
	OperationID string                `json:"operation_id,omitempty"`
	Hub         hub.TransactionResult `json:"hub"`
	ProjectID   string                `json:"project_id,omitempty"`
	TaskID      string                `json:"task_id,omitempty"`
	Status      string                `json:"status"`
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
	WorkflowPolicy  *model.ProjectWorkflowPolicy `json:"workflow_policy,omitempty"`
}

const (
	DefaultTaskListLimit = 10
	MaxTaskListLimit     = 10
)

type TaskListInput struct {
	ProjectID string         `json:"project_id"`
	Query     string         `json:"query,omitempty"`
	Status    string         `json:"status,omitempty"`
	Type      model.TaskType `json:"type,omitempty"`
	Limit     int            `json:"limit,omitempty"`
	Cursor    string         `json:"cursor,omitempty"`
}

type TaskListResult struct {
	Tasks      []TaskRecord `json:"tasks"`
	NextCursor string       `json:"next_cursor"`
	HasMore    bool         `json:"has_more"`
}
