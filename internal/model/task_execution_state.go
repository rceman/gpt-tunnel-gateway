package model

import "time"

// TaskExecutionState is the durable execution authority for one canonical
// Task. Full head is internal authority; public projections shorten it.
type TaskExecutionState struct {
	TaskID            string    `json:"task_id"`
	ProjectID         string    `json:"project_id"`
	Status            string    `json:"status"`
	Stage             string    `json:"stage"`
	Worktree          string    `json:"worktree"`
	Head              string    `json:"-"`
	Agent             string    `json:"agent"`
	ExecutionRevision int       `json:"execution_revision"`
	UpdatedAt         time.Time `json:"updated_at"`
}

const (
	TaskExecutionPlanned             = "planned"
	TaskExecutionDispatched          = "dispatched"
	TaskExecutionInProgress          = "in_progress"
	TaskExecutionAwaitingReview      = "awaiting_review"
	TaskExecutionChangesRequested    = "changes_requested"
	TaskExecutionReadyForIntegration = "ready_for_integration"
	TaskExecutionIntegrating         = "integrating"
	TaskExecutionIntegrated          = "integrated"
	TaskExecutionBlocked             = "blocked"
	TaskExecutionFailed              = "failed"
)
