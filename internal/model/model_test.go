package model

import (
	"strings"
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

func TestCompletionRejectsDuplicateUnknownAndTrailingJSON(t *testing.T) {
	task := Task{SchemaVersion: 1, ID: "t", ProjectID: "p", Title: "Title", Objective: "Objective", Branch: "feature/x", BaseRevision: strings.Repeat("a", 40), AcceptanceCriteria: []string{"done"}, RequiredGates: []string{"gate"}, Status: "created", CreatedBy: "gpt", CreatedAt: time.Now().UTC()}
	h, err := HashTask(task)
	if err != nil {
		t.Fatal(err)
	}
	task.SHA256 = h
	base := `{"schema_version":1,"run_id":"00000000-0000-4000-8000-000000000001","task_sha256":"` + h + `","status":"succeeded","summary":"ok","gate_results":[{"id":"G1","exit_code":0}],"acceptance_coverage":["AC1"],"deviations":[],"remaining_risks":[]}`
	if _, err := ParseCompletion([]byte(base+" "+base), task); err == nil {
		t.Fatal("trailing JSON accepted")
	}
	if _, err := ParseCompletion([]byte(strings.Replace(base, `"summary":"ok"`, `"summary":"ok","summary":"again"`, 1)), task); err == nil {
		t.Fatal("duplicate JSON accepted")
	}
	if _, err := ParseCompletion([]byte(strings.Replace(base, `"remaining_risks":[]`, `"remaining_risks":[],"extra":1`, 1)), task); err == nil {
		t.Fatal("unknown field accepted")
	}
}

func TestCompletionNeedsRevisionAllowsOrderedAcceptanceSubset(t *testing.T) {
	task := Task{SchemaVersion: 1, ID: "t", ProjectID: "p", Title: "Title", Objective: "Objective", Branch: "feature/x", BaseRevision: strings.Repeat("a", 40), AcceptanceCriteria: []string{"one", "two"}, RequiredGates: []string{"gate-1", "gate-2"}, Status: "created", CreatedBy: "gpt", CreatedAt: time.Now().UTC()}
	h, err := HashTask(task)
	if err != nil {
		t.Fatal(err)
	}
	task.SHA256 = h
	c := Completion{SchemaVersion: 1, RunID: "00000000-0000-4000-8000-000000000001", TaskSHA256: h, Status: "needs_gpt_revision", Summary: "revision needed", GateResults: []CompletionGateResult{{ID: "G1", ExitCode: 1}}, AcceptanceCoverage: []string{"AC2"}, Deviations: []string{}, RemainingRisks: []string{}}
	if err := ValidateCompletion(c, task); err != nil {
		t.Fatal(err)
	}
}
