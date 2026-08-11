package mcp

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

func TestCompletionGateResultSchemasAcceptCurrentEvidenceAndStayClosed(t *testing.T) {
	evidence := map[string]any{
		"id": "G1", "exit_code": float64(0), "execution": "reused",
		"tree_id": strings.Repeat("a", 40), "contract_digest": strings.Repeat("b", 64), "receipt_digest": strings.Repeat("c", 64),
	}
	if err := validateOutputValue(completionGateResultAnyIDOutputSchema(), evidence); err != nil {
		t.Fatalf("current CompletionGateResult evidence rejected: %v", err)
	}
	unknown := cloneMap(evidence)
	unknown["unexpected"] = true
	if err := validateOutputValue(completionGateResultAnyIDOutputSchema(), unknown); err == nil {
		t.Fatal("unknown CompletionGateResult field was accepted")
	}
	workflow := cloneMap(evidence)
	workflow["id"] = "G1"
	if err := validateOutputValue(completionGateResultWorkflowIDOutputSchema(), workflow); err == nil {
		t.Fatal("non-workflow gate id was accepted by workflow schema")
	}

	for name, item := range map[string]struct {
		schema map[string]any
		field  string
	}{
		"run_report":          {schema: reportOutputSchema(), field: "gate_results"},
		"review_report":       {schema: runReviewReportOutputSchema(), field: "gates"},
		"review_report_draft": {schema: runReviewDraftOutputSchema(), field: "gates"},
	} {
		properties := item.schema["properties"].(map[string]any)
		gates := properties[item.field].(map[string]any)["items"].(map[string]any)
		if !reflect.DeepEqual(gates, completionGateResultAnyIDOutputSchema()) {
			t.Fatalf("%s does not use the canonical CompletionGateResult schema: %#v", name, gates)
		}
	}
	snapshotProperties := reviewSnapshotOutputSchema()["properties"].(map[string]any)
	snapshotReport := snapshotProperties["report"].(map[string]any)["properties"].(map[string]any)
	if !reflect.DeepEqual(snapshotReport["gate_results"].(map[string]any)["items"], completionGateResultAnyIDOutputSchema()) ||
		!reflect.DeepEqual(snapshotReport["server_gate_results"].(map[string]any)["items"], completionGateResultAnyIDOutputSchema()) {
		t.Fatal("review snapshot does not use the canonical CompletionGateResult schema")
	}
}

func TestReviewReportStartGenericCallAcceptsExecutedAndReusedGateEvidence(t *testing.T) {
	server := &Server{Service: service.New(config.Config{GatewayID: "home_pc"})}
	entries := server.genericActionRegistry(server.tools())
	entry, ok := entries["task/review_report_start"]
	if !ok {
		t.Fatal("generic review report start action is not registered")
	}
	now := time.Now().UTC()
	draft := model.RunReviewReportDraft{
		SchemaVersion: model.RunReviewReportSchemaVersion, ID: "GTW-REV1", TaskID: "GTW-TSK1", RunID: "GTW-TSK1-RUN1", ProjectID: "example",
		TaskSHA256: strings.Repeat("d", 64), Branch: "feature/review", BaseRevision: strings.Repeat("e", 40), ReviewedHead: strings.Repeat("f", 40),
		RepositoryState:   model.ReviewRepositoryState{Branch: "feature/review", BaseRevision: strings.Repeat("e", 40), ReviewedHead: strings.Repeat("f", 40), WorktreeClean: true, BaseAncestor: true},
		Gates:             []model.CompletionGateResult{{ID: "G1", ExitCode: 0, Execution: "executed"}, {ID: "G2", ExitCode: 0, Execution: "reused", TreeID: strings.Repeat("a", 40), ContractDigest: strings.Repeat("b", 64), ReceiptDigest: strings.Repeat("c", 64)}},
		ServerGateResults: []model.CompletionGateResult{{ID: "test", ExitCode: 0, Execution: "reused", TreeID: strings.Repeat("a", 40), ContractDigest: strings.Repeat("b", 64), ReceiptDigest: strings.Repeat("c", 64)}},
		ChangedFiles:      []string{}, CompletedSections: []string{}, DraftRevision: 1, UpdatedAt: now,
	}
	entry.Execute = func(context.Context, json.RawMessage) (any, error) { return draft, nil }
	entries["task/review_report_start"] = entry
	result, err := server.genericDispatch(authority.WithDelivery(context.Background()), entries, durableSession.Record{}, "task/review_report_start", json.RawMessage(`{"task_id":"GTW-TSK1","run_id":"GTW-TSK1-RUN1"}`))
	if err != nil {
		t.Fatal(err)
	}
	if result["is_error"] != false {
		t.Fatalf("generic review report call rejected current gate evidence: %#v", result)
	}
}

func cloneMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
