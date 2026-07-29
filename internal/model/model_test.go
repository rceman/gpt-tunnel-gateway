package model

import (
	"testing"
	"time"
)

func TestTaskHashAndResultValidation(t *testing.T) {
	task := Task{SchemaVersion: 1, ID: "t", ProjectID: "p", Title: "Title", Objective: "Objective", Branch: "feature/x", BaseRevision: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", AcceptanceCriteria: []string{"done"}, Status: "created", CreatedBy: "gpt", CreatedAt: time.Now().UTC()}
	h, err := HashTask(task)
	if err != nil {
		t.Fatal(err)
	}
	task.SHA256 = h
	run := Run{SchemaVersion: 1, ID: "r", TaskID: "t", TaskSHA256: h, ProjectID: "p", DispatchMessage: "x"}
	res := AgentResult{SchemaVersion: 1, TaskID: "t", TaskSHA256: h, RunID: "r", Status: "succeeded", Summary: "ok", AcceptanceCoverage: []string{"done"}, FinishedAt: time.Now().UTC()}
	if err := ValidateAgentResult(res, task, run); err != nil {
		t.Fatal(err)
	}
}
func TestRelativePathRejectsEscape(t *testing.T) {
	if ValidateRelativePath("../x") == nil {
		t.Fatal("escape accepted")
	}
}
