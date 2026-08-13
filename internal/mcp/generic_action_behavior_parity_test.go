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

func TestTypedAndGenericTaskListSearchStatusLimitCursorParity(t *testing.T) {
	s, revision := newWorkflowPolicyStatusService(t)
	ctx := context.Background()
	_, identifierOperation, err := s.ProjectIdentifiersAdopt(ctx, service.ProjectIdentifiersAdoptInput{
		ProjectID: "example", ProjectCode: "EXM",
		WriteOptions: service.WriteOptions{ExpectedHubRevision: revision},
	})
	if err != nil {
		t.Fatal(err)
	}
	revision = identifierOperation.Hub.After
	for _, spec := range []struct{ slug, title string }{
		{"alpha-parity", "Alpha parity task"},
		{"beta-parity", "Beta parity task"},
		{"gamma-parity", "Gamma parity task"},
	} {
		_, operation, err := s.TaskCreate(ctx, service.TaskCreateInput{
			ProjectID: "example", Slug: spec.slug, Title: spec.title, Objective: spec.title,
			AcceptanceCriteria: []string{"bounded"}, OperationClass: "implementation", CreatedBy: "planner",
			WriteOptions: service.WriteOptions{ExpectedHubRevision: revision},
		})
		if err != nil {
			t.Fatal(err)
		}
		revision = operation.Hub.After
	}
	server := &Server{
		Service:          s,
		AuthorityContext: authority.WithDelivery(ctx),
	}
	sessionID := genericSession(t, s, "example")

	callTypedAndGeneric(t, server, sessionID, "call", "task/list", map[string]any{
		"query": "BETA", "status": "created", "limit": 10,
	})
	pageOne, _ := callTypedAndGeneric(t, server, sessionID, "call", "task/list", map[string]any{
		"limit": 2,
	})
	cursor, ok := pageOne["next_cursor"].(string)
	if !ok || cursor == "" || pageOne["has_more"] != true {
		t.Fatalf("task/list did not return bounded continuation: %#v", pageOne)
	}
	callTypedAndGeneric(t, server, sessionID, "call", "task/list", map[string]any{
		"limit": 2, "cursor": cursor,
	})

	batch := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "tools/call",
		"params": map[string]any{"name": "batch", "arguments": map[string]any{
			"session_id": sessionID,
			"calls":      []any{map[string]any{"action": "task/list", "input": map[string]any{"limit": 2}}},
		}},
	})))
	results := batch["results"].([]any)
	if len(results) != 1 || results[0].(map[string]any)["is_error"] != false {
		t.Fatalf("task/list batch failed: %#v", batch)
	}
	direct, _ := callTypedAndGeneric(t, server, sessionID, "call", "task/list", map[string]any{
		"limit": 2,
	})
	batchResult := results[0].(map[string]any)["result"].(map[string]any)
	assertJSONEqual(t, direct, batchResult)
}

func TestTypedAndGenericBoundedProjectListParity(t *testing.T) {
	s, _ := newWorkflowPolicyStatusService(t)
	server := &Server{
		Service:          s,
		AuthorityContext: authority.WithDelivery(context.Background()),
	}
	sessionID := genericSession(t, s, "example")
	projectResponse := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "project", "arguments": map[string]any{"action": "list", "input": map[string]any{"limit": 1}}},
	})))
	projectGeneric := genericActionResult(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{"session_id": sessionID, "action": "project/list", "input": map[string]any{"limit": 1}}},
	})))
	assertJSONEqual(t, projectResponse["result"].(map[string]any), projectGeneric)
}

func TestTypedAndGenericAgentTailCursorParity(t *testing.T) {
	s, _ := newWorkflowPolicyStatusService(t)
	ctx := context.Background()
	script := filepath.Join(t.TempDir(), "airelay")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf 'one\\ntwo\\nthree\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	s.Airelay.Command = script
	server := &Server{
		Service:          s,
		AuthorityContext: authority.WithDelivery(ctx),
	}
	sessionID := genericSession(t, s, "example")
	pageOne, _ := callTypedAndGeneric(t, server, sessionID, "call", "agent/tail", map[string]any{
		"lines": 2,
	})
	cursor, ok := pageOne["next_cursor"].(string)
	if !ok || cursor == "" {
		t.Fatalf("agent/tail did not return a cursor: %#v", pageOne)
	}
	callTypedAndGeneric(t, server, sessionID, "call", "agent/tail", map[string]any{
		"lines": 2, "cursor": cursor,
	})
}
