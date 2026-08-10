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
	started := genericStructured(t, sessionCall(t, server, map[string]any{"action": "start", "project_id": "example", "role": durableSession.RolePlanner, "session_type": durableSession.SessionTypeChatGPT}))
	sessionID := started["session"].(map[string]any)["session_id"].(string)
	ok := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "call", "arguments": map[string]any{"session_id": sessionID, "action": "test/planner", "input": map[string]any{"value": "ok"}}}})))
	if ok["is_error"] != false || calls != 1 {
		t.Fatalf("durable planner session was not accepted: result=%#v calls=%d", ok, calls)
	}
	wrong := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{"name": "call", "arguments": map[string]any{"session_id": sessionID, "action": "planner/report_publish", "input": map[string]any{"unknown": true}}}})))
	message := wrong["result"].(map[string]any)["error"].(string)
	if wrong["is_error"] != true || !strings.Contains(message, `required "delivery"`) || calls != 1 {
		t.Fatalf("wrong role was not rejected before decode/handler: result=%#v calls=%d", wrong, calls)
	}
}

func TestGenericBatchChecksEveryActionAgainstDurableSession(t *testing.T) {
	server := newSessionTestServer(t)
	server.AuthorityContext = authority.WithPlanner(context.Background())
	var plannerCalls, deliveryCalls int
	registerAuthorityTestAction(t, server, "test/planner", durableSession.RolePlanner, false, &plannerCalls)
	registerAuthorityTestAction(t, server, "test/delivery", durableSession.RoleDelivery, false, &deliveryCalls)
	started := genericStructured(t, sessionCall(t, server, map[string]any{"action": "start", "project_id": "example", "role": durableSession.RolePlanner, "session_type": durableSession.SessionTypeChatGPT}))
	sessionID := started["session"].(map[string]any)["session_id"].(string)
	batch := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "batch", "arguments": map[string]any{"session_id": sessionID, "calls": []any{
		map[string]any{"action": "test/planner", "input": map[string]any{"value": "ok"}},
		map[string]any{"action": "test/delivery", "input": map[string]any{"value": "denied"}},
	}}}})))
	results := batch["results"].([]any)
	if len(results) != 2 || results[0].(map[string]any)["is_error"] != false || results[1].(map[string]any)["is_error"] != true || plannerCalls != 1 || deliveryCalls != 0 {
		t.Fatalf("batch authority mismatch: result=%#v planner=%d delivery=%d", batch, plannerCalls, deliveryCalls)
	}
}

func TestGenericAuthorityRequiresDurableWorkflowPolicy(t *testing.T) {
	server := newSessionTestServer(t)
	var calls int
	registerAuthorityTestAction(t, server, "test/policy", durableSession.RoleDelivery, true, &calls)
	started := genericStructured(t, sessionCall(t, server, map[string]any{"action": "start", "project_id": "example", "role": durableSession.RoleDelivery, "session_type": durableSession.SessionTypeChatGPT}))
	sessionID := started["session"].(map[string]any)["session_id"].(string)
	result := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "call", "arguments": map[string]any{"session_id": sessionID, "action": "test/policy", "input": map[string]any{"value": "ok"}}}})))
	message := result["result"].(map[string]any)["error"].(string)
	if result["is_error"] != true || !strings.Contains(message, "workflow policy") || calls != 0 {
		t.Fatalf("missing policy did not fail closed: result=%#v calls=%d", result, calls)
	}
}

func TestGenericRegistryAndTypedAuthorityUseSameContract(t *testing.T) {
	server := &Server{}
	legacy := map[string]Tool{}
	for _, name := range []string{"task_correction_create", "delivery_handoff_publish", "planner_report_publish", "project_workflow_policy_update"} {
		legacy[name] = Tool{
			Description:  name,
			InputSchema:  obj(map[string]any{}, ""),
			OutputSchema: closedOutput(map[string]any{"ok": outputBoolean()}, "ok"),
			Execute:      func(context.Context, json.RawMessage) (any, error) { return map[string]any{"ok": true}, nil },
		}
	}
	entries := server.genericActionRegistry(legacy)
	for _, name := range []string{"task_correction_create", "delivery_handoff_publish", "planner_report_publish", "project_workflow_policy_update"} {
		entry := entries[legacyActionPath(name)]
		contract := actionAuthorityContractFor(name)
		if entry.AuthorityRole != contract.Role || entry.RequiresWorkflowPolicy != contract.RequiresWorkflowPolicy {
			t.Fatalf("typed/generic authority drift for %s: entry=%#v contract=%#v", name, entry, contract)
		}
	}
}

