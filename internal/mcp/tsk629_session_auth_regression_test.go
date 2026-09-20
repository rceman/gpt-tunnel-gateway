package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/agentguide"
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

func TestTSK629RoleActionMatrixIsPermissiveAfterSessionAuthentication(t *testing.T) {
	t.Setenv("GPT_TUNNEL_SESSION", "")
	fixture := newTSK571HTTPFixture(t, []string{durableSession.RoleLead, durableSession.RoleAdvisor, durableSession.RoleWorker}, true, true)
	installTSK563Airelay(t, fixture)
	cases := []struct {
		role   string
		action string
		input  map[string]any
	}{
		{durableSession.RoleLead, "agent/status", map[string]any{"agent": fixture.agentID}},
		{durableSession.RoleLead, "agent/prompt", map[string]any{"agent": fixture.agentID, "message": "bounded"}},
		{durableSession.RoleLead, "agent/interrupt", map[string]any{"agent": fixture.agentID}},
		{durableSession.RoleAdvisor, "agent/status", map[string]any{"agent": fixture.agentID}},
		{durableSession.RoleAdvisor, "agent/prompt", map[string]any{"agent": fixture.agentID, "message": "bounded"}},
		{durableSession.RoleAdvisor, "agent/interrupt", map[string]any{"agent": fixture.agentID}},
		{durableSession.RoleWorker, "agent/status", map[string]any{"agent": fixture.agentID}},
		{durableSession.RoleWorker, "agent/prompt", map[string]any{"agent": fixture.agentID, "message": "bounded"}},
		{durableSession.RoleWorker, "agent/interrupt", map[string]any{"agent": fixture.agentID}},
	}
	for _, tc := range cases {
		result := fixture.call(t, fixture.sessions[tc.role], tc.action, tc.input)
		if result["ok"] != true {
			t.Fatalf("authenticated %s/%s action was rejected: %#v", tc.role, tc.action, result)
		}
	}
	leadAwait := fixture.call(t, fixture.sessions[durableSession.RoleLead], "agent/await", map[string]any{"agent": fixture.agentID, "seconds": 1})
	if leadAwait["ok"] != true {
		t.Fatalf("Lead agent/await was rejected: %#v", leadAwait)
	}
	fixture.server.Service.Config.Debug.Enabled = true
	entries := fixture.server.genericActionRegistry(fixture.server.tools())
	for _, path := range []string{"agent/prompt", "agent/interrupt", "agent/status", "agent/tail"} {
		if !actionAuthorityAllowsSessionRole(entries[path].AuthorityRole, durableSession.RolePlanner) || !actionAuthorityAllowsSessionRole(entries[path].AuthorityRole, durableSession.RoleLead) || !actionAuthorityAllowsSessionRole(entries[path].AuthorityRole, durableSession.RoleAdvisor) || !actionAuthorityAllowsSessionRole(entries[path].AuthorityRole, durableSession.RoleWorker) {
			t.Fatalf("%s does not expose the permissive authenticated contract: %#v", path, entries[path])
		}
	}
	for _, path := range []string{"debug/status", "debug/prompt", "debug/tail", "debug/await", "debug/activate"} {
		if entries[path].AuthorityRole != actionRolePlannerOrLead {
			t.Fatalf("%s authority metadata changed: %#v", path, entries[path])
		}
	}
}

