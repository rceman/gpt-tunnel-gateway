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

func TestGenericRegisteredActionDiscoveryCallAndBatchFailureContinuation(t *testing.T) {
	server := &Server{
		Service:          service.New(config.Config{GatewayID: "home_pc", StateDir: filepath.Join(t.TempDir(), "state")}),
		AuthorityContext: authority.WithDelivery(context.Background()),
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
		"params": map[string]any{"name": "schema", "arguments": map[string]any{"path": "test/echo"}},
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
	if _, ok := invalid["action"]; ok || invalid["is_error"] != true || !strings.Contains(invalid["result"].(map[string]any)["error"].(string), `schema with path="test/echo"`) {
		t.Fatalf("generic validation error was not actionable: %#v", invalid)
	}

	batch := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "tools/call",
		"params": map[string]any{"name": "batch", "arguments": map[string]any{"session_id": sessionID, "calls": []any{
			map[string]any{"action": "test/echo", "input": map[string]any{"value": "first"}},
			map[string]any{"action": "missing/action", "input": map[string]any{}},
			map[string]any{"action": "test/echo", "input": map[string]any{"value": "last"}},
		}}},
	})))
	results := batch["results"].([]any)
	if len(results) != 3 || results[0].(map[string]any)["action"] != "test/echo" || results[1].(map[string]any)["action"] != "missing/action" || results[1].(map[string]any)["is_error"] != true || results[2].(map[string]any)["action"] != "test/echo" || results[2].(map[string]any)["is_error"] != false {
		t.Fatalf("batch did not preserve ordered continuation: %#v", batch)
	}
}
func TestGenericLegacyReadAndMutationAuthorityReuse(t *testing.T) {
	server := &Server{
		Service:          service.New(config.Config{GatewayID: "home_pc", StateDir: filepath.Join(t.TempDir(), "state")}),
		AuthorityContext: authority.WithDelivery(context.Background()),
	}
	sessionID := genericSession(t, server.Service, "example")
	generic := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{"session_id": sessionID, "action": "system/ping", "input": map[string]any{}}},
	})))
	if generic["is_error"] != true || !strings.Contains(generic["result"].(map[string]any)["error"].(string), "unknown action") {
		t.Fatalf("system/ping remained routable through generic registry: %#v", generic)
	}

	unauthorizedServer := &Server{Service: server.Service}
	var calls int
	registerAuthorityTestAction(t, unauthorizedServer, "test/policy", durableSession.RoleDelivery, true, &calls)
	unauthorized := callMCP(t, unauthorizedServer, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{"session_id": sessionID, "action": "test/policy", "input": map[string]any{"value": "ok"}}},
	}))
	unauthorizedResult := genericStructured(t, unauthorized)
	if unauthorizedResult["is_error"] != true || !strings.Contains(unauthorizedResult["result"].(map[string]any)["error"].(string), "AUTHORITY_UNAVAILABLE") {
		t.Fatalf("generic mutation did not reuse authority enforcement: %#v", unauthorizedResult)
	}
}
func TestGenericTransportEnvelopeAndActionPathContracts(t *testing.T) {
	callSchema := genericCallOutputSchema()
	callProperties := callSchema["properties"].(map[string]any)
	if len(callProperties) != 2 {
		t.Fatalf("single-call schema has unexpected properties: %#v", callProperties)
	}
	if _, ok := callProperties["action"]; ok {
		t.Fatal("single-call schema still exposes action")
	}
	batchSchema := genericBatchOutputSchema()
	items := batchSchema["properties"].(map[string]any)["results"].(map[string]any)["items"].(map[string]any)
	itemProperties := items["properties"].(map[string]any)
	if len(itemProperties) != 3 {
		t.Fatalf("batch item schema has unexpected properties: %#v", itemProperties)
	}
	if _, ok := itemProperties["action"]; !ok {
		t.Fatal("batch item schema lost action correlation")
	}

	for _, test := range []struct {
		path string
		want bool
	}{
		{path: "task/read", want: true},
		{path: "task.create", want: false},
		{path: "task/read/extra", want: false},
		{path: "query/run", want: true},
	} {
		if _, _, ok := genericActionParts(test.path); ok != test.want {
			t.Fatalf("genericActionParts(%q) ok=%v, want %v", test.path, ok, test.want)
		}
	}
}
