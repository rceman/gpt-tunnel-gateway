package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

func TestDefaultDeliveryRootResolvesPlannerAndDeliverySessionsAndLegacyDeliveryTool(t *testing.T) {
	server := newSessionTestServer(t)
	if err := server.RegisterGenericAction(GenericAction{
		Path:          "test/planner-only",
		Description:   "Planner-session-only regression action",
		InputSchema:   obj(map[string]any{}),
		OutputSchema:  closedOutput(map[string]any{"role": outputString()}, "role"),
		AuthorityRole: durableSession.RolePlanner,
		Authority:     authority.RequirePlanner,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			if err := authority.RequirePlanner(ctx); err != nil {
				return nil, err
			}
			return map[string]any{"role": durableSession.RolePlanner}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := server.RegisterGenericAction(GenericAction{
		Path:          "test/delivery-only",
		Description:   "Delivery-session-only regression action",
		InputSchema:   obj(map[string]any{}),
		OutputSchema:  closedOutput(map[string]any{"role": outputString()}, "role"),
		AuthorityRole: durableSession.RoleDelivery,
		Authority:     authority.RequireDelivery,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			if err := authority.RequireDelivery(ctx); err != nil {
				return nil, err
			}
			return map[string]any{"role": durableSession.RoleDelivery}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}

	planner := genericStructured(t, sessionCall(t, server, map[string]any{
		"action": "start", "project_id": "example", "role": "planner", "session_type": "chatgpt",
	}))
	plannerID := planner["session"].(map[string]any)["session_id"].(string)
	if !strings.HasPrefix(plannerID, "SP-") {
		t.Fatalf("planner bootstrap did not return SP session: %q", plannerID)
	}
	plannerCall := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{
			"session_id": plannerID, "action": "test/planner-only", "input": map[string]any{},
		}},
	})))
	if plannerCall["is_error"] != false || plannerCall["result"].(map[string]any)["role"] != durableSession.RolePlanner {
		t.Fatalf("planner session-authorized call failed: %#v", plannerCall)
	}

	delivery := genericStructured(t, sessionCall(t, server, map[string]any{
		"action": "start", "project_id": "example", "role": "delivery", "session_type": "chatgpt",
	}))
	deliveryID := delivery["session"].(map[string]any)["session_id"].(string)
	if !strings.HasPrefix(deliveryID, "SD-") {
		t.Fatalf("delivery bootstrap did not return SD session: %q", deliveryID)
	}
	deliveryCall := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{
			"session_id": deliveryID, "action": "test/delivery-only", "input": map[string]any{},
		}},
	})))
	if deliveryCall["is_error"] != false || deliveryCall["result"].(map[string]any)["role"] != durableSession.RoleDelivery {
		t.Fatalf("delivery session-authorized call failed: %#v", deliveryCall)
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
	missing := callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{"action": "test/project", "input": map[string]any{}}},
	}))
	if result, ok := missing["result"].(map[string]any); ok || result != nil {
		t.Fatalf("missing session did not fail schema validation: %#v", missing)
	}
	start := genericStructured(t, sessionCall(t, server, map[string]any{
		"action": "start", "project_id": "example", "role": "delivery", "session_type": "chatgpt",
	}))
	sessionID := start["session"].(map[string]any)["session_id"].(string)
	okCall := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{"session_id": sessionID, "action": "test/project", "input": map[string]any{}}},
	})))
	if okCall["is_error"] != false || string(seen) != `{"project_id":"example"}` {
		t.Fatalf("project was not inherited: result=%#v input=%s", okCall, seen)
	}
	mismatch := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{"session_id": sessionID, "action": "test/project", "input": map[string]any{"project_id": "other"}}},
	})))
	if mismatch["is_error"] != true {
		t.Fatalf("project mismatch was not rejected: %#v", mismatch)
	}
	if string(seen) != `{"project_id":"example"}` {
		t.Fatalf("mismatch reached handler: %s", seen)
	}
}

func TestSystemPingRemainsStandaloneAndGenericBatchContinues(t *testing.T) {
	server := newSessionTestServer(t)
	status := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "status", "arguments": map[string]any{}},
	})))
	if status["service"] != "gpt-tunnel-gatewayd" {
		t.Fatalf("standalone status failed: %#v", status)
	}
	root := callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "schema", "arguments": map[string]any{"path": "system"}},
	}))
	result, ok := root["result"].(map[string]any)
	if !ok || result["isError"] != true {
		t.Fatalf("system domain unexpectedly exposed: %#v", root)
	}
}
