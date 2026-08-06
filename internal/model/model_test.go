package model

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestTaskHashAndCompletionValidation(t *testing.T) {
	task := Task{SchemaVersion: 1, ID: "t", ProjectID: "p", Title: "Title", Objective: "Objective", Branch: "feature/x", BaseRevision: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", AcceptanceCriteria: []string{"done"}, Status: "created", CreatedBy: "gpt", CreatedAt: time.Now().UTC()}
	h, err := HashTask(task)
	if err != nil {
		t.Fatal(err)
	}
	task.SHA256 = h
	res := Completion{SchemaVersion: 1, RunID: "GTW-TSK1-RUN1", TaskSHA256: h, Status: "succeeded", Summary: "ok", GateResults: []CompletionGateResult{}, AcceptanceCoverage: []string{"AC1"}, Deviations: []string{}, RemainingRisks: []string{}}
	if err := ValidateCompletion(res, task); err != nil {
		t.Fatal(err)
	}
}

func TestHashTaskUsesCanonicalGoJSONForUnicodeAndTimestampFields(t *testing.T) {
	task := Task{
		SchemaVersion:      1,
		ID:                 "GTW-TSK1",
		ProjectID:          "gpt-tunnel-gateway",
		Title:              "Quotes \"and\" HTML-sensitive <tag>& unicode Привет",
		Objective:          "Line one\nLine two; emoji 🚀; preserve <, > and &",
		Branch:             "task/GTW-TSK1-unicode",
		BaseRevision:       strings.Repeat("a", 40),
		AcceptanceCriteria: []string{"AC1: preserve \u0000-free text"},
		Constraints:        []string{"bounded / exact / unicode"},
		RequiredGates:      []string{"G1"},
		Status:             "created",
		CreatedBy:          "tester",
		CreatedAt:          time.Date(2026, 8, 6, 12, 34, 56, 123456789, time.UTC),
	}
	wireTask := task
	wireTask.SHA256 = ""
	wire, err := json.Marshal(wireTask)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(wire)
	want := hex.EncodeToString(digest[:])
	got, err := HashTask(task)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("HashTask diverged from canonical Go JSON: got %s want %s", got, want)
	}
	if again, err := HashTask(task); err != nil || again != got {
		t.Fatalf("HashTask was not stable: got %s/%v", again, err)
	}
	changed := task
	changed.CreatedAt = changed.CreatedAt.Add(time.Nanosecond)
	changedHash, err := HashTask(changed)
	if err != nil {
		t.Fatal(err)
	}
	if changedHash == got {
		t.Fatal("HashTask ignored a timestamp change")
	}
}

func TestValidateTaskHashAcceptsStoredLegacyProjection(t *testing.T) {
	task := Task{
		SchemaVersion:      SchemaVersion,
		ID:                 "GTW-TSK1",
		ProjectID:          "gpt-tunnel-gateway",
		Title:              "Historical task",
		Objective:          "Preserve additive historical readability.",
		Branch:             "task/GTW-TSK1-historical",
		BaseRevision:       strings.Repeat("a", 40),
		AcceptanceCriteria: []string{"readable"},
		Constraints:        []string{"read-only compatibility"},
		Status:             "created",
		CreatedBy:          "test",
		CreatedAt:          time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC),
	}
	legacy, err := legacyTaskHash(task)
	if err != nil {
		t.Fatal(err)
	}
	task.SHA256 = legacy
	if err := ValidateTaskHash(task); err != nil {
		t.Fatalf("stored legacy task hash rejected: %v", err)
	}
	if err := ValidateTask(task); err != nil {
		t.Fatalf("legacy task rejected by ValidateTask: %v", err)
	}
	if canonical, err := HashTask(task); err != nil || canonical == task.SHA256 {
		t.Fatalf("fixture did not exercise legacy projection: canonical=%q err=%v", canonical, err)
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
	base := `{"schema_version":1,"run_id":"GTW-TSK1-RUN1","task_sha256":"` + h + `","status":"succeeded","summary":"ok","gate_results":[{"id":"G1","exit_code":0}],"acceptance_coverage":["AC1"],"deviations":[],"remaining_risks":[]}`
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
	c := Completion{SchemaVersion: 1, RunID: "GTW-TSK1-RUN1", TaskSHA256: h, Status: "needs_gpt_revision", Summary: "revision needed", GateResults: []CompletionGateResult{{ID: "G1", ExitCode: 1}}, AcceptanceCoverage: []string{"AC2"}, Deviations: []string{}, RemainingRisks: []string{}}
	if err := ValidateCompletion(c, task); err != nil {
		t.Fatal(err)
	}
}

func TestCompletionRejectsWhitespaceAndInvalidUTF8(t *testing.T) {
	task := Task{SchemaVersion: 1, ID: "t", ProjectID: "p", Title: "Title", Objective: "Objective", Branch: "feature/x", BaseRevision: strings.Repeat("a", 40), AcceptanceCriteria: []string{"done"}, RequiredGates: []string{"gate"}, Status: "created", CreatedBy: "gpt", CreatedAt: time.Now().UTC()}
	h, err := HashTask(task)
	if err != nil {
		t.Fatal(err)
	}
	task.SHA256 = h
	base := `{"schema_version":1,"run_id":"GTW-TSK1-RUN1","task_sha256":"` + h + `","status":"succeeded","summary":"ok","gate_results":[{"id":"G1","exit_code":0}],"acceptance_coverage":["AC1"],"deviations":[],"remaining_risks":[]}`
	for _, bad := range []string{
		strings.Replace(base, `"summary":"ok"`, `"summary":" \t"`, 1),
		strings.Replace(base, `"deviations":[]`, `"deviations":["  "]`, 1),
		strings.Replace(base, `"remaining_risks":[]`, `"remaining_risks":["\n"]`, 1),
	} {
		if _, err := ParseCompletion([]byte(bad), task); err == nil {
			t.Fatal("blank completion text accepted")
		}
	}
	invalid := append([]byte(base), 0xff)
	if _, err := ParseCompletion(invalid, task); err == nil {
		t.Fatal("invalid UTF-8 accepted")
	}
}