func TestTSK629PublicSchemasHideRuntimeSelectors(t *testing.T) {
	s, _ := mcpServiceWithSQLite(t, config.Config{Debug: config.DebugConfig{Enabled: true}, StateDir: t.TempDir()})
	server := &Server{Service: s}
	entries := server.genericActionRegistry(server.tools())
	for _, path := range []string{"agent/prompt", "agent/interrupt", "agent/status", "agent/tail", "agent/await", "debug/prompt", "debug/tail", "debug/await"} {
		entry, ok := entries[path]
		if !ok {
			t.Fatalf("missing public action %q", path)
		}
		if strings.HasPrefix(path, "debug/") && path != "debug/status" {
			requiredAgentRef := false
			for _, required := range stringList(entry.InputSchema["required"]) {
				if required == "agent_ref" {
					requiredAgentRef = true
				}
			}
			if !requiredAgentRef {
				t.Fatalf("%s does not require an explicit debug agent_ref: %#v", path, entry.InputSchema)
			}
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

func TestTSK629PublicBoundaryInventoryHasNoInternalSelectors(t *testing.T) {
	s, _ := mcpServiceWithSQLite(t, config.Config{Debug: config.DebugConfig{Enabled: true}, StateDir: t.TempDir()})
	server := &Server{Service: s}
	forbiddenDescriptions := []string{
		"airelay_session", "session_key", "runtime_ref", "runtime-key", "exact airelay session key",
		"managed runtime", "agent runtime", "worker runtime", "managed-runtime",
	}
	forbiddenSelectorKeys := map[string]bool{"airelay_session": true, "airelay_session_key": true, "session_key": true, "session_ref": true, "runtime_ref": true}
	var audit func(string, any)
	audit = func(path string, value any) {
		switch current := value.(type) {
		case map[string]any:
			for key, child := range current {
				lowerKey := strings.ToLower(key)
				if forbiddenSelectorKeys[lowerKey] {
					t.Fatalf("public schema %s exposes internal selector %q", path, key)
				}
				if lowerKey == "description" {
					text, _ := child.(string)
					lowerText := strings.ToLower(text)
					for _, forbidden := range forbiddenDescriptions {
						if strings.Contains(lowerText, forbidden) {
							t.Fatalf("public schema %s description exposes %q: %q", path, forbidden, text)
						}
					}
				}
				audit(path+"."+key, child)
			}
		case []any:
			for _, child := range current {
				audit(path+"[]", child)
			}
		}
	}
	entries := server.genericActionRegistry(server.tools())
	for path, entry := range entries {
		lowerDescription := strings.ToLower(entry.Description)
		for _, forbidden := range forbiddenDescriptions {
			if strings.Contains(lowerDescription, forbidden) {
				t.Fatalf("public action %s description exposes %q: %q", path, forbidden, entry.Description)
			}
		}
		audit(path+".input", entry.InputSchema)
		audit(path+".output", entry.OutputSchema)
	}
	for name, tool := range server.publicTools() {
		for _, forbidden := range forbiddenDescriptions {
			if strings.Contains(strings.ToLower(tool.Description), forbidden) {
				t.Fatalf("public tool %s description exposes %q: %q", name, forbidden, tool.Description)
			}
		}
		audit(name+".input", tool.InputSchema)
		audit(name+".output", tool.OutputSchema)
	}
	for _, value := range []any{agentguide.Canonical(), taskGuideWorkflow, taskGuideReview, taskGuideVerification, taskGuideCompletion, taskGuideBoundaries} {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		text := strings.ToLower(string(encoded))
		for _, forbidden := range forbiddenDescriptions {
			if strings.Contains(text, forbidden) {
				t.Fatalf("canonical guide exposes %q: %s", forbidden, encoded)
			}
		}
	}
	sessionStartInput := sessionStartPublicInputSchema()["properties"].(map[string]any)
	if len(sessionStartInput) != 1 {
		t.Fatalf("session_start exposes caller-selected fields: %#v", sessionStartInput)
	}
	if _, ok := sessionStartInput["token"]; !ok {
		t.Fatal("session_start omits the bootstrap token")
	}
	for _, forbidden := range []string{"gateway", "project", "role", "agent", "label"} {
		if _, ok := sessionStartInput[forbidden]; ok {
			t.Fatalf("session_start exposes caller-selected %q", forbidden)
		}
	}
	outputProperties := sessionStartPublicOutputSchema()["properties"].(map[string]any)
	if _, ok := outputProperties["ref"]; ok {
		t.Fatal("session_start output retains the internal ref projection")
	}
	if _, ok := outputProperties["label"]; !ok {
		t.Fatal("session_start output omits bounded Session labels")
	}
	sessionInput := sessionInputSchema()
	encodedSessionInput, _ := json.Marshal(sessionInput)
	if strings.Contains(string(encodedSessionInput), "session_ref") {
		t.Fatalf("session action schema exposes session_ref: %s", encodedSessionInput)
	}
	for _, path := range []string{"agent/prompt", "agent/interrupt"} {
		entry, ok := entries[path]
		if !ok {
			t.Fatalf("missing public Agent action %q", path)
		}
		properties := entry.InputSchema["properties"].(map[string]any)
		if _, ok := properties["agent_id"]; ok {
			t.Fatalf("Agent action %s retains agent_id targeting", path)
		}
		if _, ok := properties["agent"]; !ok {
			t.Fatalf("Agent action %s omits logical Agent targeting", path)
		}
	}
}

func TestTSK629PublicSessionBootstrapIsTokenOnly(t *testing.T) {
	server := newSessionTestServer(t)
	tool := server.tools()["session_start"]
	if _, err := tool.Execute(server.AuthorityContext, mustJSON(t, map[string]any{"gateway": "HOM", "project": "EXM", "role": durableSession.RoleWorker, "ref": "runtime-worker"})); err == nil {
		t.Fatal("session_start accepted caller-selected binding fields")
	}
	if _, err := tool.Execute(server.AuthorityContext, mustJSON(t, map[string]any{})); err == nil {
		t.Fatal("session_start accepted without a bootstrap token")
	}
	token := adr84PlannerToken(t, server)
	value, err := tool.Execute(server.AuthorityContext, mustJSON(t, map[string]any{"token": token}))
	if err != nil {
		t.Fatal(err)
	}
	projected := normalizeObject(value)
	if _, ok := projected["token"]; ok {
		t.Fatalf("session_start echoed token: %#v", projected)
	}
	recordID := projected["session"].(string)
	record, err := mcpSQLiteSessionStore(t, server.Service).Get(recordID)
	if err != nil || record.Role != durableSession.RolePlanner || record.SessionRef != nil || record.Label != nil {
		t.Fatalf("server did not create the server-resolved Planner session: record=%#v err=%v", record, err)
	}
}
