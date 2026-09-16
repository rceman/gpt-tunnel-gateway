package mcp

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

func TestGenericAgentTailSelectsOnlyUnambiguousDurableAgentSession(t *testing.T) {
	s, revision := newWorkflowPolicyStatusService(t)
	seedMCPTestCodingAgent(t, s, revision)
	marker := filepath.Join(t.TempDir(), "tail-session")
	script := filepath.Join(t.TempDir(), "airelay")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ncase \"$1\" in\ntail) printf '%s' \"$2\" > "+marker+"; printf 'line\\n' ;;\n*) exit 99 ;;\nesac\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	s.Airelay.Command = script
	server := &Server{
		Service:          s,
		AuthorityContext: authority.WithPlanner(context.Background()),
	}
	plannerID := genericSession(t, s, "example")
	store := mcpSQLiteSessionStore(t, s)
	refOne := "durable-ref-one"
	_, err := store.Create(durableSession.CreateInput{ProjectID: "example", ProjectCode: "EXM", Role: durableSession.RoleWorker, SessionType: durableSession.SessionTypeChatGPT, SessionRef: &refOne})
	if err != nil {
		t.Fatal(err)
	}
	s.Config.ProjectAgentBindings = map[string]map[string]config.AgentBinding{
		"example": {"coding-example": {SessionKey: refOne}},
	}
	call := func(id int, input map[string]any) map[string]any {
		t.Helper()
		return genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
			"jsonrpc": "2.0", "id": id, "method": "tools/call",
			"params": map[string]any{"name": "call", "arguments": map[string]any{
				"session_id": plannerID, "action": "agent/tail", "input": input,
			}},
		})))
	}
	selected := call(1, map[string]any{"agent": "coding-example", "lines": 1})
	selectedResult := selected["result"].(map[string]any)
	if selected["is_error"] != false || selectedResult["agent"] != "coding-example" {
		t.Fatalf("logical Agent did not select the configured durable target: %#v", selected)
	}
	if got, err := os.ReadFile(marker); err != nil || string(got) != refOne {
		t.Fatalf("unique selection did not pass stored SessionRef: got=%q err=%v", got, err)
	}
	delete(s.Config.ProjectAgentBindings["example"], "coding-example")
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	noAgent := call(2, map[string]any{"agent": "coding-example", "lines": 1})
	if noAgent["is_error"] != true {
		t.Fatalf("unbound logical Agent was not rejected: %#v", noAgent)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("unbound-Agent failure invoked Airelay: %v", err)
	}
	refA, refB := "durable-ref-a", "durable-ref-b"
	if _, err := store.Create(durableSession.CreateInput{ProjectID: "example", ProjectCode: "EXM", Role: durableSession.RoleWorker, SessionType: durableSession.SessionTypeChatGPT, SessionRef: &refA}); err != nil {
		t.Fatal(err)
	}
	b, err := store.Create(durableSession.CreateInput{ProjectID: "example", ProjectCode: "EXM", Role: durableSession.RoleWorker, SessionType: durableSession.SessionTypeChatGPT, SessionRef: &refB})
	if err != nil {
		t.Fatal(err)
	}
	s.Config.ProjectAgentBindings["example"]["coding-example"] = config.AgentBinding{SessionKey: refB}
	selectedAgain := call(3, map[string]any{"agent": "coding-example", "lines": 1})
	if selectedAgain["is_error"] != false || selectedAgain["result"].(map[string]any)["agent"] != "coding-example" {
		t.Fatalf("configured logical Agent did not resolve its current target: %#v", selectedAgain)
	}
	privateSelector := call(4, map[string]any{"session": b.ID, "lines": 1})
	if privateSelector["is_error"] != true {
		t.Fatalf("private Session selector was accepted: %#v", privateSelector)
	}
	if got, err := os.ReadFile(marker); err != nil || string(got) != refB {
		t.Fatalf("explicit selection did not pass selected SessionRef: got=%q err=%v", got, err)
	}
}
