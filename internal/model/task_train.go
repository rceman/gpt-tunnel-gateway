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

const (
	TaskTrainReasoningSingleton = ReasoningHigh
	TaskTrainReasoningGroup     = ReasoningMax
)

// ExecutionGroup is a contiguous ordered slice of one train. A task may not
// occur in two groups, and groups may not reorder or skip train tasks.
type ExecutionGroup struct {
	GroupID              string   `json:"group_id"`
	TaskIDs              []string `json:"task_ids"`
	RecommendedReasoning string   `json:"recommended_reasoning,omitempty"`
}

type TaskTrainExecutionGroup = ExecutionGroup

// TaskTrain is the server-owned authorization boundary for one explicit,
// ordered sequence. Host-local worktree bindings deliberately do not appear
// here; LaneBranch and BaseRevision are the portable lane identity.
type TaskTrain struct {
	SchemaVersion   int              `json:"schema_version"`
	TrainID         string           `json:"train_id,omitempty"`
	ID              string           `json:"id,omitempty"`
	ProjectID       string           `json:"project_id"`
	TaskIDs         []string         `json:"task_ids"`
	ExecutionGroups []ExecutionGroup `json:"execution_groups,omitempty"`
	BaseRevision    string           `json:"base_revision,omitempty"`
	LaneBranch      string           `json:"lane_branch,omitempty"`
	CurrentIndex    int              `json:"current_index"`
	CurrentTaskID   string           `json:"current_task_id,omitempty"`
	CurrentRunID    string           `json:"current_run_id,omitempty"`
	Status          string           `json:"status"`
	WaitReason      string           `json:"wait_reason,omitempty"`
	UpdatedAt       time.Time        `json:"updated_at"`
}

func DefaultExecutionGroups(taskIDs []string, recommendedReasoning string) []ExecutionGroup {
	if len(taskIDs) == 0 {
		return []ExecutionGroup{}
	}
	if recommendedReasoning == "" {
		recommendedReasoning = TaskTrainReasoningSingleton
		if len(taskIDs) > 1 {
			recommendedReasoning = TaskTrainReasoningGroup
		}
	}
	return []ExecutionGroup{{GroupID: "group-1", TaskIDs: append([]string{}, taskIDs...), RecommendedReasoning: recommendedReasoning}}
}

func CanonicalTaskTrainID(v TaskTrain) string {
	if v.TrainID != "" {
		return v.TrainID
	}
	return v.ID
}

func ValidateTaskTrain(v TaskTrain) error {
	trainID := CanonicalTaskTrainID(v)
	if v.SchemaVersion != TaskTrainSchemaVersion || ValidateObjectIdentifier(trainID) != nil || !idRE.MatchString(v.ProjectID) || v.UpdatedAt.IsZero() {
		return fmt.Errorf("invalid task train identity")
	}
	if v.TrainID != "" && v.ID != "" && v.TrainID != v.ID {
		return fmt.Errorf("task train identity aliases disagree")
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
	if v.BaseRevision != "" {
		if err := ValidateRevision(v.BaseRevision); err != nil {
			return fmt.Errorf("invalid task train base revision: %w", err)
		}
	}
	if v.LaneBranch != "" {
		if err := ValidateBranch(v.LaneBranch); err != nil {
			return fmt.Errorf("invalid task train lane branch: %w", err)
		}
	}
	if len(v.ExecutionGroups) > 0 {
		if err := validateExecutionGroups(v.TaskIDs, v.ExecutionGroups); err != nil {
			return err
		}
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

func validateExecutionGroups(taskIDs []string, groups []ExecutionGroup) error {
	seen := map[string]bool{}
	position := 0
	for i, group := range groups {
		if ValidateObjectIdentifier(group.GroupID) != nil || group.GroupID == "current" {
			return fmt.Errorf("invalid execution group identity")
		}
		if len(group.TaskIDs) == 0 {
			return fmt.Errorf("execution group %d is empty", i)
		}
		if group.RecommendedReasoning != "" && !validReasoningTier(group.RecommendedReasoning) {
			return fmt.Errorf("invalid execution group reasoning")
		}
		for _, taskID := range group.TaskIDs {
			if position >= len(taskIDs) || taskID != taskIDs[position] || seen[taskID] {
				return fmt.Errorf("execution groups must partition ordered task IDs contiguously")
			}
			seen[taskID] = true
			position++
		}
	}
	if position != len(taskIDs) {
		return fmt.Errorf("execution groups do not cover every task")
	}
	return nil
}

func validReasoningTier(value string) bool {
	switch value {
	case ReasoningLow, ReasoningMedium, ReasoningHigh, ReasoningMax, ReasoningBestAvailable:
		return true
	default:
		return false
	}
}
