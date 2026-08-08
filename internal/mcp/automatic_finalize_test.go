package mcp

import (
	"encoding/json"
	"testing"
)

func TestAutomaticFinalizeSchemaRejectsAgentGateBookkeeping(t *testing.T) {
	server := &Server{Service: nil}
	tool, ok := server.tools()["run_finalize"]
	if !ok {
		t.Fatal("run_finalize is not registered")
	}
	if err := validateToolArguments(tool.InputSchema, json.RawMessage(`{"run_id":"EXM-TSK1-RUN1","gate_results":[]}`)); err == nil {
		t.Fatal("Agent gate_results were accepted by canonical finalize")
	}
	if err := validateToolArguments(tool.InputSchema, json.RawMessage(`{"run_id":"EXM-TSK1-RUN1","acceptance_coverage":[]}`)); err == nil {
		t.Fatal("Agent acceptance_coverage was accepted by canonical finalize")
	}
	if err := validateToolArguments(tool.InputSchema, json.RawMessage(`{"run_id":"EXM-TSK1-RUN1","summary":"bounded"}`)); err != nil {
		t.Fatalf("bounded advisory finalize input rejected: %v", err)
	}
	if _, ok := tool.OutputSchema["properties"].(map[string]any); !ok {
		t.Fatal("run_finalize has no closed output schema")
	}
	if !tool.Annotations.DestructiveHint {
		t.Fatal("run_finalize is not marked as a mutation")
	}
}

func TestAutomaticReportSchemaAdvertisesCompleteServerGateEvidence(t *testing.T) {
	schema := reportOutputSchema()
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("report output schema has no properties")
	}
	gates, ok := properties["gate_results"].(map[string]any)
	if !ok {
		t.Fatal("report output schema has no gate_results")
	}
	items, ok := gates["items"].(map[string]any)
	if !ok {
		t.Fatal("report output schema has no gate item schema")
	}
	gateProperties, ok := items["properties"].(map[string]any)
	if !ok {
		t.Fatal("report gate schema has no properties")
	}
	for _, field := range []string{"kind", "outcome", "command", "evidence", "stdout", "stderr", "started_at", "finished_at", "timed_out", "output_truncated"} {
		if _, ok := gateProperties[field]; !ok {
			t.Fatalf("report gate schema omits server evidence field %q", field)
		}
	}
	if items["additionalProperties"] != false {
		t.Fatal("report gate schema must remain closed")
	}
}
