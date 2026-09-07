package mcp

import (
	"strings"
	"testing"

	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

func TestPublicSchemaFiltersActionsByImmutableSessionRole(t *testing.T) {
	server := newSessionTestServer(t)
	plannerID := genericSessionWithRole(t, server.Service, "example", durableSession.RolePlanner)
	agentID := genericSessionWithRole(t, server.Service, "example", durableSession.RoleAgent)

	schema := func(sessionID, path string) map[string]any {
		t.Helper()
		return genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
			"jsonrpc": "2.0", "id": 1, "method": "tools/call",
			"params": map[string]any{"name": "schema", "arguments": map[string]any{"session": sessionID, "path": path}},
		})))
	}
	actions := func(sessionID, domain string) map[string]bool {
		t.Helper()
		result := schema(sessionID, domain)
		values, ok := result["actions"].([]any)
		if !ok {
			t.Fatalf("schema(%q) actions=%#v", domain, result)
		}
		got := make(map[string]bool, len(values))
		for _, value := range values {
			path, ok := value.(map[string]any)["path"].(string)
			if !ok {
				t.Fatalf("schema(%q) action=%#v", domain, value)
			}
			got[path] = true
		}
		return got
	}
	for _, role := range []struct {
		name      string
		sessionID string
	}{
		{name: durableSession.RolePlanner, sessionID: plannerID},
		{name: durableSession.RoleAgent, sessionID: agentID},
	} {
		sessionActions := actions(role.sessionID, "session")
		for _, path := range []string{"session/info", "session/list", "session/end"} {
			if !sessionActions[path] {
				t.Fatalf("%s schema omitted %s: %#v", role.name, path, sessionActions)
			}
		}
		if sessionActions["session/update"] || sessionActions["session/bind"] {
			t.Fatalf("%s schema exposed rebinding action: %#v", role.name, sessionActions)
		}
	}

	plannerRuntime := actions(plannerID, "runtime")
	if !plannerRuntime["runtime/logs"] || !plannerRuntime["runtime/restart"] {
		t.Fatalf("planner runtime schema=%#v", plannerRuntime)
	}
	agentRuntime := actions(agentID, "runtime")
	if !agentRuntime["runtime/logs"] || agentRuntime["runtime/restart"] {
		t.Fatalf("agent runtime schema=%#v", agentRuntime)
	}
	plannerTrain := actions(plannerID, "train")
	if !plannerTrain["train/review-resolve"] {
		t.Fatalf("planner train schema omitted planner action: %#v", plannerTrain)
	}

	root := schema(plannerID, "")
	rootDomains, ok := root["domains"].([]any)
	if !ok {
		t.Fatalf("planner root schema=%#v", root)
	}
	for _, raw := range rootDomains {
		if raw.(map[string]any)["key"] == "" {
			t.Fatalf("planner root contained empty domain: %#v", root)
		}
	}
	unauthorized := callMCPRaw(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "schema", "arguments": map[string]any{"session": agentID, "path": "train/review-resolve"}},
	}))
	result, ok := unauthorized["result"].(map[string]any)
	if !ok || result["isError"] != true {
		t.Fatalf("unauthorized exact schema action was exposed: %#v", unauthorized)
	}
	removed := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{"session": plannerID, "action": "session/update", "input": map[string]any{"label": "not-allowed"}}},
	})))
	if removed["is_error"] != true {
		t.Fatalf("removed session/update remained callable: %#v", removed)
	}
}

func TestProjectBoundSessionFlowUsesCodeAndSessionDerivedProject(t *testing.T) {
	server := newSessionTestServer(t)
	started := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "session_start", "arguments": map[string]any{"gateway": "test_gateway", "project": "EXM", "role": durableSession.RolePlanner}},
	})))
	sessionID := started["session"].(string)
	if !strings.HasPrefix(sessionID, "SP-") {
		t.Fatalf("session ID did not embed Planner role: %q", sessionID)
	}
	if record, err := mcpSQLiteSessionStore(t, server.Service).Get(sessionID); err != nil || record.ProjectID != "example" {
		t.Fatalf("session_start was not project-bound: %#v err=%v", record, err)
	}
	status := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{"session": sessionID, "action": "project/status", "input": map[string]any{}}},
	})))
	if status["is_error"] == true {
		t.Fatalf("project/status failed through bound session: %#v", status)
	}
	bad := callMCPRaw(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "tools/call",
		"params": map[string]any{"name": "session_start", "arguments": map[string]any{"gateway": "test_gateway", "project": "EXM", "role": "invalid"}},
	}))
	if bad["error"] == nil && bad["result"].(map[string]any)["isError"] != true {
		t.Fatalf("invalid role was accepted: %#v", bad)
	}
	legacyProject := callMCPRaw(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 5, "method": "tools/call",
		"params": map[string]any{"name": "session_start", "arguments": map[string]any{"gateway": "test_gateway", "project": "example", "role": durableSession.RolePlanner}},
	}))
	if legacyProject["error"] == nil && legacyProject["result"].(map[string]any)["isError"] != true {
		t.Fatalf("internal project ID was accepted as a public alias: %#v", legacyProject)
	}
	badEnvelope := callMCPRaw(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 4, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{"session": sessionID, "project_id": "other", "action": "project/status", "input": map[string]any{}}},
	}))
	if badEnvelope["error"] == nil {
		t.Fatalf("call accepted project_id as alternate authority: %#v", badEnvelope)
	}
}

func TestCallRequiresSessionEnvelopeWithoutProjectAuthority(t *testing.T) {
	server := newSessionTestServer(t)
	response := callMCPRaw(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{"action": "project/status", "input": map[string]any{}, "project_id": "example"}},
	}))
	if response["error"] == nil {
		t.Fatalf("call accepted a project-bearing/unbound envelope: %#v", response)
	}
}

func TestCorruptProjectCodeInSessionIDFailsClosed(t *testing.T) {
	server := newSessionTestServer(t)
	id := genericSessionWithRole(t, server.Service, "example", durableSession.RolePlanner)
	corrupt := strings.Replace(id, "EXM", "BAD", 1)
	response := callMCPRaw(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{"session": corrupt, "action": "project/status", "input": map[string]any{}}},
	}))
	structured := genericStructured(t, response)
	if structured["is_error"] != true {
		t.Fatalf("corrupt project code session was accepted: %#v", response)
	}
}
