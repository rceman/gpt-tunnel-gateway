package model

import "fmt"

const (
	TaskPriorityP0 = "P0"
	TaskPriorityP1 = "P1"
	TaskPriorityP2 = "P2"
	TaskPriorityP3 = "P3"
	TaskPriorityP4 = "P4"
)

func TaskPriorities() []string {
	return []string{TaskPriorityP0, TaskPriorityP1, TaskPriorityP2, TaskPriorityP3, TaskPriorityP4}
}

func NormalizeTaskPriority(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	switch value {
	case TaskPriorityP0, TaskPriorityP1, TaskPriorityP2, TaskPriorityP3, TaskPriorityP4:
		return value, nil
	default:
		return "", fmt.Errorf("invalid task priority %q", value)
	}
}

func ValidateTaskPriority(value string, required bool) error {
	if value == "" {
		if required {
			return fmt.Errorf("task priority is required for executable Tasks")
		}
		return nil
	}
	_, err := NormalizeTaskPriority(value)
	return err
}

func TaskPriorityRank(value string) (int, error) {
	normalized, err := NormalizeTaskPriority(value)
	if err != nil {
		return 0, err
	}
	if normalized == "" {
		return 0, fmt.Errorf("task priority is required for ranking")
	}
	return int(normalized[1] - '0'), nil
}
