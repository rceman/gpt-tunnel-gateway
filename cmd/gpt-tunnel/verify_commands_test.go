package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func TestNormalizeCLITimestampsUsesUTCSecondPrecision(t *testing.T) {
	input := struct {
		When time.Time `json:"when"`
	}{When: time.Date(2026, time.August, 22, 12, 34, 56, 789000000, time.FixedZone("local", 2*60*60))}
	data, err := json.Marshal(normalizeCLITimestamps(input))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(data), `{"when":"2026-08-22T10:34:56Z"}`; got != want {
		t.Fatalf("timestamp JSON=%s, want %s", got, want)
	}
}

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
