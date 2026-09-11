package model

import (
	"testing"
	"time"
)

func TestTSK521TerminalTaskReadySealValidation(t *testing.T) {
	for _, status := range []string{TaskAuthoringDone, TaskAuthoringArchived} {
		t.Run(status+" without seal", func(t *testing.T) {
			task := validTaskAuthoringForTest()
			task.Summary = "A bounded summary."
			task.Status = status
			var err error
			task.RevisionSHA256, err = HashTaskAuthoring(task)
			if err != nil {
				t.Fatal(err)
			}
			if err := ValidateTaskAuthoring(task); err != nil {
				t.Fatalf("terminal task without seal rejected: %v", err)
			}
		})
		t.Run(status+" with seal", func(t *testing.T) {
			task := validTaskAuthoringForTest()
			task.Summary = "A bounded summary."
			task.Status = status
			var err error
			task.RevisionSHA256, err = HashTaskAuthoring(task)
			if err != nil {
				t.Fatal(err)
			}
			task.ReadySeal = &TaskReadySeal{
				Revision:       task.Revision,
				RevisionSHA256: task.RevisionSHA256,
				ReadyBy:        "planner",
				ReadyAt:        time.Now().UTC(),
			}
			if err := ValidateTaskAuthoring(task); err == nil {
				t.Fatal("terminal task with seal accepted")
			}
		})
	}
}
