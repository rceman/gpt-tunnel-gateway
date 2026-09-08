package mcp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
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
	one, err := store.Create(durableSession.CreateInput{ProjectID: "example", ProjectCode: "EXM", Role: durableSession.RoleAgent, SessionType: durableSession.SessionTypeChatGPT, SessionRef: &refOne})
	if err != nil {
		t.Fatal(err)
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
	selected := call(1, map[string]any{"lines": 1})
	selectedResult := selected["result"].(map[string]any)
	if selected["is_error"] != false || selectedResult["session"] != one.ID {
		t.Fatalf("omitted session did not select the unique durable Agent: %#v", selected)
	}
	if got, err := os.ReadFile(marker); err != nil || string(got) != refOne {
		t.Fatalf("unique selection did not pass stored SessionRef: got=%q err=%v", got, err)
	}
	if _, err := store.End(one.ID); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	noAgent := call(2, map[string]any{"lines": 1})
	if noAgent["is_error"] != true || !strings.Contains(fmt.Sprint(noAgent["result"]), "no active Agent session exists") {
		t.Fatalf("zero active Agent sessions was not rejected: %#v", noAgent)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("zero-session failure invoked Airelay: %v", err)
	}
	refA, refB := "durable-ref-a", "durable-ref-b"
	if _, err := store.Create(durableSession.CreateInput{ProjectID: "example", ProjectCode: "EXM", Role: durableSession.RoleAgent, SessionType: durableSession.SessionTypeChatGPT, SessionRef: &refA}); err != nil {
		t.Fatal(err)
	}
	b, err := store.Create(durableSession.CreateInput{ProjectID: "example", ProjectCode: "EXM", Role: durableSession.RoleAgent, SessionType: durableSession.SessionTypeChatGPT, SessionRef: &refB})
	if err != nil {
		t.Fatal(err)
	}
	ambiguous := call(3, map[string]any{"lines": 1})
	if ambiguous["is_error"] != true || !strings.Contains(fmt.Sprint(ambiguous["result"]), "multiple active Agent sessions") {
		t.Fatalf("multiple active Agent sessions were not rejected: %#v", ambiguous)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("ambiguous selection invoked Airelay: %v", err)
	}
	explicit := call(4, map[string]any{"session": b.ID, "lines": 1})
	if explicit["is_error"] != false || explicit["result"].(map[string]any)["session"] != b.ID {
		t.Fatalf("explicit session did not override ambiguity: %#v", explicit)
	}
	if got, err := os.ReadFile(marker); err != nil || string(got) != refB {
		t.Fatalf("explicit selection did not pass selected SessionRef: got=%q err=%v", got, err)
	}
}
