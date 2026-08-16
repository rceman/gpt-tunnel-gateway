package mcp

import (
	"strings"
	"testing"
)

func TestBootstrapFirstPublicSurfaceIsExact(t *testing.T) {
	server := newSessionTestServer(t)
	response := callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/list",
	}))
	result := response["result"].(map[string]any)
	tools := result["tools"].([]any)
	want := []string{"batch", "bootstrap", "call", "project_onboard", "schema", "session_start"}
	got := make([]string, 0, len(tools))
	for _, raw := range tools {
		got = append(got, raw.(map[string]any)["name"].(string))
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("public MCP tool surface=%v want=%v", got, want)
	}
	for _, retired := range []string{"project_list", "session_update"} {
		response := callMCPRaw(t, server, mustJSON(t, map[string]any{
			"jsonrpc": "2.0", "id": 2, "method": "tools/call",
			"params": map[string]any{"name": retired, "arguments": map[string]any{}},
		}))
		if response["error"] == nil {
			t.Fatalf("retired public tool %q remained callable: %#v", retired, response)
		}
	}
}

func TestBootstrapReturnsCompactRuntimeProjectsAndEffectiveRules(t *testing.T) {
	server := newSessionTestServer(t)
	structured := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "bootstrap", "arguments": map[string]any{}},
	})))
	runtime := structured["runtime"].(map[string]any)
	for _, field := range []string{"gateway_ready", "tunnel_ready", "version_match", "exact_source_match"} {
		if _, ok := runtime[field]; !ok {
			t.Fatalf("bootstrap runtime omitted %q: %#v", field, runtime)
		}
	}
	projects := structured["projects"].([]any)
	if len(projects) != 1 || projects[0].(map[string]any)["project_code"] != "EXM" || projects[0].(map[string]any)["project_id"] != "example" {
		t.Fatalf("bootstrap project discovery=%#v", projects)
	}
	rules := structured["rules"].(map[string]any)
	for _, field := range []string{"name", "revision", "content", "digest", "guidance"} {
		if _, ok := rules[field]; !ok {
			t.Fatalf("bootstrap rules omitted effective field %q: %#v", field, rules)
		}
	}
	if _, ok := structured["project_status"]; ok {
		t.Fatal("bootstrap leaked project operational status")
	}
}

func TestProjectBoundSessionFlowUsesCodeAndSessionDerivedProject(t *testing.T) {
	server := newSessionTestServer(t)
	started := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "session_start", "arguments": map[string]any{"project": "EXM", "role": "delivery"}},
	})))
	sessionID := started["session"].(string)
	if !strings.HasPrefix(sessionID, "SD-EXM-") {
		t.Fatalf("session ID did not embed role and project code: %q", sessionID)
	}
	project := started["project"].(map[string]any)
	if project["project_id"] != "example" || project["project_code"] != "EXM" {
		t.Fatalf("session project projection=%#v", project)
	}
	status := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{"session": sessionID, "action": "rules/read", "input": map[string]any{}}},
	})))
	if status["is_error"] != false {
		t.Fatalf("project/status failed through bound session: %#v", status)
	}
	bad := callMCPRaw(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "tools/call",
		"params": map[string]any{"name": "session_start", "arguments": map[string]any{"project": "NOPE", "role": "delivery"}},
	}))
	if bad["error"] == nil && bad["result"].(map[string]any)["isError"] != true {
		t.Fatalf("unknown project code was accepted: %#v", bad)
	}
	badEnvelope := callMCPRaw(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 4, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{"session": sessionID, "project_id": "other", "action": "project/read", "input": map[string]any{}}},
	}))
	if badEnvelope["error"] == nil {
		t.Fatalf("call accepted project_id as alternate authority: %#v", badEnvelope)
	}
}

func TestCallAndBatchRequireSessionEnvelopeWithoutProjectAuthority(t *testing.T) {
	server := newSessionTestServer(t)
	for _, name := range []string{"call", "batch"} {
		response := callMCPRaw(t, server, mustJSON(t, map[string]any{
			"jsonrpc": "2.0", "id": 1, "method": "tools/call",
			"params": map[string]any{"name": name, "arguments": map[string]any{"action": "project/read", "input": map[string]any{}, "project_id": "example"}},
		}))
		if response["error"] == nil {
			t.Fatalf("%s accepted a project-bearing/unbound envelope: %#v", name, response)
		}
	}
}

func TestCorruptProjectCodeInSessionIDFailsClosed(t *testing.T) {
	server := newSessionTestServer(t)
	started := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "session_start", "arguments": map[string]any{"project": "EXM", "role": "delivery"}},
	})))
	id := started["session"].(string)
	corrupt := strings.Replace(id, "EXM", "BAD", 1)
	response := callMCPRaw(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{"session": corrupt, "action": "project/read", "input": map[string]any{}}},
	}))
	if response["error"] == nil && response["result"].(map[string]any)["isError"] != true {
		t.Fatalf("corrupt project code session was accepted: %#v", response)
	}
}
