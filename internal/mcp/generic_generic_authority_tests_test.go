package mcp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

func TestGenericRegisteredActionDiscoveryAndCall(t *testing.T) {
	server := &Server{
		Service:          service.New(config.Config{GatewayID: "home_pc", StateDir: filepath.Join(t.TempDir(), "state")}),
		AuthorityContext: authority.WithPlanner(context.Background()),
	}
	sessionID := genericSession(t, server.Service, "example")
	if err := server.RegisterGenericAction(GenericAction{
		Path:        "test/echo",
		Description: "Echo one value for transport testing.",
		InputSchema: obj(map[string]any{"value": str("Value")}, "value"),
		OutputSchema: closedOutput(map[string]any{
			"value": outputString(),
		}, "value"),
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var input struct {
				Value string `json:"value"`
			}
			if err := decode(raw, &input); err != nil {
				return nil, err
			}
			return map[string]any{"value": input.Value}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}

	contract := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "schema", "arguments": map[string]any{"session": sessionID, "path": "test/echo"}},
	})))
	if contract["kind"] != "action" || contract["path"] != "test/echo" {
		t.Fatalf("unexpected action contract: %#v", contract)
	}

	call := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{"session_id": sessionID, "action": "test/echo", "input": map[string]any{"value": "ok"}}},
	})))
	if _, ok := call["action"]; ok || call["is_error"] != false {
		t.Fatalf("unexpected generic call: %#v", call)
	}
	invalid := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 4, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{"session_id": sessionID, "action": "test/echo", "input": map[string]any{"wrong": true}}},
	})))
	errorValue, _ := invalid["result"].(map[string]any)["error"].(map[string]any)
	if _, ok := invalid["action"]; ok || invalid["is_error"] != true || !strings.Contains(errorValue["message"].(string), `schema with path="test/echo"`) {
		t.Fatalf("generic validation error was not actionable: %#v", invalid)
	}

	unknown := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{"session_id": sessionID, "action": "missing/action", "input": map[string]any{}}},
	})))
	unknownError, _ := unknown["result"].(map[string]any)["error"].(map[string]any)
	if unknown["is_error"] != true || !strings.Contains(unknownError["message"].(string), "unknown action") {
		t.Fatalf("unknown action did not fail closed: %#v", unknown)
	}
}
func TestGenericLegacyReadAndMutationAuthorityReuse(t *testing.T) {
	server := &Server{
		Service:          service.New(config.Config{GatewayID: "home_pc", StateDir: filepath.Join(t.TempDir(), "state")}),
		AuthorityContext: authority.WithPlanner(context.Background()),
	}
	sessionID := genericSession(t, server.Service, "example")
	generic := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{"session_id": sessionID, "action": "system/ping", "input": map[string]any{}}},
	})))
	genericError, _ := generic["result"].(map[string]any)["error"].(map[string]any)
	if generic["is_error"] != true || !strings.Contains(genericError["message"].(string), "unknown action") {
		t.Fatalf("system/ping remained routable through generic registry: %#v", generic)
	}

	unauthorizedServer := &Server{Service: server.Service}
	var calls int
	registerAuthorityTestAction(t, unauthorizedServer, "test/policy", durableSession.RolePlanner, true, &calls)
	unauthorized := callMCP(t, unauthorizedServer, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{"session_id": sessionID, "action": "test/policy", "input": map[string]any{"value": "ok"}}},
	}))
	unauthorizedResult := genericStructured(t, unauthorized)
	unauthorizedError, _ := unauthorizedResult["result"].(map[string]any)["error"].(map[string]any)
	if unauthorizedResult["is_error"] != true || !strings.Contains(unauthorizedError["message"].(string), "AUTHORITY_UNAVAILABLE") {
		t.Fatalf("generic mutation did not reuse authority enforcement: %#v", unauthorizedResult)
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
	if _, err := (&Server{}).genericSchema(nil, json.RawMessage(`{"path":"query/run"}`)); err == nil {
		t.Fatal("retired query/run action remains publicly discoverable")
	}
}
