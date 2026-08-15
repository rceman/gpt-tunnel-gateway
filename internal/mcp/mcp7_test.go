package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

func TestMCP249ExposesExactlyFiveTopLevelTools(t *testing.T) {
	server := newSessionTestServer(t)
	response := callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/list",
	}))
	result := response["result"].(map[string]any)
	tools := result["tools"].([]any)
	want := []string{"batch", "call", "schema", "session_start", "session_update"}
	got := make([]string, 0, len(tools))
	for _, raw := range tools {
		got = append(got, raw.(map[string]any)["name"].(string))
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("public MCP tool surface=%v want=%v", got, want)
	}
	legacy := callMCPRaw(t, server, mustJSON(t, map[string]any{
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
	started := genericStructured(t, callMCPRaw(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "session_start", "arguments": map[string]any{"role": "delivery"}},
	})))
	sessionID := started["session"].(string)
	bound := genericStructured(t, callMCPRaw(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "session_update", "arguments": map[string]any{"session": sessionID, "project_id": "example"}},
	})))
	if bound["is_error"] == true {
		t.Fatalf("session bind failed: %#v", bound)
	}
	revision, err := server.Service.Hub.RemoteRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_ = adoptTestWorkflowPolicy(t, server.Service, "example", revision)
	rules := genericStructured(t, callMCPRaw(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{"session": sessionID, "action": "rules/read", "input": map[string]any{}}},
	})))
	if rules["is_error"] == true {
		t.Fatalf("rules/read failed: %#v", rules)
	}

	ping := genericActionResult(t, callMCPRaw(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 4, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{"session": sessionID, "action": "gateway/status", "input": map[string]any{}}},
	})))
	if ping["gateway_id"] != "test_gateway" {
		t.Fatalf("sessionless ping failed: %#v", ping)
	}
	if runtime, ok := ping["runtime_identity"].(map[string]any); !ok || runtime["artifact_set_coherent"] == nil || runtime["running_gateway_matches_installed"] == nil {
		t.Fatalf("sessionless status omitted server-owned runtime identity: %#v", ping)
	}
	if err := validateOutputValue(toolOutputSchemas["status"], ping); err != nil {
		t.Fatalf("sessionless status violated its output schema: %v", err)
	}
	projectIDStatus := callMCPRaw(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 5, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{"session": sessionID, "action": "gateway/status", "input": map[string]any{"project_id": "example"}}},
	}))
	if genericStructured(t, projectIDStatus)["is_error"] != true {
		t.Fatalf("status accepted project_id as alternate authority: %#v", projectIDStatus)
	}
	if _, ok := ping["project_status"].(map[string]any); !ok {
		t.Fatalf("session-bound status omitted project status: %#v", ping)
	}
	if err := validateOutputValue(toolOutputSchemas["status"], ping); err != nil {
		t.Fatalf("session-bound status violated its output schema: %v", err)
	}
	genericProjectStatus := callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 6, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{"session": sessionID, "action": "project/status", "input": map[string]any{}}},
	}))
	genericStructuredResult := genericStructured(t, genericProjectStatus)
	if genericStructuredResult["is_error"] != true {
		t.Fatalf("project/status remained routable through call: %#v", genericProjectStatus)
	}

	missingSession := callMCPRaw(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 7, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{"action": "project/read", "input": map[string]any{}}},
	}))
	if missingSession["error"] == nil {
		t.Fatalf("sessionless call was not rejected: %#v", missingSession)
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

func TestMCP249SessionFirstBindingAndEnvelopeGuards(t *testing.T) {
	server := newSessionTestServer(t)
	started := genericStructured(t, callMCPRaw(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "session_start", "arguments": map[string]any{"role": durableSession.RoleDelivery, "label": "test"}},
	})))
	sessionID, ok := started["session"].(string)
	if !ok || sessionID == "" {
		t.Fatalf("session_start did not return public session: %#v", started)
	}
	if _, ok := started["rules"].(map[string]any); !ok {
		t.Fatalf("session_start omitted global rules: %#v", started)
	}
	if _, ok := started["projects"].([]any); !ok {
		t.Fatalf("session_start omitted active projects: %#v", started)
	}

	bound := genericStructured(t, callMCPRaw(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "session_update", "arguments": map[string]any{"session": sessionID, "project_id": "example", "ref": "first"}},
	})))
	if bound["project_id"] != "example" || bound["rules_acknowledgement_required"] != true {
		t.Fatalf("session_update did not bind project: %#v", bound)
	}
	retried := genericStructured(t, callMCPRaw(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "tools/call",
		"params": map[string]any{"name": "session_update", "arguments": map[string]any{"session": sessionID, "project_id": "example", "ref": "retry"}},
	})))
	if retried["project_id"] != "example" {
		t.Fatalf("same-project session_update was not retry-safe: %#v", retried)
	}
	record, err := durableSession.NewStore(server.Service.Config.StateDir).Get(sessionID)
	if err != nil || record.ProjectID != "example" || record.SessionRef == nil || *record.SessionRef != "retry" {
		t.Fatalf("session update did not persist the bound identity/ref: %#v err=%v", record, err)
	}

	cross := callMCPRaw(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 4, "method": "tools/call",
		"params": map[string]any{"name": "session_update", "arguments": map[string]any{"session": sessionID, "project_id": "other"}},
	}))
	if result, ok := cross["result"].(map[string]any); !ok || result["isError"] != true {
		t.Fatalf("cross-project session_update was not rejected: %#v", cross)
	}
	unchanged, err := durableSession.NewStore(server.Service.Config.StateDir).Get(sessionID)
	if err != nil || unchanged.ProjectID != "example" {
		t.Fatalf("cross-project rejection changed session authority: %#v err=%v", unchanged, err)
	}

	wrongField := callMCPRaw(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 5, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{"session_id": sessionID, "action": "rules/read", "input": map[string]any{}}},
	}))
	if wrongField["error"] == nil {
		t.Fatalf("call accepted retired session_id envelope: %#v", wrongField)
	}
	missingBatchSession := callMCPRaw(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 6, "method": "tools/call",
		"params": map[string]any{"name": "batch", "arguments": map[string]any{"calls": []any{}}},
	}))
	if missingBatchSession["error"] == nil {
		t.Fatalf("batch accepted without root session: %#v", missingBatchSession)
	}
	retiredBind := genericStructured(t, callMCPRaw(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 7, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{"session": sessionID, "action": "session/bind", "input": map[string]any{"project_id": "example"}}},
	})))
	if retiredBind["is_error"] != true {
		t.Fatalf("retired session/bind action remained callable: %#v", retiredBind)
	}
}

func TestMCP7SessionStartAndUpdateStayUnderOneSecond(t *testing.T) {
	server := newSessionTestServer(t)
	startedAt := time.Now()
	started := genericStructured(t, callMCPRaw(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "session_start", "arguments": map[string]any{"role": durableSession.RoleDelivery}},
	})))
	startLatency := time.Since(startedAt)
	if startLatency >= time.Second {
		t.Fatalf("session_start exceeded one second: %s", startLatency)
	}
	sessionID := started["session"].(string)
	updatedAt := time.Now()
	updated := genericStructured(t, callMCPRaw(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "session_update", "arguments": map[string]any{"session": sessionID, "project_id": "example"}},
	})))
	updateLatency := time.Since(updatedAt)
	if updated["is_error"] == true {
		t.Fatalf("session_update failed: %#v", updated)
	}
	if updateLatency >= time.Second {
		t.Fatalf("session_update exceeded one second: %s", updateLatency)
	}
	t.Logf("session_start latency: %s; session_update latency: %s", startLatency, updateLatency)
}
