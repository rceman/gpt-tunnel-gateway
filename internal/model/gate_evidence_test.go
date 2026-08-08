package model

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func gateEvidenceFixture() (Task, Run, Report) {
	base := strings.Repeat("a", 40)
	task := Task{SchemaVersion: SchemaVersion, ID: "EXM-TSK52", ProjectID: "example", BaseRevision: base, Status: "created", RequiredGates: []string{"go test ./..."}}
	task.SHA256 = strings.Repeat("b", 64)
	run := Run{SchemaVersion: SchemaVersion, ID: "EXM-TSK52-RUN1", TaskID: task.ID, TaskSHA256: task.SHA256, ProjectID: task.ProjectID, Branch: "task/EXM-TSK52", BaseRevision: base, Status: "failed"}
	started := time.Date(2026, 8, 8, 12, 0, 0, 123000000, time.UTC)
	finished := started.Add(5 * time.Minute)
	report := Report{
		SchemaVersion: SchemaVersion, TaskID: task.ID, RunID: run.ID, ProjectID: task.ProjectID,
		Status: "failed", Summary: "server-owned gate evidence", GateResults: []CompletionGateResult{{
			ID: "G1", ExitCode: -1, Kind: "executable", Outcome: "timeout", Command: "go test ./...",
			Evidence: "command exceeded bounded timeout of 5m0s", Stdout: "partial output\n", StartedAt: &started,
			FinishedAt: &finished, TimedOut: true, OutputTruncated: true,
		}}, AcceptanceCoverage: []string{}, Deviations: []string{}, RemainingRisks: []string{},
		Repository: RepositoryProof{Branch: run.Branch, Head: strings.Repeat("c", 40), WorktreeClean: false, BaseAncestor: false, Commits: []string{}, ChangedFiles: []string{}, DiffScope: base + ".." + strings.Repeat("c", 40)}, FinishedAt: finished,
	}
	return task, run, report
}

func TestCompactV1ReportAndRichGateEvidenceRoundTripSeparately(t *testing.T) {
	task, run, richReport := gateEvidenceFixture()
	compact := CompactReport(richReport)
	compactData, err := json.Marshal(compact)
	if err != nil {
		t.Fatal(err)
	}
	var compactObject map[string]any
	if err := json.Unmarshal(compactData, &compactObject); err != nil {
		t.Fatal(err)
	}
	gate := compactObject["gate_results"].([]any)[0].(map[string]any)
	if len(gate) != 2 {
		t.Fatalf("compact v1 gate wire shape changed: %#v", gate)
	}
	if _, err := ParseReport(compactData, task, run); err != nil {
		t.Fatal(err)
	}

	evidence := NewGateEvidenceArtifact(richReport)
	evidenceData, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := ParseGateEvidence(evidenceData, task, run)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, evidence) {
		t.Fatalf("rich evidence changed across round-trip:\nwant %#v\n got %#v", evidence, decoded)
	}
	if err := ValidateGateEvidenceParity(compact, decoded); err != nil {
		t.Fatal(err)
	}
	if err := enrichReportForTest(&compact, decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(compact.GateResults, richReport.GateResults) {
		t.Fatalf("rich evidence did not enrich compact report: %#v", compact.GateResults)
	}
}

func enrichReportForTest(report *Report, evidence GateEvidenceArtifact) error {
	if err := ValidateGateEvidenceParity(*report, evidence); err != nil {
		return err
	}
	report.GateResults = append([]CompletionGateResult{}, evidence.GateResults...)
	return nil
}

func TestGateEvidenceRejectsUnknownIdentityOrderAndParityErrors(t *testing.T) {
	task, run, report := gateEvidenceFixture()
	evidence := NewGateEvidenceArtifact(report)
	data, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	unknown := strings.Replace(string(data), `"outcome":"timeout"`, `"outcome":"timeout","forged":true`, 1)
	if _, err := ParseGateEvidence([]byte(unknown), task, run); err == nil {
		t.Fatal("unknown rich evidence field was accepted")
	}
	wrongRun := evidence
	wrongRun.RunID = "EXM-TSK52-RUN2"
	if _, err := ParseGateEvidence(mustJSON(t, wrongRun), task, run); err == nil {
		t.Fatal("wrong-run evidence was accepted")
	}
	reordered := evidence
	reordered.GateResults = append([]CompletionGateResult{}, evidence.GateResults...)
	reordered.GateResults[0].ID = "G2"
	if _, err := ParseGateEvidence(mustJSON(t, reordered), task, run); err == nil {
		t.Fatal("reordered evidence was accepted")
	}
	compact := CompactReport(report)
	compact.GateResults[0].ExitCode = 0
	if err := ValidateGateEvidenceParity(compact, evidence); err == nil {
		t.Fatal("exit-code-mismatched evidence was accepted")
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
