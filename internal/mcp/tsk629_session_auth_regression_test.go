package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

func TestTSK629PublicCallHardCutsRuntimeSessionFallback(t *testing.T) {
	t.Setenv("GPT_TUNNEL_SESSION", "")
	fixture := newTSK571HTTPFixture(t, []string{durableSession.RoleWorker}, true, true)

	runtimeResult := fixture.call(t, fixture.runtime, "task/read", map[string]any{"key": fixture.task.ID})
	if runtimeResult["ok"] != false {
		t.Fatalf("runtime-key session was accepted: %#v", runtimeResult)
	}
	message := tsk571ErrorMessage(t, runtimeResult)
	if !strings.Contains(message, "session") && !strings.Contains(message, "Session") {
		t.Fatalf("runtime-key rejection did not identify durable Session authentication: %q", message)
	}

	durableResult := fixture.call(t, fixture.sessions[durableSession.RoleWorker], "task/read", map[string]any{"key": fixture.task.ID})
	if durableResult["ok"] != true {
		t.Fatalf("durable Worker Session was rejected: %#v", durableResult)
	}

	unknownResult := fixture.call(t, "HOM_EXM_W_zzzzz", "task/read", map[string]any{"key": fixture.task.ID})
	if unknownResult["ok"] != false {
		t.Fatalf("unknown durable Session was accepted: %#v", unknownResult)
	}
}

func TestTSK629AllWorkflowRolesAuthenticateThroughDurableSession(t *testing.T) {
	server := newSessionTestServer(t)
	calls := map[string]int{}
	for _, role := range []string{durableSession.RolePlanner, durableSession.RoleLead, durableSession.RoleAdvisor, durableSession.RoleWorker} {
		role := role
		if err := server.RegisterGenericAction(GenericAction{
			Path:        "test/" + role,
			Description: "TSK629 durable Session authentication test action.",
			InputSchema: obj(map[string]any{"value": str("value")}, "value"),
			OutputSchema: closedOutput(map[string]any{
				"role": outputString(),
			}, "role"),
			AuthorityRole: role,
			Execute: func(context.Context, json.RawMessage) (any, error) {
				calls[role]++
				return map[string]any{"role": role}, nil
			},
		}); err != nil {
			t.Fatal(err)
		}
	}
	for _, role := range []string{durableSession.RolePlanner, durableSession.RoleLead, durableSession.RoleAdvisor, durableSession.RoleWorker} {
		sessionID := genericSessionWithRole(t, server.Service, "example", role)
		result := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
			"jsonrpc": "2.0", "id": role, "method": "tools/call",
			"params": map[string]any{"name": "call", "arguments": map[string]any{
				"session": sessionID, "action": "test/" + role, "input": map[string]any{"value": "ok"},
			}},
		})))
		if result["is_error"] == true || calls[role] != 1 {
			t.Fatalf("durable %s Session authentication failed: result=%#v calls=%d", role, result, calls[role])
		}
	}
}

func TestTSK629RoleActionMatrixRemainsFailClosed(t *testing.T) {
	t.Setenv("GPT_TUNNEL_SESSION", "")
	fixture := newTSK571HTTPFixture(t, []string{durableSession.RoleLead, durableSession.RoleAdvisor, durableSession.RoleWorker}, true, true)
	installTSK563Airelay(t, fixture)
	cases := []struct {
		role   string
		action string
		input  map[string]any
	}{
		{durableSession.RoleLead, "agent/status", map[string]any{"agent": fixture.agentID}},
		{durableSession.RoleLead, "agent/prompt", map[string]any{"agent": fixture.agentID, "message": "forbidden"}},
		{durableSession.RoleLead, "agent/interrupt", map[string]any{"agent": fixture.agentID}},
		{durableSession.RoleAdvisor, "agent/status", map[string]any{"agent": fixture.agentID}},
		{durableSession.RoleAdvisor, "agent/prompt", map[string]any{"agent": fixture.agentID, "message": "forbidden"}},
		{durableSession.RoleAdvisor, "agent/interrupt", map[string]any{"agent": fixture.agentID}},
		{durableSession.RoleWorker, "agent/status", map[string]any{"agent": fixture.agentID}},
		{durableSession.RoleWorker, "agent/prompt", map[string]any{"agent": fixture.agentID, "message": "forbidden"}},
		{durableSession.RoleWorker, "agent/interrupt", map[string]any{"agent": fixture.agentID}},
		{durableSession.RoleWorker, "task/dispatch", map[string]any{"key": fixture.task.ID}},
		{durableSession.RoleLead, "task/submit-code", map[string]any{}},
	}
	for _, tc := range cases {
		result := fixture.call(t, fixture.sessions[tc.role], tc.action, tc.input)
		if result["ok"] != false {
			t.Fatalf("forbidden %s/%s action was accepted: %#v", tc.role, tc.action, result)
		}
	}
	leadAwait := fixture.call(t, fixture.sessions[durableSession.RoleLead], "agent/await", map[string]any{"agent": fixture.agentID, "seconds": 1})
	if leadAwait["ok"] != true {
		t.Fatalf("Lead agent/await was rejected: %#v", leadAwait)
	}
	fixture.server.Service.Config.Debug.Enabled = true
	entries := fixture.server.genericActionRegistry(fixture.server.tools())
	for _, path := range []string{"agent/prompt", "agent/interrupt", "agent/status", "agent/tail", "debug/tail"} {
		if entries[path].AuthorityRole != durableSession.RolePlanner {
			t.Fatalf("%s authority was flattened: %#v", path, entries[path])
		}
	}
	if entries["agent/await"].AuthorityRole != actionRolePlannerOrLead {
		t.Fatalf("agent/await authority=%q", entries["agent/await"].AuthorityRole)
	}
}

func TestTSK629PublicSchemasHideRuntimeSelectors(t *testing.T) {
	s, _ := mcpServiceWithSQLite(t, config.Config{Debug: config.DebugConfig{Enabled: true}, StateDir: t.TempDir()})
	server := &Server{Service: s}
	entries := server.genericActionRegistry(server.tools())
	for _, path := range []string{"agent/prompt", "agent/interrupt", "agent/status", "agent/tail", "agent/await", "debug/prompt", "debug/tail"} {
		entry, ok := entries[path]
		if !ok {
			t.Fatalf("missing public action %q", path)
		}
		encoded, err := json.Marshal(map[string]any{"input": entry.InputSchema, "output": entry.OutputSchema})
		if err != nil {
			t.Fatal(err)
		}
		text := string(encoded)
		for _, forbidden := range []string{"airelay_session", "runtime_ref", "runtime-key", "runtime identity", "session_key"} {
			if strings.Contains(text, forbidden) {
				t.Fatalf("%s public schema leaks %q: %s", path, forbidden, text)
			}
		}
	}
	generic, err := json.Marshal(map[string]any{
		"call": genericCallInputSchema(), "schema": genericSchemaPublicInputSchema(),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"runtime identity", "runtime-key", "runtime_ref", "airelay_session"} {
		if strings.Contains(string(generic), forbidden) {
			t.Fatalf("generic transport schema leaks %q: %s", forbidden, generic)
		}
	}
}
