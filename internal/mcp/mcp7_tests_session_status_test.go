package mcp

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
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

func TestPublicSessionStartGatewaySelectionContract(t *testing.T) {
	server := newSessionTestServer(t)
	server.Service.Config.MaxListItems = 2000

	call := func(arguments map[string]any) map[string]any {
		t.Helper()
		return callMCPRaw(t, server, mustJSON(t, map[string]any{
			"jsonrpc": "2.0", "id": 1, "method": "tools/call",
			"params": map[string]any{"name": "session_start", "arguments": arguments},
		}))
	}
	assertStarted := func(response map[string]any, wantGateway, wantLabel string) {
		t.Helper()
		result, ok := response["result"].(map[string]any)
		if !ok || result["isError"] == true {
			t.Fatalf("session_start failed: %#v", response)
		}
		structured, ok := result["structuredContent"].(map[string]any)
		if !ok {
			t.Fatalf("session_start omitted structured content: %#v", response)
		}
		gateway, ok := structured["gateway"].(map[string]any)
		if !ok || gateway["key"] != wantGateway {
			t.Fatalf("session_start gateway=%#v want=%q", structured["gateway"], wantGateway)
		}
		if structured["label"] != wantLabel {
			t.Fatalf("session_start label=%#v want=%q", structured["label"], wantLabel)
		}
	}

	assertStarted(call(map[string]any{
		"project": "EXM", "role": durableSession.RolePlanner, "label": "default",
	}), "HOM", "default")
	assertStarted(call(map[string]any{
		"gateway": "HOM", "project": "EXM", "role": durableSession.RolePlanner, "label": "explicit",
	}), "HOM", "explicit")

	unknown := call(map[string]any{
		"gateway": "BAD", "project": "EXM", "role": durableSession.RolePlanner,
	})
	unknownResult, ok := unknown["result"].(map[string]any)
	if !ok || unknownResult["isError"] != true {
		t.Fatalf("unknown explicit gateway was not rejected: %#v", unknown)
	}
	unknownContent, ok := unknownResult["content"].([]any)
	if !ok || len(unknownContent) != 1 {
		t.Fatalf("unknown explicit gateway omitted error content: %#v", unknown)
	}
	unknownText, ok := unknownContent[0].(map[string]any)["text"].(string)
	if !ok || !strings.Contains(unknownText, "unknown gateway") || !strings.Contains(unknownText, "BAD") {
		t.Fatalf("unknown explicit gateway error=%q", unknownText)
	}

	server.gatewayInventoryFn = func() []string { return []string{"OTH", "HOM"} }
	ambiguous := call(map[string]any{
		"project": "EXM", "role": durableSession.RolePlanner,
	})
	ambiguousResult, ok := ambiguous["result"].(map[string]any)
	if !ok || ambiguousResult["isError"] != true {
		t.Fatalf("omitted gateway was accepted for ambiguous inventory: %#v", ambiguous)
	}
	ambiguousText := ambiguousResult["content"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(ambiguousText, "gateway selection required") || !strings.Contains(ambiguousText, "HOM, OTH") {
		t.Fatalf("ambiguous gateway error was not deterministic/bounded: %q", ambiguousText)
	}
}

func TestPublicSessionStartRejectsDeliveryRole(t *testing.T) {
	server := newSessionTestServer(t)
	roleSchema := sessionStartPublicInputSchema()["properties"].(map[string]any)["role"].(map[string]any)
	if roleSchema["type"] != "string" || roleSchema["minLength"] != 1 || roleSchema["maxLength"] != 16 {
		t.Fatalf("public session_start role schema=%#v", roleSchema)
	}
	enum, ok := roleSchema["enum"].([]any)
	if !ok || len(enum) != len(durableSession.WorkflowRoles()) {
		t.Fatalf("public session_start role schema is not registry-backed: %#v", roleSchema)
	}
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
	for _, role := range []string{"delivery", "watcher"} {
		response := callMCPRaw(t, server, mustJSON(t, map[string]any{
			"jsonrpc": "2.0", "id": 1, "method": "tools/call",
			"params": map[string]any{"name": "session_start", "arguments": map[string]any{"gateway": "HOM", "project": "EXM", "role": role}},
		}))
		if response["error"] == nil {
			result, _ := response["result"].(map[string]any)
			if result["isError"] != true {
				t.Fatalf("%s session_start was accepted: %#v", role, response)
			}
		}
		after := countSessions()
		if after != before {
			t.Fatalf("rejected %s session_start created a session: before=%d after=%d", role, before, after)
		}
	}
}

func TestPublicSessionStartAfterTerminationIsFreshAndBoundCallWorks(t *testing.T) {
	server := newSessionTestServer(t)
	start := func(role, label string) string {
		result := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
			"jsonrpc": "2.0", "id": 1, "method": "tools/call",
			"params": map[string]any{"name": "session_start", "arguments": map[string]any{"gateway": "HOM", "project": "EXM", "role": role, "label": label}},
		})))
		project := result["project"].(map[string]any)
		if result["role"] != role || result["session"] == "" || project["key"] != "EXM" || project["name"] != "example" || result["label"] != label {
			t.Fatalf("session_start(%q) result=%#v", role, result)
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
	if err != nil || bound.ProjectID != "example" || bound.Label == nil || *bound.Label != "fresh" {
		t.Fatalf("fresh session did not bind label at creation: %#v err=%v", bound, err)
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
