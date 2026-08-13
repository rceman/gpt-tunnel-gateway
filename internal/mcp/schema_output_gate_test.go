package mcp

import (
	"reflect"
	"strings"
	"testing"
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

func cloneMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
