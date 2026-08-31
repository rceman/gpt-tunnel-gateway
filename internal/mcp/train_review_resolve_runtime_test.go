package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

func TestTrainReviewResolveSchemaAndDispatchParity(t *testing.T) {
	server := newSessionTestServer(t)
	configureTrainV2MCPTest(t, server)
	sessionID := genericSessionWithRole(t, server.Service, "example", durableSession.RolePlanner)

	schema := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "schema", "arguments": map[string]any{"session": sessionID, "path": "train/review-resolve"}},
	})))
	if schema["kind"] != "action" || schema["path"] != "train/review-resolve" {
		t.Fatalf("review-resolve schema was not discoverable: %#v", schema)
	}

	response := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{
			"session_id": sessionID, "action": "train/review-resolve", "input": map[string]any{
				"project_id": "example", "train_id": "EXM-TRN1", "rejected_task_id": "EXM-TSK1",
				"rejected_item_position": 0, "rejected_attempt_number": 1, "rejected_review_id": "review-1",
				"rejected_reviewed_head": strings.Repeat("a", 40), "finding_ids": []string{"F1"},
				"corrections": []any{}, "resolving_head": strings.Repeat("b", 40), "reviewer_evidence": "bounded",
			},
		}},
	})))
	raw, _ := json.Marshal(response)
	if strings.Contains(string(raw), "unknown action") || strings.Contains(string(raw), "not registered") {
		t.Fatalf("review-resolve schema/dispatch drifted: %#v", response)
	}
}
