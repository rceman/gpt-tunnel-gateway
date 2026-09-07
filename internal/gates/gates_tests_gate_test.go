package gates

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestGateTimingWarningIsNonfatalAndBounded(t *testing.T) {
	result := timedGateResult(model.WorkflowGateTest, 0, gateOptimizationBudget+time.Millisecond)
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], GateOptimizationWarning) || result.DurationMS < 30000 {
		t.Fatalf("timing warning=%#v", result)
	}
	results := []model.CompletionGateResult{result}
	annotateGateAggregate(results, gateOptimizationBudget+time.Millisecond)
	if results[0].AggregateMS < 30000 || len(results[0].Warnings) != 2 {
		t.Fatalf("aggregate timing=%#v", results[0])
	}
}

func TestTestGateCommandContractPreservesFullCompatibility(t *testing.T) {
	legacy, err := TestGateContractDigest([]string{"format", "check", "test"})
	if err != nil {
		t.Fatal(err)
	}
	full, err := TestGateCommandContractDigest([]string{"format", "check", "test"}, FullTestScope())
	if err != nil || full != legacy {
		t.Fatalf("full contract changed: legacy=%s full=%s err=%v", legacy, full, err)
	}
	scoped, err := TestGateCommandContractDigest([]string{"format", "check", "test"}, TestScope{
		Mode:     TestScopePackages,
		Packages: []string{"./internal/gates"},
	})
	if err != nil || scoped == legacy {
		t.Fatalf("scoped contract did not differ: scoped=%s legacy=%s err=%v", scoped, legacy, err)
	}
}

func TestExecutorIncludesFullOutputForAnyFailedCommand(t *testing.T) {
	e := Executor{
		Tokens: func(context.Context, string) (TokenReport, error) {
			return TokenReport{Max: TokenFile{Path: "small.txt", Tokens: 1}}, nil
		},
		Command: func(context.Context, string, string, ...string) (int, string, error) {
			return 1, "FAIL custom-language: assertion failed\nsecond diagnostic line", errors.New("exit status 1")
		},
	}
	_, err := e.Execute(context.Background(), "/repo", []string{"test"})
	if err == nil || !strings.Contains(err.Error(), "gate test failed") || !strings.Contains(err.Error(), "FAIL custom-language: assertion failed") || !strings.Contains(err.Error(), "second diagnostic line") {
		t.Fatalf("failed gate omitted bounded command output: %v", err)
	}
}

func TestFixedCommandTailCapsOversizedMultilineOutput(t *testing.T) {
	code, output, err := fixedCommand(context.Background(), t.TempDir(), "sh", "-c", "i=0; while [ $i -lt 20000 ]; do printf 'line-%05d\\n' \"$i\"; i=$((i+1)); done; printf 'FINAL-CAUSAL-LINE\\n'; exit 1")
	if err == nil || code != 1 {
		t.Fatalf("oversized command result=%d err=%v", code, err)
	}
	if len(output) > maxGateOutputBytes {
		t.Fatalf("captured output exceeded cap: %d > %d", len(output), maxGateOutputBytes)
	}
	if !strings.HasPrefix(output, gateOutputTruncationMarker) {
		t.Fatalf("missing truncation marker: %q", output[:min(len(output), 80)])
	}
	if !strings.Contains(output, "FINAL-CAUSAL-LINE") {
		t.Fatal("tail output lost final causal line")
	}
	if strings.Contains(output, "line-00000") {
		t.Fatal("tail output retained the discarded beginning")
	}
}

func min(left, right int) int {
	if left < right {
		return left
	}
	return right
}
