package model

import (
	"strings"
	"testing"
	"time"
)

func TestTSK521TaskExecutionStateValidation(t *testing.T) {
	state := TaskExecutionState{
		TaskID:             "EXM-TSK521",
		ProjectID:          "example",
		Status:             TaskExecutionDispatched,
		Stage:              "code",
		Worktree:           "WT-TSK521-aaaaaaaa",
		BaseHead:           strings.Repeat("a", 40),
		Head:               strings.Repeat("a", 40),
		Branch:             "task/EXM-TSK521-task",
		Agent:              "EXM-CODER",
		TaskRevision:       2,
		TaskRevisionSHA256: strings.Repeat("b", 64),
		ExecutionRevision:  1,
		UpdatedAt:          time.Now().UTC(),
	}
	if err := ValidateTaskExecutionState(state); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*TaskExecutionState){
		"task hash length":  func(v *TaskExecutionState) { v.TaskRevisionSHA256 = strings.Repeat("B", 64) },
		"head suffix":       func(v *TaskExecutionState) { v.Worktree = "WT-TSK521-bbbbbbbb" },
		"planned persisted": func(v *TaskExecutionState) { v.Status = TaskExecutionPlanned },
	} {
		candidate := state
		mutate(&candidate)
		if err := ValidateTaskExecutionState(candidate); err == nil {
			t.Fatalf("%s accepted invalid state", name)
		}
	}
}
