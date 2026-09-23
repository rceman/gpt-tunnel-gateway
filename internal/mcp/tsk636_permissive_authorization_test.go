package mcp

import (
	"context"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/session"
)

func TestTSK636AuthenticatedWorkflowRolesShareSchemaAndActionAuthorization(t *testing.T) {
	fixture := newTSK571HTTPFixture(t, []string{session.RolePlanner, session.RoleLead, session.RoleAdvisor, session.RoleWorker}, true, true)
	installTSK563Airelay(t, fixture)
	fixture.server.Service.Config.Debug.Enabled = true
	fixture.server.AuthorityContext = authority.WithPlanner(context.Background())

	wantDomains := map[string][]string{
		"task":  {"task/read"},
		"adr":   {"adr/list"},
		"agent": {"agent/status"},
		"debug": {"debug/status", "debug/prompt", "debug/tail", "debug/await", "debug/activate"},
	}
	for role, sessionID := range fixture.sessions {
		for domain, wantPaths := range wantDomains {
			value := frozenResult(t, fixture.client.request(t, "tools/call", map[string]any{
				"name":      "schema",
				"arguments": map[string]any{"session": sessionID, "path": domain},
			}))
			paths := map[string]bool{}
			for _, raw := range value["actions"].([]any) {
				paths[raw.(map[string]any)["path"].(string)] = true
			}
			for _, path := range wantPaths {
				if !paths[path] {
					t.Fatalf("%s schema for %s omitted %q: %v", role, domain, path, paths)
				}
			}
		}

		for _, action := range []struct {
			path  string
			input map[string]any
		}{
			{path: "task/read", input: map[string]any{"key": fixture.task.ID}},
			{path: "adr/list", input: map[string]any{}},
			{path: "agent/status", input: map[string]any{"key": fixture.agentID}},
		} {
			result := fixture.call(t, sessionID, action.path, action.input)
			if result["ok"] != true {
				t.Fatalf("authenticated %s %s was rejected: %#v", role, action.path, result)
			}
		}
	}
}
