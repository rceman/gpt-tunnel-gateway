package mcp

import (
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func TestTSK433JournalActionSurfaceIsCanonicalAndSessionBound(t *testing.T) {
	server := &Server{Service: service.New(config.Config{GatewayID: "journal-test", StateDir: t.TempDir()})}
	entries := server.genericActionRegistry(server.tools())
	want := []string{"journal/contract", "journal/add", "journal/list", "journal/read"}
	for _, path := range want {
		entry, ok := entries[path]
		if !ok {
			t.Fatalf("canonical journal action %q is not registered", path)
		}
		if entry.InputSchema == nil || entry.OutputSchema == nil {
			t.Fatalf("journal action %q has an incomplete contract", path)
		}
		if !entry.SessionBound {
			t.Fatalf("journal action %q is not session-bound", path)
		}
	}
	for _, retired := range []string{"journal/create", "journal/update", "journal/archive", "journal/delete", "journal/kind", "operator_record", "operator_history", "operator_checkpoint"} {
		if _, ok := entries[retired]; ok {
			t.Fatalf("retired journal action %q is registered", retired)
		}
	}
	add := entries["journal/add"].InputSchema
	properties, _ := add["properties"].(map[string]any)
	if _, ok := properties["stream"]; !ok {
		t.Fatal("journal/add has no stream property")
	}
	if _, ok := properties["data"]; !ok {
		t.Fatal("journal/add has no data property")
	}
	if _, ok := properties["project_id"]; ok {
		t.Fatal("journal/add exposes caller-owned project_id; project is session-bound")
	}
	stream, _ := properties["stream"].(map[string]any)
	enum, _ := stream["enum"].([]any)
	if len(enum) != 3 {
		t.Fatalf("journal/add stream enum is not the closed three-stream registry: %#v", enum)
	}
	for _, rejected := range []string{"lead-decisions", "test-perf-findings"} {
		for _, value := range enum {
			if value == rejected {
				t.Fatalf("journal/add accepts unapproved stream %q", rejected)
			}
		}
	}
	read := entries["journal/read"].OutputSchema
	readProps, _ := read["properties"].(map[string]any)
	for _, field := range []string{"key", "stream", "data", "actor", "role", "session", "sequence", "created_at"} {
		if _, ok := readProps[field]; !ok {
			t.Fatalf("journal/read output lacks %q", field)
		}
	}
	if _, ok := readProps["kind"]; ok {
		t.Fatal("journal/read exposes the retired flat kind coordinate")
	}
	list := entries["journal/list"].OutputSchema
	listProps, _ := list["properties"].(map[string]any)
	if _, ok := listProps["items"]; !ok {
		t.Fatal("journal/list output lacks items")
	}
	// _pagination is transport-private keyset metadata detached by the
	// transport layer; the public schema intentionally omits it.
}

func TestTSK433JournalActionsRoundTripThroughPublicTransport(t *testing.T) {
	server := newSessionTestServer(t)
	started := genericStructured(t, sessionCall(t, server, map[string]any{"action": "start", "project_id": "example", "role": "planner", "session_type": "chatgpt"}))
	session := started["session"].(map[string]any)["session_id"].(string)

	contract := genericActionResult(t, publicCallbackEnvelope(t, server, session, "journal/contract", map[string]any{"stream": "planner-notes"}))
	if contract["stream"] != "planner-notes" || contract["purpose"] == "" || contract["writer_authority"] == nil {
		t.Fatalf("journal/contract=%#v", contract)
	}

	added := genericActionResult(t, publicCallbackEnvelope(t, server, session, "journal/add", map[string]any{
		"stream": "planner-notes",
		"data": map[string]any{
			"summary": "e2e note", "decisions": []any{"d"}, "commitments": []any{}, "facts": []any{},
			"assumptions": []any{}, "blockers": []any{}, "unresolved": []any{}, "next_actions": []any{}, "references": []any{},
		},
	}))
	key, ok := added["key"].(string)
	if !ok || key != "EXM-JRN1" {
		t.Fatalf("journal/add=%#v, want key EXM-JRN1", added)
	}

	read := genericActionResult(t, publicCallbackEnvelope(t, server, session, "journal/read", map[string]any{"key": key}))
	if read["key"] != key || read["stream"] != "planner-notes" || read["role"] != "planner" || read["sequence"] != float64(1) {
		t.Fatalf("journal/read=%#v", read)
	}
	data, _ := read["data"].(map[string]any)
	if data["summary"] != "e2e note" {
		t.Fatalf("journal/read data=%#v", data)
	}

	list := genericActionResult(t, publicCallbackEnvelope(t, server, session, "journal/list", map[string]any{"stream": "planner-notes"}))
	items, _ := list["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("journal/list items=%#v", list)
	}

	rejected := genericStructured(t, publicCallbackEnvelope(t, server, session, "journal/add", map[string]any{
		"stream": "planner-notes",
		"data":   map[string]any{"summary": "incomplete"},
	}))
	if rejected["is_error"] != true {
		t.Fatalf("invalid journal/add was accepted: %#v", rejected)
	}
	errValue, _ := rejected["result"].(map[string]any)["error"].(map[string]any)
	if errValue["code"] != "JOURNAL_CONTRACT_VIOLATION" {
		t.Fatalf("journal/add violation error=%#v", errValue)
	}
	message, _ := errValue["message"].(string)
	if !strings.Contains(message, "data.decisions") {
		t.Fatalf("journal/add violation message lacks failing paths: %#v", errValue)
	}
}
