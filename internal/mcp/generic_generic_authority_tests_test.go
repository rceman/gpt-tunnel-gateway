package mcp

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func TestGenericRegisteredActionDiscoveryAndCall(t *testing.T) {
	server := &Server{
		Service: func() *service.Service {
			s, _ := mcpServiceWithSQLite(t, config.Config{GatewayID: "HOM", StateDir: filepath.Join(t.TempDir(), "state")})
			return s
		}(),
		AuthorityContext: authority.WithPlanner(context.Background()),
	}
	sessionID := genericSession(t, server.Service, "example")
	if err := server.RegisterGenericAction(GenericAction{Path: "test/echo"}); err == nil {
		t.Fatal("unfrozen action registration was accepted")
	}

	contract := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "schema", "arguments": map[string]any{"session": sessionID, "path": "session/info"}},
	})))
	if contract["kind"] != "action" || contract["path"] != "session/info" {
		t.Fatalf("unexpected compiled action contract: %#v", contract)
	}
	inputSchema := contract["contract"].(map[string]any)["input_schema"].(map[string]any)
	if len(schemaProperties(inputSchema)) != 0 {
		t.Fatalf("session/info discovery did not use its compiled empty input schema: %#v", inputSchema)
	}

	call := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{"session": sessionID, "action": "session/info", "input": map[string]any{}}},
	})))
	if call["is_error"] != false {
		t.Fatalf("compiled action call failed: %#v", call)
	}
	if _, ok := call["result"].(map[string]any)["session"]; !ok {
		t.Fatalf("session/info omitted its contract result: %#v", call)
	}

	invalid := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{"session": sessionID, "action": "operation/read", "input": map[string]any{"operation_id": "EXM-OPR1"}}},
	})))
	invalidError, _ := invalid["result"].(map[string]any)["error"].(map[string]any)
	if invalid["is_error"] != true || !strings.Contains(invalidError["message"].(string), `schema with path="operation/read"`) {
		t.Fatalf("compiled runtime validation did not reject the compatibility alias: %#v", invalid)
	}

	unknown := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 4, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{"session": sessionID, "action": "missing/action", "input": map[string]any{}}},
	})))
	unknownError, _ := unknown["result"].(map[string]any)["error"].(map[string]any)
	if unknown["is_error"] != true || !strings.Contains(unknownError["message"].(string), "unknown action") {
		t.Fatalf("unknown action did not fail closed: %#v", unknown)
	}
}

func TestGenericLegacyReadAndMutationAuthorityReuse(t *testing.T) {
	server := &Server{
		Service: func() *service.Service {
			s, _ := mcpServiceWithSQLite(t, config.Config{GatewayID: "HOM", StateDir: filepath.Join(t.TempDir(), "state")})
			return s
		}(),
		AuthorityContext: authority.WithPlanner(context.Background()),
	}
	sessionID := genericSession(t, server.Service, "example")
	generic := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{"session": sessionID, "action": "system/ping", "input": map[string]any{}}},
	})))
	genericError, _ := generic["result"].(map[string]any)["error"].(map[string]any)
	if generic["is_error"] != true || !strings.Contains(genericError["message"].(string), "unknown action") {
		t.Fatalf("system/ping remained routable through generic registry: %#v", generic)
	}

	unauthorizedServer := &Server{Service: server.Service}
	unauthorized := genericStructured(t, callMCP(t, unauthorizedServer, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{"session": sessionID, "action": "task/create", "input": map[string]any{}}},
	})))
	unauthorizedError, _ := unauthorized["result"].(map[string]any)["error"].(map[string]any)
	if unauthorized["is_error"] != true || !strings.Contains(unauthorizedError["message"].(string), "AUTHORITY_UNAVAILABLE") {
		t.Fatalf("generic mutation did not reuse authority enforcement: %#v", unauthorized)
	}
}

func TestGenericTransportEnvelopeAndActionPathContracts(t *testing.T) {
	callSchema := genericCallOutputSchema()
	if callSchema["type"] != "object" {
		t.Fatalf("single-call schema has unexpected type: %#v", callSchema)
	}
	if _, ok := callSchema["oneOf"]; !ok {
		t.Fatalf("single-call schema is missing success/failure alternatives: %#v", callSchema)
	}
	for _, test := range []struct {
		path string
		want bool
	}{
		{path: "task/read", want: true},
		{path: "task.create", want: false},
		{path: "task/read/extra", want: false},
	} {
		if _, _, ok := genericActionParts(test.path); ok != test.want {
			t.Fatalf("genericActionParts(%q) ok=%v, want %v", test.path, ok, test.want)
		}
	}
	server := newSessionTestServer(t)
	if _, ok := server.genericActionRegistry(server.tools())["query/run"]; ok {
		t.Fatal("retired query/run action remains publicly discoverable")
	}
}
