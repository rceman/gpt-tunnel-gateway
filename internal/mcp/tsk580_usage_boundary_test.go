package mcp

import (
	"context"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
)

func TestTSK580TelemetryFailureDoesNotChangeAuthenticatedActionResult(t *testing.T) {
	server := newSessionTestServer(t)
	server.AuthorityContext = authority.WithPlanner(context.Background())
	sessionID := genericSession(t, server.Service, "example")
	if _, err := server.Service.Durability.Local.Exec(context.Background(), `DROP TABLE local_token_usage_events`); err != nil {
		t.Fatal(err)
	}
	response := genericActionResult(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{
			"session_id": sessionID, "action": "agent/guide", "input": map[string]any{},
		}},
	})))
	if len(response) == 0 {
		t.Fatalf("business response changed: %#v", response)
	}
}

func TestTSK580AuthenticatedSuccessErrorsAndRepeatsAreCounted(t *testing.T) {
	server := newSessionTestServer(t)
	server.AuthorityContext = authority.WithPlanner(context.Background())
	sessionID := genericSession(t, server.Service, "example")
	call := func(id int, action string) map[string]any {
		t.Helper()
		return callMCP(t, server, mustJSON(t, map[string]any{
			"jsonrpc": "2.0", "id": id, "method": "tools/call",
			"params": map[string]any{"name": "call", "arguments": map[string]any{
				"session_id": sessionID, "action": action, "input": map[string]any{},
			}},
		}))
	}
	if result := genericActionResult(t, call(1, "agent/guide")); len(result) == 0 {
		t.Fatal("successful authenticated action returned empty result")
	}
	if result := genericStructured(t, call(2, "agent/guide")); result["is_error"] == true {
		t.Fatalf("repeated authenticated action failed: %#v", result)
	}
	if result := genericStructured(t, call(3, "not-an-action")); result["is_error"] != true {
		t.Fatalf("structured action error was not returned: %#v", result)
	}
	usage, err := server.Service.Durability.ReadTokenUsage(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if usage.RequestCount != 3 || usage.InputTokens <= 0 || usage.OutputTokens <= 0 || usage.TotalTokens != usage.InputTokens+usage.OutputTokens {
		t.Fatalf("usage=%#v", usage)
	}

	unknown := "SA-UNKNOWN"
	if _, err := server.Service.Durability.Local.Query(context.Background(), `SELECT COUNT(*) FROM local_token_usage WHERE session_id=?`, unknown); err != nil {
		t.Fatal(err)
	}
	_ = callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 4, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{
			"session_id": unknown, "action": "agent/guide", "input": map[string]any{},
		}},
	}))
	rows, err := server.Service.Durability.Local.Query(context.Background(), `SELECT COUNT(*) FROM local_token_usage_events WHERE session_id=?`, unknown)
	if err != nil || len(rows.Rows) != 1 || rows.Rows[0][0] != int64(0) {
		t.Fatalf("unknown session usage rows=%#v err=%v", rows.Rows, err)
	}
}
