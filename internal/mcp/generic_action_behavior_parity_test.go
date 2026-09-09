package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

func typedStructured(t *testing.T, response map[string]any) map[string]any {
	t.Helper()
	result, ok := response["result"].(map[string]any)
	if !ok || result["isError"] == true {
		t.Fatalf("typed MCP call failed: %#v", response)
	}
	structured, ok := result["structuredContent"].(map[string]any)
	if !ok {
		t.Fatalf("typed MCP call omitted structured content: %#v", response)
	}
	return structured
}

func genericActionResult(t *testing.T, response map[string]any) map[string]any {
	t.Helper()
	structured := genericStructured(t, response)
	if structured["is_error"] != false {
		t.Fatalf("generic MCP action failed: %#v", structured)
	}
	result, ok := structured["result"].(map[string]any)
	if !ok {
		t.Fatalf("generic MCP action omitted result: %#v", structured)
	}
	return result
}

func assertJSONEqual(t *testing.T, want, got map[string]any) {
	t.Helper()
	wantJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(wantJSON, gotJSON) {
		t.Fatalf("typed/generic results diverged\nwant=%s\ngot=%s", wantJSON, gotJSON)
	}
}

func callTypedAndGeneric(t *testing.T, server *Server, sessionID, name, action string, input map[string]any) (map[string]any, map[string]any) {
	t.Helper()
	arguments := input
	if name == "call" {
		arguments = map[string]any{"session_id": sessionID, "action": action, "input": input}
	}
	typed := typedStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": name, "arguments": arguments},
	})))
	generic := genericActionResult(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{
			"session_id": sessionID,
			"action":     action,
			"input":      input,
		}},
	})))
	if name == "call" {
		result, ok := typed["result"].(map[string]any)
		if !ok {
			t.Fatalf("public call omitted result: %#v", typed)
		}
		typed = result
	}
	assertJSONEqual(t, typed, generic)
	return typed, generic
}

func TestTypedAndGenericTaskListCursorParity(t *testing.T) {
	s, _ := newWorkflowPolicyStatusService(t)
	ctx := context.Background()
	server := &Server{
		Service:          s,
		AuthorityContext: authority.WithPlanner(ctx),
	}
	sessionID := genericSession(t, s, "example")

	direct, _ := callTypedAndGeneric(t, server, sessionID, "call", "task/list", map[string]any{})
	repeat, _ := callTypedAndGeneric(t, server, sessionID, "call", "task/list", map[string]any{})
	assertJSONEqual(t, direct, repeat)
}

func TestRetiredProjectListIsNotPubliclyCallable(t *testing.T) {
	server := newSessionTestServer(t)
	response := callMCPRaw(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "project_list", "arguments": map[string]any{}},
	}))
	if response["error"] == nil {
		t.Fatalf("retired project_list remained callable: %#v", response)
	}
}

func TestGenericAgentTailTranscriptDedupe(t *testing.T) {
	s, revision := newWorkflowPolicyStatusService(t)
	ctx := context.Background()
	seedMCPTestCodingAgent(t, s, revision)
	script := filepath.Join(t.TempDir(), "airelay")
	marker := filepath.Join(t.TempDir(), "tail-session")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ncase \"$1\" in\nsession-status) if [ \"$3\" = --json ]; then printf '{\"sessionKey\":\"%s\",\"profile\":\"coding\",\"controllerReachable\":true,\"state\":\"idle\"}' \"$2\"; else printf 'Controller: reachable\\nState: idle\\n'; fi ;;\ntail) printf '%s' \"$2\" > "+marker+"; printf 'two\\nthree\\n' ;;\n*) exit 99 ;;\nesac\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	s.Airelay.Command = script
	server := &Server{
		Service:          s,
		AuthorityContext: authority.WithPlanner(ctx),
	}
	sessionID := genericSession(t, s, "example")
	ref := "durable-agent-ref"
	target, err := mcpSQLiteSessionStore(t, s).Create(durableSession.CreateInput{ProjectID: "example", ProjectCode: "EXM", Role: durableSession.RoleAgent, SessionType: durableSession.SessionTypeChatGPT, SessionRef: &ref})
	if err != nil {
		t.Fatal(err)
	}
	tailInput := map[string]any{"session": target.ID, "lines": 2}
	tailCall := func(id int, caller string) map[string]any {
		t.Helper()
		return genericActionResult(t, callMCP(t, server, mustJSON(t, map[string]any{
			"jsonrpc": "2.0", "id": id, "method": "tools/call",
			"params": map[string]any{"name": "call", "arguments": map[string]any{
				"session_id": caller, "action": "agent/tail", "input": tailInput,
			}},
		})))
	}
	first := tailCall(1, sessionID)
	lines, ok := first["lines"].([]any)
	if !ok || len(lines) != 2 {
		t.Fatalf("initial transcript read=%#v", first)
	}
	if got, err := os.ReadFile(marker); err != nil || string(got) != ref {
		t.Fatalf("tail did not receive stored Agent SessionRef: got=%q err=%v", got, err)
	}
	if first["session"] != target.ID {
		t.Fatalf("tail output did not echo durable Agent session: %#v", first)
	}
	repeat := tailCall(2, sessionID)
	repeatLines, ok := repeat["lines"].([]any)
	if !ok || len(repeatLines) != 0 {
		t.Fatalf("unchanged transcript was not deduped=%#v", repeat)
	}
	secondSessionID := genericSession(t, s, "example")
	independent := tailCall(3, secondSessionID)
	independentLines, ok := independent["lines"].([]any)
	if !ok || len(independentLines) != 2 {
		t.Fatalf("different durable session did not receive an independent first window=%#v", independent)
	}
	unknownOverride := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 4, "method": "tools/call", "params": map[string]any{"name": "call", "arguments": map[string]any{"session_id": sessionID, "action": "agent/tail", "input": map[string]any{"session": "example_master", "lines": 2, "dedupe": false}}}})))
	if unknownOverride["is_error"] != true {
		t.Fatalf("caller-controlled dedupe override was accepted=%#v", unknownOverride)
	}
}
