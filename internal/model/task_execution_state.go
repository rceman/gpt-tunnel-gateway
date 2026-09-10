package model

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// TaskExecutionState is the durable execution authority for one canonical
// Task. Full head is internal authority; public projections shorten it.
type TaskExecutionState struct {
	TaskID             string    `json:"task_id"`
	ProjectID          string    `json:"project_id"`
	Status             string    `json:"status"`
	Stage              string    `json:"stage"`
	Worktree           string    `json:"worktree"`
	BaseHead           string    `json:"-"`
	Head               string    `json:"-"`
	Branch             string    `json:"-"`
	TaskRevision       int       `json:"-"`
	TaskRevisionSHA256 string    `json:"-"`
	Agent              string    `json:"agent"`
	ExecutionRevision  int       `json:"execution_revision"`
	UpdatedAt          time.Time `json:"updated_at"`
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

var taskExecutionWorktreePattern = regexp.MustCompile(`^WT-TSK[0-9]+-[a-f0-9]{8}$`)

func ValidateTaskExecutionState(v TaskExecutionState) error {
	if err := ValidateProjectIdentifier(v.ProjectID); err != nil {
		return fmt.Errorf("invalid Task execution project: %w", err)
	}
	if err := ValidateCanonicalTaskID(v.TaskID); err != nil {
		return fmt.Errorf("invalid Task execution Task: %w", err)
	}
	if v.Status == TaskExecutionPlanned || !validTaskExecutionStatus(v.Status) {
		return fmt.Errorf("invalid persisted Task execution status")
	}
	if v.Stage != "code" && v.Stage != "tests" && v.Stage != "rebase" {
		return fmt.Errorf("invalid persisted Task execution stage")
	}
	if err := ValidateObjectIdentifier(v.Agent); err != nil {
		return fmt.Errorf("invalid Task execution Agent: %w", err)
	}
	if ValidateCommitSHA(v.BaseHead) != nil || ValidateCommitSHA(v.Head) != nil || ValidateSHA256(v.TaskRevisionSHA256) != nil {
		return fmt.Errorf("invalid Task execution full head authority")
	}
	if !taskExecutionWorktreePattern.MatchString(v.Worktree) {
		return fmt.Errorf("invalid Task execution worktree")
	}
	idx := strings.LastIndex(v.TaskID, "-TSK")
	if idx < 0 || !strings.HasPrefix(v.Worktree, "WT-TSK"+v.TaskID[idx+4:]+"-") {
		return fmt.Errorf("Task execution worktree does not match Task")
	}
	if len(v.Head) != 40 || !strings.HasSuffix(v.Worktree, "-"+strings.ToLower(v.Head[:8])) {
		return fmt.Errorf("Task execution worktree does not match current head")
	}
	if v.ExecutionRevision < 1 || v.UpdatedAt.IsZero() || v.Branch == "" || v.TaskRevision < 1 {
		return fmt.Errorf("incomplete Task execution state")
	}
	if err := ValidateBranch(v.Branch); err != nil || !strings.HasPrefix(v.Branch, "task/"+v.TaskID+"-") {
		return fmt.Errorf("invalid server-derived Task execution branch")
	}
	return nil
}

func validTaskExecutionStatus(value string) bool {
	switch value {
	case TaskExecutionDispatched, TaskExecutionInProgress, TaskExecutionAwaitingReview, TaskExecutionChangesRequested, TaskExecutionReadyForIntegration, TaskExecutionIntegrating, TaskExecutionIntegrated, TaskExecutionBlocked, TaskExecutionFailed:
		return true
	default:
		return false
	}
}

// IsTaskExecutionTerminal is the single authority for current-task
// eligibility. Failed and integrated executions are terminal.
func IsTaskExecutionTerminal(status string) bool {
	return status == TaskExecutionIntegrated || status == TaskExecutionFailed
}

func IsTaskExecutionNonTerminal(status string) bool {
	return status != "" && !IsTaskExecutionTerminal(status)
}
