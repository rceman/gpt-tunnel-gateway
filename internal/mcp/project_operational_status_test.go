package mcp

import (
	"testing"
)

func TestProjectStatusIsSessionBoundCompactOperationalRead(t *testing.T) {
	server := newSessionTestServer(t)
	entry, ok := server.genericActionRegistry(server.tools())["project/status"]
	if !ok || !entry.SessionBound || !entry.SessionRequired {
		t.Fatalf("project/status contract=%#v", entry)
	}
	if schemaContainsPropertyForTest(entry.InputSchema, "project_id") {
		t.Fatalf("project/status exposes caller project authority: %#v", entry.InputSchema)
	}
	started := genericStructured(t, sessionCall(t, server, map[string]any{
		"action": "start", "project_id": "example", "role": "planner", "session_type": "chatgpt",
	}))
	sessionID := started["session"].(map[string]any)["session_id"].(string)
	status := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{
			"session": sessionID, "action": "project/status", "input": map[string]any{},
		}},
	})))
	payload, ok := status["result"].(map[string]any)
	if !ok {
		t.Fatalf("project/status result=%#v", status)
	}
	if len(payload) != 3 || payload["configuration_revision"] != float64(1) || payload["procedure_count"] != float64(0) || payload["bound_hook_count"] != float64(0) {
		t.Fatalf("project/status was not the compact Session-bound configuration projection: %#v", status)
	}
}
