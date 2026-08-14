package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/runtime_log"
)

func TestCompactAgentPromptResultSuccessOmitsExecutionReceipt(t *testing.T) {
	result := compactAgentPromptResult("example", AgentSendResult{
		ProjectID: "example",
		Delivered: true,
		ExitCode:  0,
		Stdout:    "agent response",
		Stderr:    "Controller warning",
	}, nil)
	wire, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if string(wire) != `{"project_id":"example","delivered":true}` {
		t.Fatalf("compact success=%s", wire)
	}
	if strings.Contains(string(wire), "started_at") || strings.Contains(string(wire), "stdout") || strings.Contains(string(wire), "stderr") {
		t.Fatalf("success exposed execution receipt: %s", wire)
	}
}

func TestCompactAgentPromptResultFailurePreservesOnlyBoundedDiagnostics(t *testing.T) {
	result := compactAgentPromptResult("example", AgentSendResult{
		ProjectID: "example",
		Delivered: false,
		ExitCode:  7,
		Stdout:    "unneeded output",
		Stderr:    "delivery failed",
	}, nil)
	if result.ProjectID != "example" || result.Delivered || result.ExitCode != 7 || result.Stderr != "delivery failed" {
		t.Fatalf("failure diagnostics=%#v", result)
	}
	wire, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(wire), "unneeded output") || strings.Contains(string(wire), "started_at") {
		t.Fatalf("failure exposed non-decision diagnostics: %s", wire)
	}
}

func TestCompactAgentPromptResultUnavailableIncludesReason(t *testing.T) {
	result := compactAgentPromptResult("example", AgentSendResult{}, errForTest("session unavailable"))
	if result.ProjectID != "example" || result.Delivered || result.Error != "session unavailable" {
		t.Fatalf("unavailable result=%#v", result)
	}
}

func TestAgentPromptWarningUsesStructuredRuntimeLog(t *testing.T) {
	s := &Service{Config: config.Config{StateDir: t.TempDir()}}
	ctx := runtime_log.WithOperationID(context.Background(), "OPR-AGENT-PROMPT")
	s.recordAgentPromptWarning(ctx, "example", "Controller version warning")
	read, err := runtime_log.New(s.Config.StateDir).Read(runtime_log.Filter{Event: "agent_prompt_warning"})
	if err != nil {
		t.Fatal(err)
	}
	if len(read.Events) != 1 || read.Events[0].ProjectID != "example" || read.Events[0].OperationID != "OPR-AGENT-PROMPT" {
		t.Fatalf("warning event=%#v", read.Events)
	}
}

type testError string

func (e testError) Error() string { return string(e) }

func errForTest(message string) error { return testError(message) }
