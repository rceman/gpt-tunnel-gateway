package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSessionStartValidatesProjectRoleAndTypeBeforeCreation(t *testing.T) {
	server := newSessionTestServer(t)
	for _, args := range []map[string]any{
		{"action": "start", "project_id": "missing", "role": "delivery", "session_type": "chatgpt"},
		{"action": "start", "project_id": "example", "role": "operator", "session_type": "chatgpt"},
		{"action": "start", "project_id": "example", "role": "delivery", "session_type": "unknown"},
	} {
		response := sessionCall(t, server, args)
		result, ok := response["result"].(map[string]any)
		if !ok || result["isError"] != true {
			t.Fatalf("invalid start was accepted: %#v", response)
		}
	}
	entries, err := os.ReadDir(filepath.Join(server.Service.Config.StateDir, "sessions"))
	if err == nil && len(entries) != 0 {
		t.Fatalf("invalid starts created sessions: %d", len(entries))
	}
}

func TestGenericCallRequiresSessionAndInheritsProject(t *testing.T) {
	server := newSessionTestServer(t)
	var seen json.RawMessage
	if err := server.RegisterGenericAction(GenericAction{
		Path:         "test/project",
		Description:  "Project-bound test action",
		InputSchema:  obj(map[string]any{"project_id": str("Inherited project")}, "project_id"),
		OutputSchema: closedOutput(map[string]any{"project_id": outputString()}, "project_id"),
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			seen = append(seen[:0], raw...)
			var input struct {
				ProjectID string `json:"project_id"`
			}
			if err := decode(raw, &input); err != nil {
				return nil, err
			}
			return map[string]any{"project_id": input.ProjectID}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	missing := callMCP(t, server, mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "call", "arguments": map[string]any{"action": "test/project", "input": map[string]any{}}}}))
	if result, ok := missing["result"].(map[string]any); ok || result != nil {
		t.Fatalf("missing session did not fail schema validation: %#v", missing)
	}
	start := genericStructured(t, sessionCall(t, server, map[string]any{"action": "start", "project_id": "example", "role": "delivery", "session_type": "chatgpt"}))
	sessionID := start["session"].(map[string]any)["session_id"].(string)
	okCall := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{"name": "call", "arguments": map[string]any{"session_id": sessionID, "action": "test/project", "input": map[string]any{}}}})))
	if okCall["is_error"] != false || string(seen) != `{"project_id":"example"}` {
		t.Fatalf("project was not inherited: result=%#v input=%s", okCall, seen)
	}
	mismatch := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 3, "method": "tools/call", "params": map[string]any{"name": "call", "arguments": map[string]any{"session_id": sessionID, "action": "test/project", "input": map[string]any{"project_id": "other"}}}})))
	if mismatch["is_error"] != true || !strings.Contains(mismatch["result"].(map[string]any)["error"].(string), "does not match session project") {
		t.Fatalf("project mismatch was not rejected: %#v", mismatch)
	}
	if string(seen) != `{"project_id":"example"}` {
		t.Fatalf("mismatch reached handler: %s", seen)
	}
}

func TestSystemPingRemainsStandaloneAndGenericBatchContinues(t *testing.T) {
	server := newSessionTestServer(t)
	legacy := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "system_ping", "arguments": map[string]any{}}})))
	if legacy["service"] != "gpt-tunnel-gatewayd" {
		t.Fatalf("standalone system_ping failed: %#v", legacy)
	}
	root := callMCP(t, server, mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{"name": "schema", "arguments": map[string]any{"path": "system"}}}))
	result, ok := root["result"].(map[string]any)
	if !ok || result["isError"] != true {
		t.Fatalf("system domain unexpectedly exposed: %#v", root)
	}
}
