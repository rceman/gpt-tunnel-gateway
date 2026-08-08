package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func newSessionTestServer(t *testing.T) *Server {
	t.Helper()
	state := filepath.Join(t.TempDir(), "state")
	root := t.TempDir()
	c := config.Config{SchemaVersion: 1, GatewayID: "test_gateway", StateDir: state, MaxReadBytes: 1 << 20, MaxDiffBytes: 1 << 20, MaxListItems: 1000, Projects: map[string]config.ProjectConfig{
		"example": {Root: root, Mirror: filepath.Join(t.TempDir(), "mirror.git"), Remote: "origin", DefaultBranch: "main", AirelaySessionKey: "example_master"},
	}}
	return &Server{Service: service.New(c), AuthorityContext: authority.WithDelivery(context.Background())}
}

func sessionCall(t *testing.T, server *Server, args map[string]any) map[string]any {
	t.Helper()
	return callMCP(t, server, mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "session", "arguments": args}}))
}

func TestSessionLifecyclePersistsAndBindsProjectRole(t *testing.T) {
	server := newSessionTestServer(t)
	started := genericStructured(t, sessionCall(t, server, map[string]any{"action": "start", "project_id": "example", "role": "delivery", "session_type": "chatgpt", "label": "main"}))
	record := started["session"].(map[string]any)
	id := record["session_id"].(string)
	if !strings.HasPrefix(id, "S-") || len(id) != 10 || record["project_id"] != "example" || record["role"] != "delivery" || record["status"] != "active" {
		t.Fatalf("bad session projection: %#v", record)
	}
	info := genericStructured(t, sessionCall(t, server, map[string]any{"action": "info", "session_id": id}))
	if info["session"].(map[string]any)["session_id"] != id {
		t.Fatalf("info did not reload session: %#v", info)
	}
	updated := genericStructured(t, sessionCall(t, server, map[string]any{"action": "update", "session_id": id, "label": "renamed"}))
	if updated["session"].(map[string]any)["label"] != "renamed" {
		t.Fatalf("update projection=%#v", updated)
	}
	ended := genericStructured(t, sessionCall(t, server, map[string]any{"action": "end", "session_id": id}))
	if ended["session"].(map[string]any)["status"] != "ended" {
		t.Fatalf("end projection=%#v", ended)
	}
	path := filepath.Join(server.Service.Config.StateDir, "sessions", id+".json")
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

func TestSessionStartValidatesProjectRoleAndTypeBeforeCreation(t *testing.T) {
	server := newSessionTestServer(t)
	for _, args := range []map[string]any{
		{"action": "start", "project_id": "missing", "role": "delivery", "session_type": "chatgpt"},
		{"action": "start", "project_id": "example", "role": "operator", "session_type": "chatgpt"},
		{"action": "start", "project_id": "example", "role": "delivery", "session_type": "unknown"},
	} {
		response := sessionCall(t, server, args)
		result, ok := response["result"].(map[string]any)
		if !ok || result["isError"] != true {
			t.Fatalf("invalid start was accepted: %#v", response)
		}
	}
	entries, err := os.ReadDir(filepath.Join(server.Service.Config.StateDir, "sessions"))
	if err == nil && len(entries) != 0 {
		t.Fatalf("invalid starts created sessions: %d", len(entries))
	}
}

func TestGenericCallRequiresSessionAndInheritsProject(t *testing.T) {
	server := newSessionTestServer(t)
	var seen json.RawMessage
	if err := server.RegisterGenericAction(GenericAction{
		Path:         "test/project",
		Description:  "Project-bound test action",
		InputSchema:  obj(map[string]any{"project_id": str("Inherited project")}, "project_id"),
		OutputSchema: closedOutput(map[string]any{"project_id": outputString()}, "project_id"),
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			seen = append(seen[:0], raw...)
			var input struct {
				ProjectID string `json:"project_id"`
			}
			if err := decode(raw, &input); err != nil {
				return nil, err
			}
			return map[string]any{"project_id": input.ProjectID}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	missing := callMCP(t, server, mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "call", "arguments": map[string]any{"action": "test/project", "input": map[string]any{}}}}))
	if result, ok := missing["result"].(map[string]any); ok || result != nil {
		t.Fatalf("missing session did not fail schema validation: %#v", missing)
	}
	start := genericStructured(t, sessionCall(t, server, map[string]any{"action": "start", "project_id": "example", "role": "delivery", "session_type": "chatgpt"}))
	sessionID := start["session"].(map[string]any)["session_id"].(string)
	okCall := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{"name": "call", "arguments": map[string]any{"session_id": sessionID, "action": "test/project", "input": map[string]any{}}}})))
	if okCall["is_error"] != false || string(seen) != `{"project_id":"example"}` {
		t.Fatalf("project was not inherited: result=%#v input=%s", okCall, seen)
	}
	mismatch := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 3, "method": "tools/call", "params": map[string]any{"name": "call", "arguments": map[string]any{"session_id": sessionID, "action": "test/project", "input": map[string]any{"project_id": "other"}}}})))
	if mismatch["is_error"] != true || !strings.Contains(mismatch["result"].(map[string]any)["error"].(string), "does not match session project") {
		t.Fatalf("project mismatch was not rejected: %#v", mismatch)
	}
	if string(seen) != `{"project_id":"example"}` {
		t.Fatalf("mismatch reached handler: %s", seen)
	}
}

func TestSystemPingRemainsStandaloneAndGenericBatchContinues(t *testing.T) {
	server := newSessionTestServer(t)
	legacy := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "system_ping", "arguments": map[string]any{}}})))
	if legacy["service"] != "gpt-tunnel-gatewayd" {
		t.Fatalf("standalone system_ping failed: %#v", legacy)
	}
	root := callMCP(t, server, mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{"name": "schema", "arguments": map[string]any{"path": "system"}}}))
	result, ok := root["result"].(map[string]any)
	if !ok || result["isError"] != true {
		t.Fatalf("system domain unexpectedly exposed: %#v", root)
	}
}
