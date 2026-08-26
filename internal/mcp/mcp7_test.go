package mcp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
)

func TestBootstrapFirstPublicSurfaceIsExact(t *testing.T) {
	server := newSessionTestServer(t)
	response := callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/list",
	}))
	result := response["result"].(map[string]any)
	tools := result["tools"].([]any)
	want := []string{"batch", "call", "schema", "session_start", "status"}
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
	runtime := structured["runtime_identity"].(map[string]any)
	for _, field := range []string{"gateway_ready", "tunnel_ready", "version_match", "exact_source_match"} {
		if _, ok := runtime[field]; !ok {
			t.Fatalf("bootstrap runtime omitted %q: %#v", field, runtime)
		}
	}
	projects := structured["registered_projects"].(map[string]any)["projects"].([]any)
	if len(projects) != 1 || projects[0].(map[string]any)["project_id"] != "example" {
		t.Fatalf("bootstrap project discovery=%#v", projects)
	}
	if structured["status"] == "" || structured["recommended_next_action"] == "" {
		t.Fatalf("status omitted control-plane guidance: %#v", structured)
	}
}

func TestDeliverySessionStartFailsBeforeCreatingOrphan(t *testing.T) {
	server := newSessionTestServer(t)
	server.AuthorityContext = authority.WithDelivery(context.Background())
	sessionsDir := filepath.Join(server.Service.Config.StateDir, "sessions")
	countSessions := func() int {
		entries, err := os.ReadDir(sessionsDir)
		if errors.Is(err, os.ErrNotExist) {
			return 0
		}
		if err != nil {
			t.Fatal(err)
		}
		return len(entries)
	}
	before := countSessions()
	response := callMCPRaw(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "session_start", "arguments": map[string]any{"project_id": "example"}},
	}))
	if response["error"] == nil {
		result, _ := response["result"].(map[string]any)
		if result["isError"] != true {
			t.Fatalf("Delivery session_start succeeded: %#v", response)
		}
	}
	after := countSessions()
	if after != before {
		t.Fatalf("failed Delivery session_start created an orphan: before=%d after=%d", before, after)
	}
}

func TestProjectBoundSessionFlowUsesCodeAndSessionDerivedProject(t *testing.T) {
	server := newSessionTestServer(t)
	started := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "session_start", "arguments": map[string]any{"project_id": "example"}},
	})))
	sessionID := started["session"].(string)
	if !strings.HasPrefix(sessionID, "SP-EXM-") {
		t.Fatalf("session ID did not embed Planner role and project code: %q", sessionID)
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
		"params": map[string]any{"name": "session_start", "arguments": map[string]any{"project_id": "NOPE"}},
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
		"params": map[string]any{"name": "session_start", "arguments": map[string]any{"project_id": "example"}},
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
