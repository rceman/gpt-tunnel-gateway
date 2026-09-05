package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

func registerAuthorityTestAction(t *testing.T, server *Server, path, role string, policy bool, calls *int) {
	t.Helper()
	err := server.RegisterGenericAction(GenericAction{
		Path:                   path,
		Description:            "authority test action",
		InputSchema:            obj(map[string]any{"value": str("value")}, "value"),
		OutputSchema:           closedOutput(map[string]any{"ok": outputBoolean()}, "ok"),
		AuthorityRole:          role,
		RequiresWorkflowPolicy: policy,
		Execute: func(context.Context, json.RawMessage) (any, error) {
			*calls++
			return map[string]any{"ok": true}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestGenericCallUsesDurableSessionRoleBeforeInputDecode(t *testing.T) {
	server := newSessionTestServer(t)
	server.AuthorityContext = authority.WithPlanner(context.Background())
	var calls int
	registerAuthorityTestAction(t, server, "test/planner", durableSession.RolePlanner, false, &calls)
	registerAuthorityTestAction(t, server, "test/agent", durableSession.RoleAgent, false, &calls)
	started := genericStructured(t, sessionCall(t, server, map[string]any{"action": "start", "project_id": "example", "role": durableSession.RolePlanner, "session_type": durableSession.SessionTypeChatGPT}))
	sessionID := started["session"].(map[string]any)["session_id"].(string)
	ok := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "call", "arguments": map[string]any{"session_id": sessionID, "action": "test/planner", "input": map[string]any{"value": "ok"}}}})))
	if ok["is_error"] != false || calls != 1 {
		t.Fatalf("durable planner session was not accepted: result=%#v calls=%d", ok, calls)
	}
	wrong := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{"name": "call", "arguments": map[string]any{"session_id": sessionID, "action": "test/agent", "input": map[string]any{"unknown": true}}}})))
	message := wrong["result"].(map[string]any)["error"].(map[string]any)["message"].(string)
	if wrong["is_error"] != true || !strings.Contains(message, `required "agent"`) || calls != 1 {
		t.Fatalf("wrong role was not rejected before decode/handler: result=%#v calls=%d", wrong, calls)
	}
}

func TestGenericCallChecksEveryActionAgainstDurableSession(t *testing.T) {
	server := newSessionTestServer(t)
	server.AuthorityContext = authority.WithPlanner(context.Background())
	var plannerCalls, deliveryCalls int
	registerAuthorityTestAction(t, server, "test/planner", durableSession.RolePlanner, false, &plannerCalls)
	registerAuthorityTestAction(t, server, "test/agent", durableSession.RoleAgent, false, &deliveryCalls)
	started := genericStructured(t, sessionCall(t, server, map[string]any{"action": "start", "project_id": "example", "role": durableSession.RolePlanner, "session_type": durableSession.SessionTypeChatGPT}))
	sessionID := started["session"].(map[string]any)["session_id"].(string)
	allowed := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "call", "arguments": map[string]any{"session_id": sessionID, "action": "test/planner", "input": map[string]any{"value": "ok"}}}})))
	denied := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{"name": "call", "arguments": map[string]any{"session_id": sessionID, "action": "test/agent", "input": map[string]any{"value": "denied"}}}})))
	if allowed["is_error"] != false || denied["is_error"] != true || plannerCalls != 1 || deliveryCalls != 0 {
		t.Fatalf("call authority mismatch: allowed=%#v denied=%#v planner=%d delivery=%d", allowed, denied, plannerCalls, deliveryCalls)
	}
}
