package model

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func serverGateReportFixture() (Task, Run, Report) {
	base := strings.Repeat("a", 40)
	task := Task{SchemaVersion: SchemaVersion, ID: "EXM-TSK51", ProjectID: "example", BaseRevision: base, Status: "created", RequiredGates: []string{"go test ./..."}}
	task.SHA256 = strings.Repeat("b", 64)
	run := Run{SchemaVersion: SchemaVersion, ID: "EXM-TSK51-RUN1", TaskID: task.ID, TaskSHA256: task.SHA256, ProjectID: task.ProjectID, Branch: "task/EXM-TSK51", BaseRevision: base, Status: "failed"}
	started := time.Date(2026, 8, 8, 12, 0, 0, 123000000, time.UTC)
	finished := started.Add(5 * time.Minute)
	report := Report{
		SchemaVersion: SchemaVersion, TaskID: task.ID, RunID: run.ID, ProjectID: task.ProjectID,
		Status: "failed", Summary: "bounded server gate evidence", GateResults: []CompletionGateResult{{
			ID: "G1", ExitCode: -1, Kind: "executable", Outcome: "timeout", Command: "go test ./...",
			Evidence: "command exceeded bounded timeout of 5m0s", Stdout: "partial output\n", Stderr: "", StartedAt: &started,
			FinishedAt: &finished, TimedOut: true, OutputTruncated: true,
		}}, AcceptanceCoverage: []string{}, Deviations: []string{}, RemainingRisks: []string{},
		Repository: RepositoryProof{Branch: run.Branch, Head: strings.Repeat("c", 40), WorktreeClean: false, BaseAncestor: false, Commits: []string{}, ChangedFiles: []string{}, DiffScope: base + ".." + strings.Repeat("c", 40)}, FinishedAt: finished,
	}
	return task, run, report
}

func TestParseReportRoundTripsServerGateEvidenceStrictly(t *testing.T) {
	task, run, report := serverGateReportFixture()
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := ParseReport(data, task, run)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, report) {
		t.Fatalf("server gate evidence changed across strict round-trip:\nwant %#v\n got %#v", report, decoded)
	}

	unknown := strings.Replace(string(data), `"output_truncated":true`, `"output_truncated":true,"unexpected":true`, 1)
	if _, err := ParseReport([]byte(unknown), task, run); err == nil {
		t.Fatal("strict report parser accepted an unknown gate evidence field")
	}
}

func TestValidateReportRejectsSilentTimeoutOrTruncationPass(t *testing.T) {
	task, run, report := serverGateReportFixture()
	gate := &report.GateResults[0]
	gate.Outcome = "passed"
	if err := ValidateReport(report, task, run); err == nil {
		t.Fatal("timed-out gate was allowed to pass")
	}
	gate.Outcome = "timeout"
	gate.TimedOut = false
	if err := ValidateReport(report, task, run); err == nil {
		t.Fatal("timeout outcome without timed_out evidence was accepted")
	}
}
