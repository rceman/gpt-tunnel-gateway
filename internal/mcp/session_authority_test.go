package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

func TestGenericCallValidatesCompiledInputBeforeActionExecution(t *testing.T) {
	server := newSessionTestServer(t)
	server.AuthorityContext = authority.WithPlanner(context.Background())
	sessionID := genericSessionWithRole(t, server.Service, "example", durableSession.RolePlanner)
	invalid := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{
			"session": sessionID, "action": "operation/read", "input": map[string]any{"operation_id": "EXM-OPR1"},
		}},
	})))
	message := invalid["result"].(map[string]any)["error"].(map[string]any)["message"].(string)
	if invalid["is_error"] != true || !strings.Contains(message, `schema with path="operation/read"`) {
		t.Fatalf("invalid contract input was not rejected before handler execution: %#v", invalid)
	}
}

func TestGenericCallAuthenticatesEachActionAgainstDurableSession(t *testing.T) {
	server := newSessionTestServer(t)
	server.AuthorityContext = authority.WithPlanner(context.Background())
	for _, role := range []string{durableSession.RolePlanner, durableSession.RoleLead, durableSession.RoleAdvisor, durableSession.RoleWorker} {
		sessionID := genericSessionWithRole(t, server.Service, "example", role)
		result := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
			"jsonrpc": "2.0", "id": role, "method": "tools/call",
			"params": map[string]any{"name": "call", "arguments": map[string]any{
				"session": sessionID, "action": "session/info", "input": map[string]any{},
			}},
		})))
		if result["is_error"] == true {
			t.Fatalf("durable %s Session failed action authentication: %#v", role, result)
		}
	}
}
