package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestAutomaticGatesRunInImmutableTaskOrder(t *testing.T) {
	previous := automaticGateExecutor
	t.Cleanup(func() { automaticGateExecutor = previous })
	var calls []string
	automaticGateExecutor = func(_ context.Context, _ string, definition automaticGateDefinition, _ time.Duration, _ int64) (model.CompletionGateResult, error) {
		calls = append(calls, definition.description)
		now := time.Now().UTC()
		return model.CompletionGateResult{Kind: "executable", Outcome: "passed", ExitCode: 0, Command: definition.description, Evidence: "test", StartedAt: &now, FinishedAt: &now}, nil
	}
	s := &Service{}
	results, status, err := s.runAutomaticGates(context.Background(), model.Task{RequiredGates: []string{"go vet ./...", "git diff --check", "gofmt check", "clean pushed branch"}}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if status != "succeeded" || len(results) != 4 {
		t.Fatalf("status=%q results=%#v", status, results)
	}
	if strings.Join(calls, "|") != "go vet ./...|git diff --check|gofmt -l .|git status --porcelain (published branch verified by canonical repository proof)" {
		t.Fatalf("gate order=%v", calls)
	}
	for i, result := range results {
		if result.ID != "G"+string(rune('1'+i)) || result.ExitCode != 0 || result.Outcome != "passed" {
			t.Fatalf("unexpected server evidence %#v", result)
		}
	}
}

func TestAutomaticGateTimeoutPolicyIsTypedFiniteAndDeterministic(t *testing.T) {
	cases := []struct {
		name  string
		class automaticGateTimeoutClass
		want  time.Duration
	}{
		{name: "git", class: automaticGateTimeoutTight, want: automaticGateTightTimeout},
		{name: "python", class: automaticGateTimeoutPython, want: automaticGatePythonTimeout},
		{name: "focused", class: automaticGateTimeoutFocused, want: automaticGateFocusedTimeout},
		{name: "vet", class: automaticGateTimeoutGoVet, want: automaticGateGoVetTimeout},
		{name: "test", class: automaticGateTimeoutGoTest, want: automaticGateGoTestTimeout},
		{name: "race", class: automaticGateTimeoutGoRace, want: automaticGateGoRaceTimeout},
	}
	for _, tc := range cases {
		definition := automaticGateDefinition{timeoutClass: tc.class}
		if got := automaticGateTimeoutFor(definition); got != tc.want || got <= 0 {
			t.Fatalf("%s timeout=%s want=%s", tc.name, got, tc.want)
		}
	}
	if automaticGateTimeoutFor(automaticGateDefinition{}) != automaticGateTightTimeout {
		t.Fatal("unknown timeout class did not fail closed to the tight bound")
	}
	if automaticGateGoTestTimeout <= automaticGateTightTimeout || automaticGateGoRaceTimeout <= automaticGateGoTestTimeout {
		t.Fatal("heavy Go gates do not have strictly larger finite bounds")
	}
}

func TestAutomaticGatesPassTypedTimeoutToExecutor(t *testing.T) {
	previous := automaticGateExecutor
	t.Cleanup(func() { automaticGateExecutor = previous })
	seen := map[string]time.Duration{}
	automaticGateExecutor = func(_ context.Context, _ string, definition automaticGateDefinition, timeout time.Duration, _ int64) (model.CompletionGateResult, error) {
		seen[definition.description] = timeout
		now := time.Now().UTC()
		return model.CompletionGateResult{Kind: "executable", Outcome: "passed", ExitCode: 0, Command: definition.description, Evidence: "test", StartedAt: &now, FinishedAt: &now}, nil
	}
	if _, status, err := (&Service{}).runAutomaticGates(context.Background(), model.Task{RequiredGates: []string{"git diff --check", "go test ./...", "go test -race ./..."}}, t.TempDir()); err != nil || status != "succeeded" {
		t.Fatalf("runAutomaticGates status=%q err=%v", status, err)
	}
	if seen["git diff --check"] != automaticGateTightTimeout || seen["go test ./..."] != automaticGateGoTestTimeout || seen["go test -race ./..."] != automaticGateGoRaceTimeout {
		t.Fatalf("typed timeout policy was not passed to executor: %#v", seen)
	}
}

func TestAutomaticGatesFailClosedForNonzeroAndUnsupported(t *testing.T) {
	previous := automaticGateExecutor
	t.Cleanup(func() { automaticGateExecutor = previous })
	automaticGateExecutor = func(_ context.Context, _ string, definition automaticGateDefinition, _ time.Duration, _ int64) (model.CompletionGateResult, error) {
		now := time.Now().UTC()
		return model.CompletionGateResult{Kind: "executable", Outcome: "failed", ExitCode: 7, Command: definition.description, Evidence: "non-zero", StartedAt: &now, FinishedAt: &now}, nil
	}
	s := &Service{}
	results, status, err := s.runAutomaticGates(context.Background(), model.Task{RequiredGates: []string{"go vet ./...", "manual: Delivery evidence"}}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if status != "failed" || results[0].ExitCode != 7 || results[1].Kind != "manual" || results[1].Outcome != "manual" {
		t.Fatalf("unexpected fail-closed result status=%q results=%#v", status, results)
	}
}

func TestAutomaticGateExecutorTimeoutAndOutputBound(t *testing.T) {
	timeoutResult, err := executeAutomaticGate(context.Background(), t.TempDir(), automaticGateDefinition{argv: []string{"sleep", "1"}, description: "sleep 1"}, 10*time.Millisecond, 64)
	if err != nil {
		t.Fatal(err)
	}
	if !timeoutResult.TimedOut || timeoutResult.Outcome != "timeout" || timeoutResult.ExitCode != -1 {
		t.Fatalf("unexpected timeout evidence %#v", timeoutResult)
	}
	outputResult, err := executeAutomaticGate(context.Background(), t.TempDir(), automaticGateDefinition{argv: []string{"printf", "123456"}, description: "printf 123456"}, time.Second, 3)
	if err != nil {
		t.Fatal(err)
	}
	if !outputResult.OutputTruncated || outputResult.Outcome != "failed" || len(outputResult.Stdout) != 3 {
		t.Fatalf("unexpected bounded output evidence %#v", outputResult)
	}
}

func TestAutomaticGateEvidenceRoundTripsAndLegacyGateInputStaysSmall(t *testing.T) {
	now := time.Now().UTC()
	gate := model.CompletionGateResult{ID: "G1", ExitCode: 0, Kind: "executable", Outcome: "passed", Command: "go vet ./...", Evidence: "command exited successfully", Stdout: "ok\n", StartedAt: &now, FinishedAt: &now}
	report := model.Report{SchemaVersion: model.SchemaVersion, TaskID: "EXM-TSK1", RunID: "EXM-TSK1-RUN1", ProjectID: "example", Status: "needs_gpt_revision", Summary: "bounded", GateResults: []model.CompletionGateResult{gate}, AcceptanceCoverage: []string{}, Deviations: []string{}, RemainingRisks: []string{}, Repository: model.RepositoryProof{Branch: "task/EXM-TSK1-example", Head: strings.Repeat("a", 40), DiffScope: strings.Repeat("b", 40) + ".." + strings.Repeat("a", 40)}, FinishedAt: now}
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "go vet ./...") || !strings.Contains(string(data), "started_at") {
		t.Fatalf("server gate evidence was not serialized: %s", data)
	}
	var roundTrip model.Report
	if err := json.Unmarshal(data, &roundTrip); err != nil || len(roundTrip.GateResults) != 1 || roundTrip.GateResults[0].Command != gate.Command {
		t.Fatalf("server gate evidence did not round-trip: %#v %v", roundTrip, err)
	}
	legacy := `{"schema_version":1,"run_id":"EXM-TSK1-RUN1","task_sha256":"` + strings.Repeat("a", 64) + `","status":"needs_gpt_revision","summary":"legacy","gate_results":[{"id":"G1","exit_code":0,"stdout":"forged"}],"acceptance_coverage":[],"deviations":[],"remaining_risks":[]}`
	if _, err := model.ParseCompletion([]byte(legacy), model.Task{ID: "EXM-TSK1", SHA256: strings.Repeat("a", 64)}); err == nil {
		t.Fatal("legacy Agent gate evidence was accepted")
	}
}