func TestTypedAuthoritySensitiveActionRequiresDurableSession(t *testing.T) {
	server := newSessionTestServer(t)
	tools := server.tools()
	input := tools["planner_report_publish"].InputSchema
	if _, ok := input["properties"].(map[string]any)["session_id"]; !ok {
		t.Fatal("typed authority-sensitive schema does not expose session_id")
	}
	missing := callMCP(t, server, mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "planner_report_publish", "arguments": map[string]any{"handoff_id": "x", "report": map[string]any{}}}}))
	result, ok := missing["result"].(map[string]any)
	if !ok || result["isError"] != true {
		t.Fatalf("typed action without session was accepted: %#v", missing)
	}
}

func TestTypedAuthoritySensitiveActionRejectsMissingWorkflowPolicy(t *testing.T) {
	server := newSessionTestServer(t)
	started := genericStructured(t, sessionCall(t, server, map[string]any{"action": "start", "project_id": "example", "role": durableSession.RoleDelivery, "session_type": durableSession.SessionTypeChatGPT}))
	sessionID := started["session"].(map[string]any)["session_id"].(string)
	response := callMCP(t, server, mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "planner_report_publish", "arguments": map[string]any{"session_id": sessionID, "handoff_id": "missing", "report": map[string]any{}}}}))
	result, ok := response["result"].(map[string]any)
	if !ok || result["isError"] != true {
		t.Fatalf("typed action without workflow policy was accepted: %#v", response)
	}
	content := result["content"].([]any)
	textContent := content[0].(map[string]any)["text"].(string)
	if !strings.Contains(textContent, "workflow policy") {
		t.Fatalf("typed missing-policy error was not explicit: %s", textContent)
	}
}

func TestGenericSessionRejectsInvalidAndEndedAuthority(t *testing.T) {
	server := newSessionTestServer(t)
	var calls int
	registerAuthorityTestAction(t, server, "test/delivery", durableSession.RoleDelivery, false, &calls)
	invalidResponse := callMCP(t, server, mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "call", "arguments": map[string]any{"session_id": "S-0000000", "action": "test/delivery", "input": map[string]any{"value": "x"}}}}))
	invalid, ok := invalidResponse["result"].(map[string]any)
	if !ok || invalid["isError"] != true {
		t.Fatalf("invalid session was accepted: %#v", invalid)
	}
	started := genericStructured(t, sessionCall(t, server, map[string]any{"action": "start", "project_id": "example", "role": durableSession.RoleDelivery, "session_type": durableSession.SessionTypeChatGPT}))
	sessionID := started["session"].(map[string]any)["session_id"].(string)
	_ = genericStructured(t, sessionCall(t, server, map[string]any{"action": "end", "session_id": sessionID}))
	endedResponse := callMCP(t, server, mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{"name": "call", "arguments": map[string]any{"session_id": sessionID, "action": "test/delivery", "input": map[string]any{"value": "x"}}}}))
	ended, ok := endedResponse["result"].(map[string]any)
	if !ok || ended["isError"] != true || calls != 0 {
		t.Fatalf("ended session was accepted: result=%#v calls=%d", ended, calls)
	}
}

func TestGenericAuthorityDeclaredActionRejectsWrongProject(t *testing.T) {
	server := newSessionTestServer(t)
	calls := 0
	if err := server.RegisterGenericAction(GenericAction{
		Path:          "test/delivery-project",
		Description:   "project-bound delivery authority test action",
		InputSchema:   obj(map[string]any{"project_id": str("project")}, "project_id"),
		OutputSchema:  closedOutput(map[string]any{"ok": outputBoolean()}, "ok"),
		AuthorityRole: durableSession.RoleDelivery,
		Execute: func(context.Context, json.RawMessage) (any, error) {
			calls++
			return map[string]any{"ok": true}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	started := genericStructured(t, sessionCall(t, server, map[string]any{"action": "start", "project_id": "example", "role": durableSession.RoleDelivery, "session_type": durableSession.SessionTypeChatGPT}))
	sessionID := started["session"].(map[string]any)["session_id"].(string)
	result := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "call", "arguments": map[string]any{"session_id": sessionID, "action": "test/delivery-project", "input": map[string]any{"project_id": "other"}}}})))
	if result["is_error"] != true || calls != 0 || !strings.Contains(result["result"].(map[string]any)["error"].(string), "does not match session project") {
		t.Fatalf("wrong project was accepted for authority-declared action: result=%#v calls=%d", result, calls)
	}
}
