package model

import "fmt"

// TaskType classifies the kind of work represented by a Task.
type TaskType string

const (
	TaskTypeTask  TaskType = "task"
	TaskTypeBug   TaskType = "bug"
	TaskTypePerf  TaskType = "perf"
	TaskTypeChore TaskType = "chore"
)

func TaskTypes() []string {
	return []string{string(TaskTypeTask), string(TaskTypeBug), string(TaskTypePerf), string(TaskTypeChore)}
}

func NormalizeTaskType(value TaskType) (TaskType, error) {
	if value == "" {
		return TaskTypeTask, nil
	}
	switch value {
	case TaskTypeTask, TaskTypeBug, TaskTypePerf, TaskTypeChore:
		return value, nil
	default:
		return "", fmt.Errorf("invalid task type %q", value)
	}
}

func DefaultTaskType(value TaskType) TaskType {
	if value == "" {
		return TaskTypeTask
	}
	return value
}
