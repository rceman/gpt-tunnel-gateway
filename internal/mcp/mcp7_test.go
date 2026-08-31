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

func TestPublicSessionStartRejectsDeliveryRole(t *testing.T) {
	server := newSessionTestServer(t)
	roleSchema := sessionStartPublicInputSchema()["properties"].(map[string]any)["role"].(map[string]any)
	roles, ok := roleSchema["enum"].([]any)
	if !ok || len(roles) != 2 || roles[0] != durableSession.RolePlanner || roles[1] != durableSession.RoleAgent {
		t.Fatalf("public session_start role schema=%#v", roleSchema)
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
	response := callMCPRaw(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "session_start", "arguments": map[string]any{"gateway": "test_gateway", "project": "EXM", "role": durableSession.RoleDelivery, "ref": "delivery"}},
	}))
	if response["error"] == nil {
		result, _ := response["result"].(map[string]any)
		if result["isError"] != true {
			t.Fatalf("Delivery session_start was accepted: %#v", response)
		}
	}
	after := countSessions()
	if after != before {
		t.Fatalf("rejected Delivery session_start created a session: before=%d after=%d", before, after)
	}
}

func TestPublicSessionStartAfterTerminationIsFreshAndBoundCallWorks(t *testing.T) {
	server := newSessionTestServer(t)
	start := func(role, label string) string {
		result := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
			"jsonrpc": "2.0", "id": 1, "method": "tools/call",
			"params": map[string]any{"name": "session_start", "arguments": map[string]any{"gateway": "test_gateway", "project": "EXM", "role": role, "ref": label}},
		})))
		project := result["project"].(map[string]any)
		if result["role"] != role || result["session"] == "" || project["key"] != "EXM" || project["name"] != "example" {
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
	bound, err := durableSession.NewStore(server.Service.Config.StateDir).Get(b)
	if err != nil || bound.ProjectID != "example" {
		t.Fatalf("fresh session did not bind at creation: %#v err=%v", bound, err)
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
	ended, err := durableSession.NewStore(server.Service.Config.StateDir).Get(a)
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

func TestPublicSchemaFiltersActionsByImmutableSessionRole(t *testing.T) {
	server := newSessionTestServer(t)
	plannerID := genericSessionWithRole(t, server.Service, "example", durableSession.RolePlanner)
	deliveryID := genericSessionWithRole(t, server.Service, "example", durableSession.RoleDelivery)

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
		{name: durableSession.RoleDelivery, sessionID: deliveryID},
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
	if !plannerRuntime["runtime/logs"] || plannerRuntime["runtime/restart"] {
		t.Fatalf("planner runtime schema=%#v", plannerRuntime)
	}
	deliveryRuntime := actions(deliveryID, "runtime")
	if !deliveryRuntime["runtime/logs"] || !deliveryRuntime["runtime/restart"] {
		t.Fatalf("delivery runtime schema=%#v", deliveryRuntime)
	}
	plannerTrain := actions(plannerID, "train")
	if !plannerTrain["train/review-resolve"] {
		t.Fatalf("planner train schema omitted planner action: %#v", plannerTrain)
	}
	deliveryTrain := actions(deliveryID, "train")
	if deliveryTrain["train/review-resolve"] {
		t.Fatalf("delivery train schema exposed planner action: %#v", deliveryTrain)
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
		"params": map[string]any{"name": "schema", "arguments": map[string]any{"session": deliveryID, "path": "train/review-resolve"}},
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
	if record, err := durableSession.NewStore(server.Service.Config.StateDir).Get(sessionID); err != nil || record.ProjectID != "example" {
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
