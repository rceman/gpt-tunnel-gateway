package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

func genericSession(t *testing.T, s *service.Service, projectID string) string {
	return genericSessionWithRole(t, s, projectID, durableSession.RoleDelivery)
}
func genericSessionWithRole(t *testing.T, s *service.Service, projectID, role string) string {
	t.Helper()
	if s.Config.StateDir == "" {
		s.Config.StateDir = t.TempDir()
	}
	projectCode := "EXM"
	if projectID != "example" {
		projectCode = "OTH"
	}
	record, err := durableSession.NewStore(s.Config.StateDir).Create(durableSession.CreateInput{ProjectID: projectID, ProjectCode: projectCode, Role: role, SessionType: durableSession.SessionTypeChatGPT})
	if err != nil {
		t.Fatal(err)
	}
	return record.ID
}
func genericStructured(t *testing.T, response map[string]any) map[string]any {
	t.Helper()
	result, ok := response["result"].(map[string]any)
	if !ok {
		t.Fatalf("missing MCP result: %#v", response)
	}
	if result["isError"] == true {
		t.Fatalf("MCP tool error: %#v", response)
	}
	structured, ok := result["structuredContent"].(map[string]any)
	if !ok {
		t.Fatalf("missing structured content: %#v", response)
	}
	// Older application-behavior tests consume the dispatcher projection. The
	// public transport is ADR84-shaped; keep this test helper focused on the
	// application result while the boundary tests assert the public envelope.
	if okValue, present := structured["ok"]; present {
		if okValue == true {
			return map[string]any{"result": structured["result"], "is_error": false}
		}
		return map[string]any{"result": map[string]any{"error": structured["error"]}, "is_error": true}
	}
	return structured
}
func TestGenericSessionStartIsDiscoverableAndCreatesPlannerSession(t *testing.T) {
	server := newSessionTestServer(t)
	dispatch := genericStructured(t, sessionCall(t, server, map[string]any{
		"action": "start", "project_id": "example", "role": durableSession.RolePlanner, "session_type": durableSession.SessionTypeChatGPT,
	}))
	if dispatch["action"] != "start" {
		t.Fatalf("session.start public call failed: %#v", dispatch)
	}
	session := dispatch["session"].(map[string]any)
	if session["role"] != durableSession.RolePlanner || !strings.HasPrefix(session["session_id"].(string), "SP-") {
		t.Fatalf("generic planner bootstrap did not create SP session: %#v", session)
	}
}
func TestQueryRunUsesSharedReadOnlyDSLAndSchemaDiscovery(t *testing.T) {
	server := newSessionTestServer(t)
	started := genericStructured(t, sessionCall(t, server, map[string]any{"action": "start", "project_id": "example", "role": durableSession.RoleDelivery, "session_type": durableSession.SessionTypeChatGPT}))
	sessionID := started["session"].(map[string]any)["session_id"].(string)
	revision, err := server.Service.Hub.RemoteRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	adoptTestWorkflowPolicy(t, server.Service, "example", revision)
	rules := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 0, "method": "tools/call", "params": map[string]any{"name": "call", "arguments": map[string]any{"session": sessionID, "action": "rules/read", "input": map[string]any{}}}})))
	if rules["is_error"] == true {
		t.Fatalf("rules/read failed after policy setup: %#v", rules)
	}
	result := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "call", "arguments": map[string]any{"session_id": sessionID, "action": "query/run", "input": map[string]any{"dsl": "task.list().select(id,status).limit(2)"}}}})))
	queryResult := result["result"].(map[string]any)
	if _, ok := result["action"]; ok || result["is_error"] == true || queryResult["entity"] != "task" {
		t.Fatalf("query/run failed: %#v", result)
	}
	runContract := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{"name": "schema", "arguments": map[string]any{"session": sessionID, "path": "query/run"}}})))
	if runContract["kind"] != "action" || runContract["path"] != "query/run" {
		t.Fatalf("query/run action was not discoverable: %#v", runContract)
	}
	runDefinition := runContract["contract"].(map[string]any)
	if runDefinition["annotations"].(map[string]any)["read_only"] != true {
		t.Fatalf("query/run contract is not read-only: %#v", runDefinition)
	}
	legacyContract, err := server.genericSchema(server.tools(), json.RawMessage(`{"path":"query/task"}`))
	if err != nil {
		t.Fatal(err)
	}
	contract := legacyContract.(map[string]any)
	if contract["kind"] != "query_entity" || contract["contract"].(map[string]any)["entity"] != "task" {
		t.Fatalf("query schema=%#v", contract)
	}
	if _, err := server.genericSchema(server.tools(), json.RawMessage(`{"path":"query/missing"}`)); err == nil || !strings.Contains(err.Error(), "query schema path") || !strings.Contains(err.Error(), "query/missing") {
		t.Fatalf("invalid query entity was accepted: %v", err)
	}
	ordinary := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 5, "method": "tools/call", "params": map[string]any{"name": "schema", "arguments": map[string]any{"session": sessionID, "path": "task/read"}}})))
	if ordinary["kind"] != "action" || ordinary["path"] != "task/read" {
		t.Fatalf("ordinary action schema lookup failed: %#v", ordinary)
	}
	invalid := callMCP(t, server, mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 3, "method": "tools/call", "params": map[string]any{"name": "call", "arguments": map[string]any{"session_id": sessionID, "action": "query/run", "input": map[string]any{"dsl": "task.read()"}}}}))
	if !strings.Contains(string(mustJSON(t, invalid)), "query must begin") {
		t.Fatalf("exact-read query was not rejected: %#v", invalid)
	}
}
func TestGenericTransportSchemasAreCompactAndApplicationIndependent(t *testing.T) {
	server := &Server{Service: service.New(config.Config{GatewayID: "home_pc"}), AuthorityContext: authority.WithDelivery(context.Background())}
	tools := server.tools()
	sessionID := genericSession(t, server.Service, "example")
	staticBytes := 0
	for _, name := range []string{"call", "schema"} {
		tool, ok := tools[name]
		if !ok {
			t.Fatalf("generic tool %q is not registered", name)
		}
		encoded, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		staticBytes += len(encoded)
		text := string(encoded)
		if strings.Contains(text, "project_id") || strings.Contains(text, "oneOf") || strings.Contains(text, "anyOf") || strings.Contains(text, "enum") {
			t.Fatalf("generic static schema embeds application contract: %s", text)
		}
	}
	t.Logf("generic static input-schema bytes=%d", staticBytes)
	root := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "schema", "arguments": map[string]any{"session": sessionID, "path": ""}},
	})))
	if root["revision"] != genericSchemaRevision || root["kind"] != "root" {
		t.Fatalf("unexpected generic schema root: %#v", root)
	}
	if len(root["domains"].([]any)) == 0 {
		t.Fatal("generic schema root has no domains")
	}
	before := make([]Tool, 0, len(tools)-2)
	after := make([]Tool, 0, len(tools))
	for name, tool := range tools {
		tool.Execute = nil
		after = append(after, tool)
		if name != "call" && name != "schema" {
			before = append(before, tool)
		}
	}
	beforeBytes, err := json.Marshal(before)
	if err != nil {
		t.Fatal(err)
	}
	afterBytes, err := json.Marshal(after)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("static MCP manifest bytes before=%d after=%d additive=%d token-estimate-before=%d after=%d", len(beforeBytes), len(afterBytes), len(afterBytes)-len(beforeBytes), (len(beforeBytes)+3)/4, (len(afterBytes)+3)/4)
}
