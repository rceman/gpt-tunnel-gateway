package mcp

import (
	"context"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
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
	created := call(1, "adr/create", map[string]any{"title": "Policy ADR", "summary": "Bounded policy summary", "context": "context", "decision": "decision", "consequences": "consequences", "status": "proposed"})
	createdResult := created["result"].(map[string]any)
	adrID := createdResult["key"].(string)
	if createdResult["revision"] != float64(1) {
		t.Fatalf("create result=%#v", createdResult)
	}
	omittedStatus := call(2, "adr/create", map[string]any{"title": "Defaulted ADR", "summary": "Bounded defaulted summary", "context": "context", "decision": "decision", "consequences": "consequences"})
	if omittedStatus["is_error"] == true {
		t.Fatalf("ADR create omitted status=%#v, want proposed", omittedStatus)
	}
	omittedResult := omittedStatus["result"].(map[string]any)
	omittedID := omittedResult["key"].(string)
	omittedRead := call(3, "adr/read", map[string]any{"key": omittedID})
	if omittedRead["is_error"] == true || omittedRead["result"].(map[string]any)["status"] != "proposed" {
		t.Fatalf("ADR create omitted status=%#v, want proposed", omittedRead)
	}
	accepted := call(4, "adr/create", map[string]any{"title": "Rejected ADR", "summary": "Bounded rejected summary", "context": "context", "decision": "decision", "consequences": "consequences", "status": "accepted"})
	if accepted["is_error"] != true {
		t.Fatalf("ADR create accepted disallowed status: %#v", accepted)
	}
	updated := call(5, "adr/update", map[string]any{"key": adrID, "status": "accepted", "reason": "accepted by owner"})
	if updated["is_error"] == true {
		t.Fatalf("ADR update status transition failed: %#v", updated)
	}
	if result := updated["result"].(map[string]any); result["revision"] != float64(1) {
		t.Fatalf("status-only update result=%#v, want unchanged content revision", result)
	}
	readCurrent := call(6, "adr/read", map[string]any{"key": adrID})
	if result := readCurrent["result"].(map[string]any); result["status"] != "accepted" || result["revision"] != float64(1) {
		t.Fatalf("current ADR revision=%#v", result)
	}
	readOne := call(7, "adr/read", map[string]any{"key": adrID, "revision": 1})
	if result := readOne["result"].(map[string]any); result["status"] != "proposed" {
		t.Fatalf("historical ADR revision=%#v", result)
	}
	history := call(8, "adr/history", map[string]any{"key": adrID})
	if history["is_error"] == true {
		t.Fatalf("ADR history failed: %#v", history)
	}
	historyResult := history["result"].(map[string]any)
	if historyResult["key"] != adrID {
		t.Fatalf("ADR history key=%#v", historyResult)
	}
	items := historyResult["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("ADR history items=%#v, want immutable content plus lifecycle event", items)
	}
	if event := items[1].(map[string]any); event["mutation_kind"] != "status" || event["revision"] != float64(1) {
		t.Fatalf("ADR lifecycle event=%#v", event)
	}
	if got := sqlitestore.SharedLifecycleStatusValues("task", true); len(got) != 1 || got[0] != "planned" {
		t.Fatalf("Task create status policy=%#v, want [planned]", got)
	}
	if got := sqlitestore.SharedLifecycleStatusValues("task", false); len(got) != 4 || got[2] != "done" {
		t.Fatalf("Task allowed status policy=%#v, want done", got)
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
