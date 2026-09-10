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
