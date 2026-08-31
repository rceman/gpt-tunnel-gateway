package mcp

import (
	"strings"
	"testing"

	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

func TestADR84PublicMCPBoundaryIsSixToolsAndBounded(t *testing.T) {
	server := newSessionTestServer(t)
	response := callMCPRaw(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/list",
	}))
	result := response["result"].(map[string]any)
	tools := result["tools"].([]any)
	want := []string{"call", "guide", "projects", "schema", "session_start", "status"}
	got := make([]string, 0, len(tools))
	for _, raw := range tools {
		tool := raw.(map[string]any)
		got = append(got, tool["name"].(string))
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("public MCP tools=%v want=%v", got, want)
	}
	for _, forbidden := range []string{"batch", "session_update", "agent_status", "callback_list"} {
		for _, name := range got {
			if name == forbidden {
				t.Fatalf("forbidden top-level MCP tool %q is present", forbidden)
			}
		}
	}
	for _, raw := range tools {
		tool := raw.(map[string]any)
		if tool["inputSchema"].(map[string]any)["additionalProperties"] != false {
			t.Fatalf("%s input schema is not closed: %#v", tool["name"], tool["inputSchema"])
		}
	}
}

func TestADR84PublicBootstrapAndBoundCallUseExactEnvelopes(t *testing.T) {
	server := newSessionTestServer(t)
	call := func(id int, name string, arguments map[string]any) map[string]any {
		t.Helper()
		response := callMCPRaw(t, server, mustJSON(t, map[string]any{
			"jsonrpc": "2.0", "id": id, "method": "tools/call",
			"params": map[string]any{
				"name": name, "arguments": arguments,
				"_meta": map[string]any{"adr": "GTW-ADR81", "references": []string{"GTW-ADR84", "GTW-ADR83"}, "why": "ADR84 public MCP boundary regression."},
			},
		}))
		result, ok := response["result"].(map[string]any)
		if !ok || result["isError"] == true {
			t.Fatalf("%s failed: %#v", name, response)
		}
		structured, ok := result["structuredContent"].(map[string]any)
		if !ok {
			t.Fatalf("%s omitted structured content: %#v", name, response)
		}
		return structured
	}
	status := call(1, "status", map[string]any{})
	if len(status) != 3 || status["ready"] == nil || status["gateways"] == nil || status["captured_at"] == nil {
		t.Fatalf("status is not ADR84-shaped: %#v", status)
	}
	guide := call(2, "guide", map[string]any{})
	roles, ok := guide["roles"].([]any)
	if !ok || len(roles) != 2 {
		t.Fatalf("guide omitted roles: %#v", guide)
	}
	if roles[0].(map[string]any)["key"] != "planner" || roles[0].(map[string]any)["ref_required"] != false || roles[1].(map[string]any)["key"] != "agent" || roles[1].(map[string]any)["ref_required"] != true || roles[1].(map[string]any)["ref_semantics"] != "airelay_session_key" {
		t.Fatalf("guide role contract=%#v", roles)
	}
	projects := call(3, "projects", map[string]any{"gateway": "test_gateway"})
	if projects["gateway"].(map[string]any)["key"] != "test_gateway" {
		t.Fatalf("projects gateway=%#v", projects["gateway"])
	}
	listedProjects := projects["projects"].([]any)
	if len(listedProjects) != 1 || listedProjects[0].(map[string]any)["key"] != "EXM" || listedProjects[0].(map[string]any)["name"] != "example" {
		t.Fatalf("projects did not expose compact identity: %#v", projects)
	}
	started := call(4, "session_start", map[string]any{
		"gateway": "test_gateway", "project": "EXM", "role": durableSession.RolePlanner, "ref": "planner-e2e",
	})
	if len(started) != 6 || started["session"] == nil || started["gateway"] == nil || started["project"] == nil || started["rules"] == nil {
		t.Fatalf("session_start is not ADR84-shaped: %#v", started)
	}
	startedProject := started["project"].(map[string]any)
	if startedProject["key"] != "EXM" || startedProject["name"] != "example" {
		t.Fatalf("session_start did not expose compact project identity: %#v", startedProject)
	}
	sessionID := started["session"].(string)
	record, err := durableSession.NewStore(server.Service.Config.StateDir).Get(sessionID)
	if err != nil || record.Status != durableSession.StatusActive || record.ProjectID != "example" || record.Role != durableSession.RolePlanner {
		t.Fatalf("session_start did not create the bound session: %#v err=%v", record, err)
	}
	schema := call(5, "schema", map[string]any{"session": sessionID, "path": "agent"})
	if schema["kind"] != "domain" || schema["path"] != "agent" {
		t.Fatalf("schema response=%#v", schema)
	}
	callResult := call(6, "call", map[string]any{
		"session": sessionID, "action": "session/info", "input": map[string]any{},
	})
	if callResult["ok"] != true {
		t.Fatalf("bound call envelope=%#v", callResult)
	}
	if _, ok := callResult["metrics"].(map[string]any); !ok {
		t.Fatalf("bound call omitted metrics: %#v", callResult)
	}
}

func TestADR84ApplicationDomainsRemainBehindSchemaAndCall(t *testing.T) {
	server := newSessionTestServer(t)
	entries := server.genericActionRegistry(server.tools())
	for _, path := range []string{"agent/list", "callback/list", "system/await"} {
		if _, ok := entries[path]; !ok {
			t.Fatalf("application action %q is not registered behind schema/call", path)
		}
	}
	for _, raw := range server.publicTools() {
		if strings.Contains(raw.Name, "/") {
			t.Fatalf("application action %q was promoted to the top-level MCP surface", raw.Name)
		}
	}
	call := func(path string) map[string]any {
		t.Helper()
		response := callMCPRaw(t, server, mustJSON(t, map[string]any{
			"jsonrpc": "2.0", "id": path, "method": "tools/call",
			"params": map[string]any{"name": "session_start", "arguments": map[string]any{
				"gateway": server.Service.Config.GatewayID, "project": "EXM", "role": durableSession.RolePlanner,
			}},
		}))
		return response["result"].(map[string]any)["structuredContent"].(map[string]any)
	}
	startedResult := call("session_start")
	sessionID := startedResult["session"].(string)
	for _, domain := range []string{"agent", "callback", "system"} {
		response := callMCPRaw(t, server, mustJSON(t, map[string]any{
			"jsonrpc": "2.0", "id": domain, "method": "tools/call",
			"params": map[string]any{"name": "schema", "arguments": map[string]any{
				"session": sessionID, "path": domain,
			}},
		}))
		structured := response["result"].(map[string]any)["structuredContent"].(map[string]any)
		if structured["kind"] != "domain" || structured["path"] != domain {
			t.Fatalf("schema(%q)=%#v", domain, structured)
		}
	}
}

func TestADR84PublicSchemaRequiresBoundSessionAndRejectsBatch(t *testing.T) {
	server := newSessionTestServer(t)
	missing := callMCPRaw(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "schema", "arguments": map[string]any{}},
	}))
	if missing["error"] == nil {
		t.Fatalf("schema without session was accepted: %#v", missing)
	}
	batch := callMCPRaw(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "batch", "arguments": map[string]any{}},
	}))
	if batch["error"] == nil {
		t.Fatalf("retired batch tool remained callable: %#v", batch)
	}
}
