package mcp

import (
	"context"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
)

func TestTSK577ADRStatusSchemasAndRuntimeUseDescriptorPolicy(t *testing.T) {
	server := newSessionTestServer(t)
	server.AuthorityContext = authority.WithPlanner(context.Background())
	entries := server.genericActionRegistry(server.tools())
	create := entries["adr/create"].InputSchema
	update := entries["adr/update"].InputSchema
	if got := schemaEnum(schemaProperties(create)["status"]); len(got) != 1 || got[0] != "proposed" {
		t.Fatalf("ADR create status enum=%#v, want [proposed]", got)
	}
	if got := schemaEnum(schemaProperties(update)["status"]); len(got) != 4 || got[0] != "proposed" || got[1] != "accepted" || got[2] != "superseded" || got[3] != "archived" {
		t.Fatalf("ADR update status enum=%#v, want descriptor allowed statuses", got)
	}

	sessionID := genericSession(t, server.Service, "example")
	call := func(id int, action string, input map[string]any) map[string]any {
		t.Helper()
		return genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
			"jsonrpc": "2.0", "id": id, "method": "tools/call",
			"params": map[string]any{"name": "call", "arguments": map[string]any{
				"session_id": sessionID, "action": action, "input": input,
			}},
		})))
	}
	created := call(1, "adr/create", map[string]any{"title": "Policy ADR", "context": "context", "decision": "decision", "consequences": "consequences"})
	createdResult := created["result"].(map[string]any)
	adrID := createdResult["adr"].(string)
	if createdResult["revision"] != float64(1) {
		t.Fatalf("create result=%#v", createdResult)
	}
	accepted := call(2, "adr/create", map[string]any{"title": "Rejected ADR", "context": "context", "decision": "decision", "consequences": "consequences", "status": "accepted"})
	if accepted["is_error"] != true {
		t.Fatalf("ADR create accepted disallowed status: %#v", accepted)
	}
	updated := call(3, "adr/update", map[string]any{"adr": adrID, "status": "accepted", "reason": "accepted by owner"})
	if updated["is_error"] == true {
		t.Fatalf("ADR update status transition failed: %#v", updated)
	}
	if result := updated["result"].(map[string]any); result["revision"] != float64(2) {
		t.Fatalf("update result=%#v", result)
	}
}

func schemaEnum(value any) []string {
	property, _ := value.(map[string]any)
	values, _ := property["enum"].([]any)
	result := make([]string, 0, len(values))
	for _, value := range values {
		if text, ok := value.(string); ok {
			result = append(result, text)
		}
	}
	return result
}
