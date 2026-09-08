package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

func TestTSK545AgentGuideIsClosedBoundedAndPlannerOnly(t *testing.T) {
	server := &Server{Service: service.New(config.Config{GatewayID: "home_pc", StateDir: t.TempDir()})}
	entry, ok := server.genericActionRegistry(server.tools())["agent/guide"]
	if !ok {
		t.Fatal("agent/guide is not registered")
	}
	if entry.AuthorityRole != durableSession.RolePlanner || !entry.LocalReadOnly || !entry.Annotations.ReadOnlyHint || !entry.Annotations.IdempotentHint {
		t.Fatalf("agent/guide metadata=%#v", entry)
	}
	if !entry.SessionBound || !entry.SessionRequired {
		t.Fatalf("agent/guide session binding=%#v", entry)
	}
	if entry.InputSchema["additionalProperties"] != false || len(schemaProperties(entry.InputSchema)) != 0 || len(stringList(entry.InputSchema["required"])) != 0 {
		t.Fatalf("agent/guide input schema=%#v", entry.InputSchema)
	}
	if entry.OutputSchema["additionalProperties"] != false {
		t.Fatalf("agent/guide output is not closed: %#v", entry.OutputSchema)
	}
	properties := schemaProperties(entry.OutputSchema)
	want := []string{"architecture", "authority", "prompt_interrupt", "status_await", "tail"}
	for _, field := range want {
		value, ok := properties[field].(map[string]any)
		if !ok || value["type"] != "string" || value["maxLength"] != 768 {
			t.Fatalf("agent/guide output field %q=%#v", field, properties[field])
		}
	}
	if got := stringList(entry.OutputSchema["required"]); len(got) != len(want) {
		t.Fatalf("agent/guide required=%v", got)
	}
	value, err := entry.Execute(authority.WithPlanner(context.Background()), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	guide, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("agent/guide result=%#v", value)
	}
	for _, text := range []string{
		"multiple durable Planner sessions",
		"exactly one attached enabled coding Agent",
		"zero is an error",
		"more than one requires explicit SA-*",
		"SA-GTW-AB12",
		"optional logical Agent selector",
		"Train and watcher",
		"project Airelay fallback",
		"repo guide file",
	} {
		found := false
		for _, raw := range guide {
			if strings.Contains(raw.(string), text) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("agent/guide omitted %q: %#v", text, guide)
		}
	}
}

func TestTSK545AgentGuideRejectsNonPlannerSession(t *testing.T) {
	server := newSessionTestServer(t)
	store := mcpSQLiteSessionStore(t, server.Service)
	session, err := store.Create(durableSession.CreateInput{
		ProjectID: "example", ProjectCode: "EXM", Role: durableSession.RoleAgent,
		SessionType: durableSession.SessionTypeChatGPT,
	})
	if err != nil {
		t.Fatal(err)
	}
	response := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 545, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{
			"session": session.ID, "action": "agent/guide", "input": map[string]any{},
		}},
	})))
	if response["is_error"] != true {
		t.Fatalf("non-Planner guide call succeeded: %#v", response)
	}
	errorValue, _ := response["result"].(map[string]any)["error"].(map[string]any)
	if !strings.Contains(errorValue["message"].(string), "planner") {
		t.Fatalf("non-Planner guide error=%#v", response)
	}
}
