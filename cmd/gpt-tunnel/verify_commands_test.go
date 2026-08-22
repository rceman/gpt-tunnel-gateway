package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func TestVerifySuccessProjectionIsCompactAndFailureKeepsDiagnostics(t *testing.T) {
	compact := struct {
		OperationID string   `json:"operation_id"`
		Status      string   `json:"status"`
		Scope       string   `json:"scope"`
		Packages    []string `json:"packages,omitempty"`
		Reused      bool     `json:"reused,omitempty"`
	}{
		OperationID: "verify-a",
		Status:      "completed",
		Scope:       "changed",
	}
	data, err := json.Marshal(compact)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "gates") || strings.Contains(string(data), "source_fingerprint") {
		t.Fatalf("success projection leaked diagnostics: %s", data)
	}
	failure := service.VerifyReceipt{Status: "failed", Error: "gate output", Gates: []model.CompletionGateResult{{ID: "test", ExitCode: 1}}}
	data, err = json.Marshal(failure)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "gate output") || !strings.Contains(string(data), "test") {
		t.Fatalf("failure diagnostics were lost: %s", data)
	}
}
