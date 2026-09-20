package mcp

import (
	"fmt"
	"strings"
	"testing"

	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

func TestBootstrapFirstPublicSurfaceIsExact(t *testing.T) {
	server := newSessionTestServer(t)
	response := callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/list",
	}))
	result := response["result"].(map[string]any)
	tools := result["tools"].([]any)
	want := []string{"call", "guide", "projects", "schema", "session_start", "status"}
	got := make([]string, 0, len(tools))
	for _, raw := range tools {
		got = append(got, raw.(map[string]any)["name"].(string))
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("public MCP tool surface=%v want=%v", got, want)
	}
	for _, retired := range []string{"project_list", "session_update", "bootstrap", "project_onboard"} {
		response := callMCPRaw(t, server, mustJSON(t, map[string]any{
			"jsonrpc": "2.0", "id": 2, "method": "tools/call",
			"params": map[string]any{"name": retired, "arguments": map[string]any{}},
		}))
		if response["error"] == nil {
			t.Fatalf("retired public tool %q remained callable: %#v", retired, response)
		}
	}
}

func TestStatusReturnsCompactRuntimeProjects(t *testing.T) {
	server := newSessionTestServer(t)
	structured := genericStructured(t, callMCPRaw(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "status", "arguments": map[string]any{}},
	})))
	if _, ok := structured["ready"].(bool); !ok || len(structured["gateways"].([]any)) != 1 || structured["captured_at"] == "" {
		t.Fatalf("status omitted canonical readiness identity: %#v", structured)
	}
}

func TestPublicSessionStartTokenContract(t *testing.T) {
	server := newSessionTestServer(t)
	grant := adr84PlannerToken(t, server)
	response := callMCPRaw(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "session_start", "arguments": map[string]any{"token": grant}},
	}))
	result := response["result"].(map[string]any)
	structured := result["structuredContent"].(map[string]any)
	if result["isError"] == true || structured["role"] != durableSession.RolePlanner {
		t.Fatalf("token session_start failed: %#v", response)
	}
	if structured["gateway"].(map[string]any)["key"] != "HOM" {
		t.Fatalf("token did not resolve server Gateway: %#v", structured)
	}
	for _, arguments := range []map[string]any{
		{"token": grant, "gateway": "BAD"},
		{"gateway": "HOM", "project": "EXM", "role": durableSession.RolePlanner},
	} {
		invalid := callMCPRaw(t, server, mustJSON(t, map[string]any{
			"jsonrpc": "2.0", "id": 2, "method": "tools/call",
			"params": map[string]any{"name": "session_start", "arguments": arguments},
		}))
		if invalid["error"] == nil {
			if invalidResult, ok := invalid["result"].(map[string]any); !ok || invalidResult["isError"] != true {
				t.Fatalf("caller-selected session_start fields were accepted: %#v", invalid)
			}
		}
	}
}

func TestPublicSessionStartRejectsCallerSelectedRole(t *testing.T) {
	server := newSessionTestServer(t)
	properties := sessionStartPublicInputSchema()["properties"].(map[string]any)
	if len(properties) != 1 || properties["token"] == nil {
		t.Fatalf("public session_start schema=%#v", properties)
	}
	before, err := mcpSQLiteSessionStore(t, server.Service).List()
	if err != nil {
		t.Fatal(err)
	}
	for _, arguments := range []map[string]any{
		{"gateway": "HOM", "project": "EXM", "role": "delivery"},
		{"token": adr84PlannerToken(t, server), "role": "worker"},
	} {
		response := callMCPRaw(t, server, mustJSON(t, map[string]any{
			"jsonrpc": "2.0", "id": 1, "method": "tools/call",
			"params": map[string]any{"name": "session_start", "arguments": arguments},
		}))
		if response["error"] == nil {
			result, _ := response["result"].(map[string]any)
			if result["isError"] != true {
				t.Fatalf("caller-selected session_start was accepted: %#v", response)
			}
		}
	}
	after, err := mcpSQLiteSessionStore(t, server.Service).List()
	if err != nil || len(after) != len(before) {
		t.Fatalf("rejected session_start changed durable sessions: before=%d after=%d err=%v", len(before), len(after), err)
	}
}

func TestPublicSessionStartAfterTerminationIsFreshAndBoundCallWorks(t *testing.T) {
	server := newSessionTestServer(t)
	token := adr84PlannerToken(t, server)
	start := func(role, label string) string {
		if role != durableSession.RolePlanner {
			t.Fatalf("token bootstrap role=%q", role)
		}
		result := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
			"jsonrpc": "2.0", "id": 1, "method": "tools/call",
			"params": map[string]any{"name": "session_start", "arguments": map[string]any{"token": token}},
		})))
		project := result["project"].(map[string]any)
		if result["role"] != role || result["session"] == "" || project["key"] != "EXM" || project["name"] != "example" {
			t.Fatalf("session_start(%q,%q) result=%#v", role, label, result)
		}
		return result["session"].(string)
	}
	end := func(id string) {
		result := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
			"jsonrpc": "2.0", "id": 2, "method": "tools/call",
			"params": map[string]any{"name": "call", "arguments": map[string]any{"session": id, "action": "session/end", "input": map[string]any{}}},
		})))
		ended, _ := result["result"].(map[string]any)
		if ended["action"] != "end" {
			t.Fatalf("session/end result=%#v", result)
		}
	}
	a := start(durableSession.RolePlanner, "terminated")
	end(a)
	for i := 0; i < 2; i++ {
		terminated := start(durableSession.RolePlanner, fmt.Sprintf("terminated-%d", i))
		end(terminated)
	}
	b := start(durableSession.RolePlanner, "fresh")
	if b == a {
		t.Fatalf("fresh session reused terminated ID %q", b)
	}
	bound, err := mcpSQLiteSessionStore(t, server.Service).Get(b)
	if err != nil || bound.ProjectID != "example" || bound.Label != nil {
		t.Fatalf("fresh token session unexpectedly carried a caller label: %#v err=%v", bound, err)
	}
	status := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{"session": b, "action": "project/status", "input": map[string]any{}}},
	})))
	if status["is_error"] == true {
		t.Fatalf("bound read-only call failed: %#v", status)
	}
	c := start(durableSession.RolePlanner, "new")
	if c == a || c == b {
		t.Fatalf("new session reused ID: a=%q b=%q c=%q", a, b, c)
	}
	ended, err := mcpSQLiteSessionStore(t, server.Service).Get(a)
	if err != nil || ended.Status != durableSession.StatusEnded {
		t.Fatalf("terminated session changed: %#v err=%v", ended, err)
	}
	old := callMCPRaw(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 5, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{"session": a, "action": "project/status", "input": map[string]any{}}},
	}))
	oldStructured := genericStructured(t, old)
	if oldStructured["is_error"] != true {
		t.Fatalf("terminated session was accepted: %#v", old)
	}
}
