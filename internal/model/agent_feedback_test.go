package model

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func validAgentFeedback() AgentFeedback {
	return AgentFeedback{
		Summary:      "Two small workflow improvements were identified.",
		Friction:     []string{"The long service gate gives little progress output."},
		Improvements: []string{"Expose bounded package progress."},
		ToolCandidates: []AgentFeedbackToolCandidate{{
			Problem:        "Repeatedly waiting for the same slow gate.",
			ProposedTool:   "bounded_gate_status",
			ExpectedReuse:  "recurring",
			ExpectedSaving: "Reduce idle waiting during long local gates.",
			SafetyBoundary: "Read-only status; never starts or retries a gate.",
		}},
		NoneObserved: false,
	}
}

func feedbackReportFixture() (Task, Run, Report) {
	task := Task{SchemaVersion: SchemaVersion, ID: "GTW-TSK1", ProjectID: "project", BaseRevision: strings.Repeat("a", 40), Status: "created"}
	task.SHA256 = strings.Repeat("b", 64)
	run := Run{SchemaVersion: SchemaVersion, ID: "GTW-TSK1-RUN1", TaskID: task.ID, TaskSHA256: task.SHA256, ProjectID: task.ProjectID, Branch: "task/GTW-TSK1", BaseRevision: task.BaseRevision, Status: "needs_gpt_revision", CompletionPath: "completion.json"}
	report := Report{
		SchemaVersion: SchemaVersion, TaskID: task.ID, RunID: run.ID, ProjectID: task.ProjectID,
		Status: "needs_gpt_revision", Summary: "bounded report", GateResults: []CompletionGateResult{},
		AcceptanceCoverage: []string{}, Deviations: []string{}, RemainingRisks: []string{},
		AgentFeedback: &AgentFeedback{}, Repository: RepositoryProof{
			Branch: "task/GTW-TSK1", Head: strings.Repeat("c", 40), WorktreeClean: true, BaseAncestor: true,
			Commits: []string{}, ChangedFiles: []string{}, DiffScope: strings.Repeat("a", 40) + ".." + strings.Repeat("c", 40),
		}, FinishedAt: time.Now().UTC(),
	}
	feedback := validAgentFeedback()
	report.AgentFeedback = &feedback
	return task, run, report
}

func TestAgentFeedbackRoundTripAndHistoricalReportCompatibility(t *testing.T) {
	task, run, report := feedbackReportFixture()
	if err := ValidateReport(report, task, run); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Report
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if err := ValidateReport(decoded, task, run); err != nil {
		t.Fatal(err)
	}
	if decoded.AgentFeedback == nil || decoded.AgentFeedback.ToolCandidates[0].ExpectedReuse != "recurring" {
		t.Fatalf("agent feedback was not preserved: %#v", decoded.AgentFeedback)
	}

	report.AgentFeedback = nil
	historical, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var historicalReport Report
	if err := json.Unmarshal(historical, &historicalReport); err != nil {
		t.Fatal(err)
	}
	if err := ValidateReport(historicalReport, task, run); err != nil {
		t.Fatalf("historical report without feedback rejected: %v", err)
	}
}

func TestCompletionAgentFeedbackRoundTripAndOptionalCompatibility(t *testing.T) {
	task := Task{SchemaVersion: SchemaVersion, ID: "GTW-TSK1", ProjectID: "project", Status: "created"}
	task.SHA256 = strings.Repeat("a", 64)
	feedback := validAgentFeedback()
	completion := CanonicalCompletion(Completion{
		SchemaVersion: SchemaVersion, RunID: "GTW-TSK1-RUN1", TaskSHA256: task.SHA256,
		Status: "needs_gpt_revision", Summary: "bounded completion", GateResults: []CompletionGateResult{},
		AcceptanceCoverage: []string{}, Deviations: []string{}, RemainingRisks: []string{}, AgentFeedback: &feedback,
	})
	data, err := CompletionJSON(completion)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := ParseCompletion(data, task)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.AgentFeedback == nil || decoded.AgentFeedback.Summary != feedback.Summary {
		t.Fatalf("completion feedback was not preserved: %#v", decoded.AgentFeedback)
	}

	legacy := completion
	legacy.AgentFeedback = nil
	legacyData, err := CompletionJSON(legacy)
	if err != nil {
		t.Fatal(err)
	}
	decodedLegacy, err := ParseCompletion(legacyData, task)
	if err != nil {
		t.Fatalf("completion without historical feedback rejected: %v", err)
	}
	if decodedLegacy.AgentFeedback != nil {
		t.Fatal("missing historical feedback became present")
	}
}

