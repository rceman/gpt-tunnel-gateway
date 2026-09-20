package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

func TestTSK650ProjectSessionDiscoveryIsScopedAndTokenNeverProjects(t *testing.T) {
	server := newSessionTestServer(t)
	token := adr84PlannerToken(t, server)
	started := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "session_start", "arguments": map[string]any{"token": token}},
	})))
	sessionID := started["session"].(string)
	store := mcpSQLiteSessionStore(t, server.Service)
	other, err := store.Create(durableSession.CreateInput{
		ProjectID: "other", ProjectCode: "OTH", Role: durableSession.RolePlanner, SessionType: durableSession.SessionTypeChatGPT,
	})
	if err != nil {
		t.Fatal(err)
	}
	listed := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{"session": sessionID, "action": "session/list", "input": map[string]any{}}},
	})))
	if listed["is_error"] == true {
		t.Fatalf("session/list failed: %#v", listed)
	}
	result, ok := listed["result"].(map[string]any)
	if !ok {
		t.Fatalf("session/list result=%#v", listed)
	}
	sessions, ok := result["sessions"].([]any)
	if !ok {
		t.Fatalf("session/list sessions=%#v", result)
	}
	for _, raw := range sessions {
		item := raw.(map[string]any)
		if item["session_id"] == other.ID || item["project_id"] == "other" {
			t.Fatalf("cross-project session was exposed: %#v", result)
		}
	}
	encoded, err := json.Marshal(map[string]any{"start": started, "list": listed})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), token) {
		t.Fatal("bootstrap token was projected in session results")
	}
	guide := canonicalAgentGuide()
	guideBytes, err := json.Marshal(guide)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(guideBytes), token) {
		t.Fatal("bootstrap token was projected in agent guide")
	}
}
