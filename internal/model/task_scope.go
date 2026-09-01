package model

import (
	"fmt"
	"strings"
)

const (
	TaskExecutionTrain    TaskExecution = "train"
	TaskExecutionHotfix   TaskExecution = "hotfix"
	MaxTaskScopeItems                   = 128
	MaxTaskScopeItemBytes               = 1024
)

type TaskExecution string

func NormalizeTaskExecution(value TaskExecution) (TaskExecution, error) {
	if value == "" {
		return TaskExecutionTrain, nil
	}
	switch value {
	case TaskExecutionTrain, TaskExecutionHotfix:
		return value, nil
	default:
		return "", fmt.Errorf("invalid task execution %q", value)
	}
}

func DefaultTaskExecution(value TaskExecution) TaskExecution {
	if value == "" {
		return TaskExecutionTrain
	}
	return value
}

type TaskScope struct {
	Files   []string `json:"files"`
	Modules []string `json:"modules"`
}

func NormalizeTaskScope(value *TaskScope) (*TaskScope, error) {
	if value == nil {
		return nil, nil
	}
	result := &TaskScope{
		Files:   append([]string(nil), value.Files...),
		Modules: append([]string(nil), value.Modules...),
	}
	if result.Files == nil {
		result.Files = []string{}
	}
	if result.Modules == nil {
		result.Modules = []string{}
	}
	if err := ValidateTaskScope(result); err != nil {
		return nil, err
	}
	return result, nil
}

func ValidateTaskScope(value *TaskScope) error {
	if value == nil {
		return nil
	}
	if len(value.Files) > MaxTaskScopeItems || len(value.Modules) > MaxTaskScopeItems {
		return fmt.Errorf("task scope exceeds %d items", MaxTaskScopeItems)
	}
	for _, item := range append(append([]string{}, value.Files...), value.Modules...) {
		if strings.TrimSpace(item) == "" || strings.ContainsAny(item, "\x00\r\n") || len([]byte(item)) > MaxTaskScopeItemBytes {
			return fmt.Errorf("invalid task scope item")
		}
	}
	return nil
}
