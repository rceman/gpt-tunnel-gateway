package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestTSK564TaskUpdateSchemaAndHandlerUseKey(t *testing.T) {
	server := newSessionTestServer(t)
	server.AuthorityContext = authority.WithPlanner(context.Background())
	entries := server.genericActionRegistry(server.tools())
	entry, ok := entries["task/update"]
	if !ok {
		t.Fatal("canonical task/update action missing")
	}
	if entry.InputSchema["additionalProperties"] != false {
		t.Fatalf("task/update schema is not closed: %#v", entry.InputSchema)
	}
	properties := schemaProperties(entry.InputSchema)
	if _, ok := properties["key"]; !ok {
		t.Fatal("task/update schema does not advertise key")
	}
	if _, ok := properties["task_id"]; ok {
		t.Fatal("task/update schema advertises legacy task_id")
	}

	sessionID := genericSession(t, server.Service, "example")
	create := func(input map[string]any) map[string]any {
		t.Helper()
		return genericActionResult(t, callMCP(t, server, mustJSON(t, map[string]any{
			"jsonrpc": "2.0", "id": 1, "method": "tools/call",
			"params": map[string]any{"name": "call", "arguments": map[string]any{
				"session_id": sessionID, "action": "task/create", "input": input,
			}},
		})))
	}
	created := create(map[string]any{
		"title": "Parity task", "summary": "A bounded parity summary.", "objective": "Exercise task update parity.", "adr_relation": model.TaskADRNoRequired,
	})
	key, ok := created["key"].(string)
	if !ok || !strings.HasPrefix(key, "EXM-TSK") {
		t.Fatalf("created task key=%#v", created)
	}

	updated := genericActionResult(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{
			"session_id": sessionID, "action": "task/update", "input": map[string]any{
				"key": key, "title": "Updated parity task", "reason": "verify key parity",
			},
		}},
	})))
	if updated["key"] != key || updated["revision"] != float64(2) {
		t.Fatalf("task/update result=%#v", updated)
	}

	legacy := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{
			"session_id": sessionID, "action": "task/update", "input": map[string]any{
				"task_id": key, "title": "Legacy field", "reason": "must reject legacy field",
			},
		}},
	})))
	if legacy["is_error"] != true {
		t.Fatal("task/update accepted legacy task_id")
	}
}
