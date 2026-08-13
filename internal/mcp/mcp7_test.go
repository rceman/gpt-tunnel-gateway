package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestMCP7ExposesExactlySevenTopLevelTools(t *testing.T) {
	server := newSessionTestServer(t)
	response := callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/list",
	}))
	result := response["result"].(map[string]any)
	tools := result["tools"].([]any)
	want := []string{"batch", "call", "project", "rules", "schema", "session", "status"}
	got := make([]string, 0, len(tools))
	for _, raw := range tools {
		got = append(got, raw.(map[string]any)["name"].(string))
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("public MCP tool surface=%v want=%v", got, want)
	}
	legacy := callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "system_ping", "arguments": map[string]any{}},
	}))
	if legacy["error"] == nil {
		t.Fatalf("legacy top-level tool remained callable: %#v", legacy)
	}
	if got := mcp7ProjectActions["remove"]; got != "project/remove" {
		t.Fatalf("project/remove is not exposed through the existing project action: %q", got)
	}
}

func TestMCP7SessionlessBootstrapAndSessionBoundTransport(t *testing.T) {
	server := newSessionTestServer(t)
	project := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "project", "arguments": map[string]any{
			"action": "read", "input": map[string]any{"project_id": "example"},
		}},
	})))
	if project["action"] != "project/read" || project["is_error"] != false {
		t.Fatalf("sessionless project bootstrap failed: %#v", project)
	}

	revision, err := server.Service.Hub.RemoteRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_ = adoptTestWorkflowPolicy(t, server.Service, "example", revision)
	rules := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "rules", "arguments": map[string]any{"project_id": "example"}},
	})))
	if rules["project_id"] != "example" {
		t.Fatalf("sessionless rules bootstrap failed: %#v", rules)
	}

	ping := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "tools/call",
		"params": map[string]any{"name": "status", "arguments": map[string]any{}},
	})))
	if ping["gateway_id"] != "test_gateway" {
		t.Fatalf("sessionless ping failed: %#v", ping)
	}
	if err := validateOutputValue(toolOutputSchemas["status"], ping); err != nil {
		t.Fatalf("sessionless status violated its output schema: %v", err)
	}
	projectIDStatus := callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 4, "method": "tools/call",
		"params": map[string]any{"name": "status", "arguments": map[string]any{"project_id": "example"}},
	}))
	if projectIDStatus["error"] == nil {
		t.Fatalf("status accepted project_id as alternate authority: %#v", projectIDStatus)
	}
	statusSession := genericSession(t, server.Service, "example")
	bound := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 6, "method": "tools/call",
		"params": map[string]any{"name": "status", "arguments": map[string]any{"session_id": statusSession}},
	})))
	if _, ok := bound["project_status"].(map[string]any); !ok {
		t.Fatalf("session-bound status omitted project status: %#v", bound)
	}
	if err := validateOutputValue(toolOutputSchemas["status"], bound); err != nil {
		t.Fatalf("session-bound status violated its output schema: %v", err)
	}
	projectStatus := callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 7, "method": "tools/call",
		"params": map[string]any{"name": "project", "arguments": map[string]any{"action": "status", "input": map[string]any{"project_id": "example"}}},
	}))
	result := projectStatus["result"].(map[string]any)
	if result["isError"] != true {
		t.Fatalf("project/status remained in the project whitelist: %#v", projectStatus)
	}
	genericProjectStatus := callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 8, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{"session_id": statusSession, "action": "project/status", "input": map[string]any{"project_id": "example"}}},
	}))
	genericStructuredResult := genericStructured(t, genericProjectStatus)
	if genericStructuredResult["is_error"] != true {
		t.Fatalf("project/status remained routable through call: %#v", genericProjectStatus)
	}

	missingSession := callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 4, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{"action": "project/read", "input": map[string]any{}}},
	}))
	if missingSession["error"] == nil || !strings.Contains(string(mustJSON(t, missingSession)), "session_id") {
		t.Fatalf("sessionless call was not rejected: %#v", missingSession)
	}

	started := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 5, "method": "tools/call",
		"params": map[string]any{"name": "session", "arguments": map[string]any{
			"action": "start", "project_id": "example", "role": "delivery", "session_type": "chatgpt",
		}},
	})))
	if started["action"] != "start" {
		t.Fatalf("sessionless session.start failed: %#v", started)
	}
}

func TestMCP7BatchCannotCrossSessionProjectAuthority(t *testing.T) {
	server := newSessionTestServer(t)
	if err := server.RegisterGenericAction(GenericAction{
		Path:         "test/project",
		Description:  "Project-bound transport test action.",
		InputSchema:  obj(map[string]any{"project_id": str("Project identifier.")}, "project_id"),
		OutputSchema: closedOutput(map[string]any{"project_id": outputString()}, "project_id"),
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			return map[string]any{"project_id": optionalString(raw, "project_id")}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	sessionID := genericSession(t, server.Service, "example")
	response := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "batch", "arguments": map[string]any{
			"session_id": sessionID,
			"calls":      []any{map[string]any{"action": "test/project", "input": map[string]any{"project_id": "other"}}},
		}},
	})))
	results := response["results"].([]any)
	if len(results) != 1 || results[0].(map[string]any)["is_error"] != true {
		t.Fatalf("cross-project batch was not rejected: %#v", response)
	}
}
