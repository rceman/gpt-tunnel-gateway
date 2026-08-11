package model

import (
	"fmt"
	"strings"
	"time"
)

const (
	TaskTrainSchemaVersion = 1
	MaxTaskTrainTasks      = 32
)

const (
	TaskTrainActive          = "active"
	TaskTrainWaitingDelivery = "waiting_delivery"
	TaskTrainBlocked         = "blocked"
	TaskTrainCompleted       = "completed"
)

// TaskTrain is the server-owned authorization boundary for one explicit,
// ordered sequence. It never discovers or appends tasks from a backlog.
type TaskTrain struct {
	SchemaVersion int       `json:"schema_version"`
	ID            string    `json:"id"`
	ProjectID     string    `json:"project_id"`
	TaskIDs       []string  `json:"task_ids"`
	CurrentIndex  int       `json:"current_index"`
	CurrentTaskID string    `json:"current_task_id,omitempty"`
	CurrentRunID  string    `json:"current_run_id,omitempty"`
	Status        string    `json:"status"`
	WaitReason    string    `json:"wait_reason,omitempty"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func ValidateTaskTrain(v TaskTrain) error {
	if v.SchemaVersion != TaskTrainSchemaVersion || v.ID != "current" || !idRE.MatchString(v.ProjectID) || v.UpdatedAt.IsZero() {
		return fmt.Errorf("invalid task train identity")
	}
	if len(v.TaskIDs) < 1 || len(v.TaskIDs) > MaxTaskTrainTasks {
		return fmt.Errorf("invalid task train task count")
	}
	seen := map[string]bool{}
	for _, id := range v.TaskIDs {
		if err := ValidateObjectIdentifier(id); err != nil {
			return fmt.Errorf("invalid task train task: %w", err)
		}
		if seen[id] {
			return fmt.Errorf("duplicate task train task %q", id)
		}
		seen[id] = true
	}
	if v.CurrentIndex < 0 || v.CurrentIndex > len(v.TaskIDs) {
		return fmt.Errorf("invalid task train index")
	}
	switch v.Status {
	case TaskTrainActive, TaskTrainWaitingDelivery, TaskTrainBlocked:
		if v.CurrentIndex >= len(v.TaskIDs) || v.CurrentTaskID != v.TaskIDs[v.CurrentIndex] {
			return fmt.Errorf("invalid task train current task")
		}
	case TaskTrainCompleted:
		if v.CurrentIndex != len(v.TaskIDs) || v.CurrentTaskID == "" {
			return fmt.Errorf("invalid completed task train")
		}
	default:
		return fmt.Errorf("invalid task train status")
	}
	if v.CurrentRunID != "" && ValidateObjectIdentifier(v.CurrentRunID) != nil {
		return fmt.Errorf("invalid task train current run")
	}
	if v.Status == TaskTrainBlocked && strings.TrimSpace(v.WaitReason) == "" {
		return fmt.Errorf("blocked task train requires wait reason")
	}
	if v.Status != TaskTrainBlocked && v.Status != TaskTrainWaitingDelivery && v.WaitReason != "" {
		return fmt.Errorf("wait reason is only valid for blocked or delivery-waiting trains")
	}
	return nil
}