func TestAgentFeedbackSchemasAreOptionalClosedAndBounded(t *testing.T) {
	for _, filename := range []string{"gpt-tunnel-completion.schema.json", "report.schema.json"} {
		data, err := os.ReadFile(filepath.Join("..", "..", "schemas", filename))
		if err != nil {
			t.Fatal(err)
		}
		var schema map[string]any
		if err := json.Unmarshal(data, &schema); err != nil {
			t.Fatal(err)
		}
		properties := schema["properties"].(map[string]any)
		feedback := properties["agent_feedback"].(map[string]any)
		if feedback["additionalProperties"] != false {
			t.Fatalf("%s feedback schema is not closed", filename)
		}
		feedbackProperties := feedback["properties"].(map[string]any)
		if feedbackProperties["friction"].(map[string]any)["maxItems"].(float64) != 3 || feedbackProperties["improvements"].(map[string]any)["maxItems"].(float64) != 3 || feedbackProperties["tool_candidates"].(map[string]any)["maxItems"].(float64) != 3 {
			t.Fatalf("%s feedback array bounds are invalid", filename)
		}
		candidate := feedbackProperties["tool_candidates"].(map[string]any)["items"].(map[string]any)
		if candidate["additionalProperties"] != false {
			t.Fatalf("%s candidate schema is not closed", filename)
		}
		for _, item := range schema["required"].([]any) {
			if item == "agent_feedback" {
				t.Fatalf("%s agent_feedback is not optional", filename)
			}
		}
	}
}

func TestAgentFeedbackRejectsBoundsEnumsContradictionsAndNestedUnknownFields(t *testing.T) {
	valid := validAgentFeedback()
	tests := []struct {
		name string
		edit func(*AgentFeedback)
	}{
		{"friction bound", func(v *AgentFeedback) { v.Friction = []string{"a", "b", "c", "d"} }},
		{"improvement bound", func(v *AgentFeedback) { v.Improvements = []string{"a", "b", "c", "d"} }},
		{"candidate bound", func(v *AgentFeedback) {
			v.ToolCandidates = []AgentFeedbackToolCandidate{valid.ToolCandidates[0], valid.ToolCandidates[0], valid.ToolCandidates[0], valid.ToolCandidates[0]}
		}},
		{"enum", func(v *AgentFeedback) { v.ToolCandidates[0].ExpectedReuse = "sometimes" }},
		{"none observed contradiction", func(v *AgentFeedback) { v.NoneObserved = true }},
		{"oversized summary", func(v *AgentFeedback) { v.Summary = strings.Repeat("x", MaxAgentFeedbackSummaryBytes+1) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := valid
			test.edit(&value)
			if err := ValidateAgentFeedback(value); err == nil {
				t.Fatal("invalid agent feedback accepted")
			}
		})
	}

	data, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if err := json.Unmarshal(data, &object); err != nil {
		t.Fatal(err)
	}
	candidate := object["tool_candidates"].([]any)[0].(map[string]any)
	candidate["unexpected"] = true
	data, err = json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseAgentFeedback(data); err == nil {
		t.Fatal("nested unknown agent feedback field accepted")
	}
}
