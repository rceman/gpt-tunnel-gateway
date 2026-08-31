package mcp

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

type agentADR81Trace struct {
	References []string
	Why        string
}

func agentADR81Metadata(t *testing.T, trace agentADR81Trace) map[string]any {
	t.Helper()
	if !reflect.DeepEqual(trace.References, []string{"GTW-ADR84", "GTW-ADR83"}) {
		t.Fatalf("ADR81 references=%v", trace.References)
	}
	if trace.Why == "" {
		t.Fatal("ADR81 trace is missing why")
	}
	return map[string]any{
		"adr":        "GTW-ADR81",
		"references": trace.References,
		"why":        trace.Why,
	}
}

func TestCanonicalAgentPublicMCPHTTPContractCoversAllActions(t *testing.T) {
	s, revision := newWorkflowPolicyStatusService(t)
	revision = seedMCPTestCodingAgent(t, s, revision)
	_ = ensureMCPTestProjectIdentifiers(t, s)
	command := filepath.Join(t.TempDir(), "airelay")
	if err := os.WriteFile(command, []byte("#!/bin/sh\ncase \"$1\" in\nsession-status) printf 'Controller: reachable\\nState: idle\\n' ;;\ntail) printf 'public e2e\\n' ;;\nprompt) printf 'sent\\n' ;;\nesac\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	s.Config.AirelayCommand = command
	s.Airelay.Command = command
	server := &Server{Service: s, AuthorityContext: authority.WithDelivery(context.Background())}
	httpServer := httptest.NewServer(server.Router())
	defer httpServer.Close()
	client := &frozenConnectorClient{http: httpServer.Client(), endpoint: httpServer.URL + "/mcp", methods: map[string]int{}}

	initialized := client.request(t, "initialize", map[string]any{
		"protocolVersion": "2025-03-26",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "tsk443-agent-e2e", "version": "1"},
	})
	if initialized["error"] != nil {
		t.Fatalf("initialize failed: %#v", initialized)
	}
	client.notify(t, "notifications/initialized")
	tools := client.request(t, "tools/list", map[string]any{})
	toolResult, ok := tools["result"].(map[string]any)
	if !ok {
		t.Fatalf("tools/list result=%#v", tools)
	}
	toolList, ok := toolResult["tools"].([]any)
	if !ok {
		t.Fatalf("tools/list tools=%#v", toolResult["tools"])
	}
	if len(toolList) != 6 {
		t.Fatalf("top-level tool count=%d, want 6", len(toolList))
	}

	started := frozenResult(t, client.request(t, "tools/call", map[string]any{
		"name": "session_start",
		"arguments": map[string]any{
			"gateway": "test_gateway",
			"project": "example",
			"role":    "delivery",
		},
	}))
	sessionID, ok := started["session"].(string)
	if !ok || sessionID == "" {
		t.Fatalf("session_start did not return a session: %#v", started)
	}

	schemaMeta := agentADR81Metadata(t, agentADR81Trace{
		References: []string{"GTW-ADR84", "GTW-ADR83"},
		Why:        "ADR81 records the public Agent action inventory and ADR84/ADR83 define its canonical Agent naming and contract.",
	})
	schema := frozenResult(t, client.requestWithMeta(t, "tools/call", map[string]any{
		"name":      "schema",
		"arguments": map[string]any{"session": sessionID, "path": "agent"},
	}, schemaMeta))
	actions, ok := schema["actions"].([]any)
	if !ok || len(actions) != 6 {
		t.Fatalf("Agent schema actions=%#v", schema["actions"])
	}
	contracts := make(map[string]map[string]any, len(actions))
	for _, raw := range actions {
		action, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("invalid Agent schema action=%#v", raw)
		}
		path, _ := action["path"].(string)
		contracts[path] = action
	}
	wantActions := []string{"agent/await", "agent/interrupt", "agent/list", "agent/prompt", "agent/status", "agent/tail"}
	gotActions := make([]string, 0, len(contracts))
	for path := range contracts {
		gotActions = append(gotActions, path)
	}
	sortStrings(gotActions)
	if !reflect.DeepEqual(gotActions, wantActions) {
		t.Fatalf("Agent schema actions=%v, want=%v", gotActions, wantActions)
	}

	for _, action := range wantActions {
		contract := frozenResult(t, client.requestWithMeta(t, "tools/call", map[string]any{
			"name":      "schema",
			"arguments": map[string]any{"session": sessionID, "path": action},
		}, agentADR81Metadata(t, agentADR81Trace{
			References: []string{"GTW-ADR84", "GTW-ADR83"},
			Why:        "ADR81 ties each public Agent action schema to the canonical ADR84/ADR83 naming and routing contract.",
		})))
		if contract["path"] != action {
			t.Fatalf("schema(%s) returned %#v", action, contract)
		}
		if _, ok := contract["contract"].(map[string]any)["input_schema"]; !ok {
			t.Fatalf("schema(%s) omitted input_schema: %#v", action, contract)
		}
		contracts[action] = contract["contract"].(map[string]any)
	}

	call := func(action string, input map[string]any, why string) map[string]any {
		t.Helper()
		contract, ok := contracts[action]
		if !ok {
			t.Fatalf("missing public contract for %s", action)
		}
		inputSchema, ok := contract["input_schema"].(map[string]any)
		if !ok {
			t.Fatalf("%s omitted input_schema: %#v", action, contract)
		}
		if err := validateToolArguments(inputSchema, mustJSON(t, input)); err != nil {
			t.Fatalf("%s public schema rejects its E2E input: %v", action, err)
		}
		structured := frozenResult(t, client.requestWithMeta(t, "tools/call", map[string]any{
			"name": "call",
			"arguments": map[string]any{
				"session": sessionID,
				"action":  action,
				"input":   input,
			},
		}, agentADR81Metadata(t, agentADR81Trace{
			References: []string{"GTW-ADR84", "GTW-ADR83"},
			Why:        why,
		})))
		if structured["ok"] != true {
			t.Fatalf("%s failed: %#v", action, structured)
		}
		result, ok := structured["result"].(map[string]any)
		if !ok {
			t.Fatalf("%s omitted result: %#v", action, structured)
		}
		return result
	}

	list := call("agent/list", map[string]any{}, "ADR81 traces the canonical list action under the ADR84/ADR83 Agent surface.")
	if agents, ok := list["agents"].([]any); !ok || len(agents) != 1 {
		t.Fatalf("agent/list result=%#v", list)
	}
	status := call("agent/status", map[string]any{"agent": "coding-example"}, "ADR81 traces compact status naming under the ADR84/ADR83 Agent surface.")
	if status["agent"] != "coding-example" {
		t.Fatalf("agent/status result=%#v", status)
	}
	awaited := call("agent/await", map[string]any{"agent": "coding-example", "seconds": 1}, "ADR81 traces bounded await under the ADR85/ADR83 Agent supervision contract.")
	if awaited["agent"] != "coding-example" {
		t.Fatalf("agent/await result=%#v", awaited)
	}
	tail := call("agent/tail", map[string]any{"agent": "coding-example", "lines": 1}, "ADR81 traces the canonical tail action and ADR84/ADR83 Agent naming.")
	if tail["agent"] != "coding-example" {
		t.Fatalf("agent/tail result=%#v", tail)
	}
	prompt := call("agent/prompt", map[string]any{"agent": "coding-example", "message": "tsk443 public E2E"}, "ADR81 traces prompt dispatch through the ADR84/ADR83 Agent contract.")
	promptOperation, ok := prompt["operation"].(string)
	if !ok || promptOperation == "" || prompt["status"] != "accepted" {
		t.Fatalf("agent/prompt result=%#v", prompt)
	}
	waitAgentOperationTerminal(t, s, sessionID, promptOperation, "agent-prompt")
	interrupt := call("agent/interrupt", map[string]any{"agent": "coding-example"}, "ADR81 traces interrupt dispatch through the ADR84/ADR83 Agent contract.")
	interruptOperation, ok := interrupt["operation"].(string)
	if !ok || interruptOperation == "" || interrupt["status"] != "accepted" {
		t.Fatalf("agent/interrupt result=%#v", interrupt)
	}
	waitAgentOperationTerminal(t, s, sessionID, interruptOperation, "agent-interrupt")
}

func waitAgentOperationTerminal(t *testing.T, s *service.Service, sessionID, operationID, kind string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		value, err := s.AgentIPCOperationStatus(service.WithAgentSessionID(context.Background(), sessionID), operationID, kind)
		if err != nil {
			t.Fatal(err)
		}
		if agentOperationTerminal(value) {
			switch receipt := value.(type) {
			case service.AgentPromptReceipt:
				if receipt.Status != "completed" {
					t.Fatalf("Agent prompt failed: %#v", receipt)
				}
			case service.AgentInterruptReceipt:
				if receipt.Status != "completed" {
					t.Fatalf("Agent interrupt failed: %#v", receipt)
				}
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s operation %s did not become terminal", kind, operationID)
}

func agentOperationTerminal(value any) bool {
	switch receipt := value.(type) {
	case service.AgentPromptReceipt:
		return receipt.Status == "completed" || receipt.Status == "failed"
	case service.AgentInterruptReceipt:
		return receipt.Status == "completed" || receipt.Status == "failed"
	default:
		return false
	}
}
