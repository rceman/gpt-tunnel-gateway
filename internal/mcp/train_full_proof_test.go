package mcp

import (
	"strings"
	"testing"
)

func TestTrainFullProofActionsExposeBoundedReceiptContracts(t *testing.T) {
	server := newSessionTestServer(t)
	for _, path := range []string{"train/full-proof", "train/full-proof_status"} {
		response := callMCP(t, server, mustJSON(t, map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"method":  "tools/call",
			"params":  map[string]any{"name": "schema", "arguments": map[string]any{"path": path}},
		}))
		structured := genericStructured(t, response)
		contract, ok := structured["contract"].(map[string]any)
		if !ok {
			t.Fatalf("missing %s contract: %#v", path, structured)
		}
		if contract["path"] != path || contract["domain"] != "train" {
			t.Fatalf("unexpected %s contract: %#v", path, contract)
		}
		if path == "train/full-proof" {
			input := contract["input_schema"].(map[string]any)
			required := input["required"].([]any)
			if len(required) != 1 || required[0] != "train_id" {
				t.Fatalf("full-proof required fields=%#v", required)
			}
		} else if !strings.Contains(strings.ToLower(contract["description"].(string)), "receipt") {
			t.Fatalf("status contract is not receipt-oriented: %#v", contract)
		}
	}
}
