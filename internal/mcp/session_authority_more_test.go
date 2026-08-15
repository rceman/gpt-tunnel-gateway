package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

func TestGenericAuthorityRequiresDurableWorkflowPolicy(t *testing.T) {
	server := newSessionTestServer(t)
	revision, err := server.Service.Hub.RemoteRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, err = server.Service.Hub.Transact(context.Background(), revision, "test: remove canonical workflow configuration", func(worktree string) ([]string, error) {
		path := hub.ProtocolRoot + "/projects/example/configuration/current.json"
		if err := os.Remove(filepath.Join(worktree, filepath.FromSlash(path))); err != nil {
			return nil, err
		}
		return []string{path}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
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

func TestBoundSessionRulesUseLocalPolicyCacheWithoutHubRead(t *testing.T) {
	server := newSessionTestServer(t)
	policy, err := server.Service.ProjectWorkflowPolicyRead(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	server.Service.Hub.Config.Hub.RepositoryURL = filepath.Join(t.TempDir(), "unavailable.git")
	record := durableSession.Record{
		ID:                   "SP-FASTAUTH1",
		Role:                 durableSession.RoleDelivery,
		ProjectID:            "example",
		GlobalRulesRevision:  globalWorkflowRevision,
		GlobalRulesDigest:    globalWorkflowDigest(),
		ProjectRulesRevision: policy.Revision,
		ProjectRulesDigest:   digestJSON(policy),
	}
	if err := server.validateSessionRules(context.Background(), record, "agent/tail"); err != nil {
		t.Fatalf("cached bound-session authority unexpectedly used Hub: %v", err)
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
	if result["is_error"] != true || calls != 0 {
		t.Fatalf("wrong project was accepted for authority-declared action: result=%#v calls=%d", result, calls)
	}
}
