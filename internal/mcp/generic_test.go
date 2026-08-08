package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

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
	return structured
}

func TestGenericTransportSchemasAreCompactAndApplicationIndependent(t *testing.T) {
	server := &Server{Service: service.New(config.Config{GatewayID: "home_pc"})}
	tools := server.tools()
	staticBytes := 0
	for _, name := range []string{"call", "schema", "batch"} {
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
		"params": map[string]any{"name": "schema", "arguments": map[string]any{"path": ""}},
	})))
	if root["revision"] != genericSchemaRevision || root["kind"] != "root" {
		t.Fatalf("unexpected generic schema root: %#v", root)
	}
	if len(root["domains"].([]any)) == 0 {
		t.Fatal("generic schema root has no domains")
	}
	before := make([]Tool, 0, len(tools)-3)
	after := make([]Tool, 0, len(tools))
	for name, tool := range tools {
		tool.Execute = nil
		after = append(after, tool)
		if name != "call" && name != "schema" && name != "batch" {
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

func TestGenericRegisteredActionDiscoveryCallAndBatchFailureContinuation(t *testing.T) {
	server := &Server{Service: service.New(config.Config{GatewayID: "home_pc"})}
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
		"params": map[string]any{"name": "call", "arguments": map[string]any{"action": "test/echo", "input": map[string]any{"value": "ok"}}},
	})))
	if call["action"] != "test/echo" || call["is_error"] != false {
		t.Fatalf("unexpected generic call: %#v", call)
	}
	invalid := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 4, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{"action": "test/echo", "input": map[string]any{"wrong": true}}},
	})))
	if invalid["is_error"] != true || !strings.Contains(invalid["result"].(map[string]any)["error"].(string), `schema with path="test/echo"`) {
		t.Fatalf("generic validation error was not actionable: %#v", invalid)
	}

	batch := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "tools/call",
		"params": map[string]any{"name": "batch", "arguments": map[string]any{"calls": []any{
			map[string]any{"action": "test/echo", "input": map[string]any{"value": "first"}},
			map[string]any{"action": "missing/action", "input": map[string]any{}},
			map[string]any{"action": "test/echo", "input": map[string]any{"value": "last"}},
		}}},
	})))
	results := batch["results"].([]any)
	if len(results) != 3 || results[1].(map[string]any)["is_error"] != true || results[2].(map[string]any)["is_error"] != false {
		t.Fatalf("batch did not preserve ordered continuation: %#v", batch)
	}
}

func TestGenericLegacyReadAndMutationAuthorityReuse(t *testing.T) {
	server := &Server{Service: service.New(config.Config{GatewayID: "home_pc"})}
	legacy := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "system_ping", "arguments": map[string]any{}},
	})))
	generic := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{"action": "system/ping", "input": map[string]any{}}},
	})))
	legacyValue := legacy["gateway_id"]
	genericValue := generic["result"].(map[string]any)["gateway_id"]
	if legacyValue != genericValue || generic["is_error"] != false {
		t.Fatalf("generic read path diverged from legacy handler: legacy=%#v generic=%#v", legacy, generic)
	}

	unauthorized := callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{"action": "project/workflow_policy_update", "input": map[string]any{"unknown": true}}},
	}))
	unauthorizedResult := genericStructured(t, unauthorized)
	if unauthorizedResult["is_error"] != true || !strings.Contains(unauthorizedResult["result"].(map[string]any)["error"].(string), "AUTHORITY_UNAVAILABLE") {
		t.Fatalf("generic mutation did not reuse authority enforcement: %#v", unauthorizedResult)
	}
}

func TestGenericWorkflowPolicyMutationMatchesLegacyHandler(t *testing.T) {
	s, hubRevision := newWorkflowPolicyStatusService(t)
	current, err := s.ProjectWorkflowPolicyRead(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	current.Revision++
	current.UpdatedBy = "generic-equivalence-test"
	current.UpdatedAt = time.Now().UTC()
	input := map[string]any{"policy": current, "expected_hub_revision": hubRevision}
	server := &Server{Service: s, AuthorityContext: authority.WithPlanner(context.Background())}
	legacy := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "project_workflow_policy_update", "arguments": input},
	})))
	legacyPolicy := legacy["policy"].(map[string]any)
	if legacyPolicy["revision"] != float64(current.Revision) {
		t.Fatalf("legacy policy mutation did not publish expected revision: %#v", legacy)
	}

	nextRevision, err := s.Hub.RemoteRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	current.Revision++
	current.UpdatedAt = time.Now().UTC()
	genericInput := map[string]any{"policy": current, "expected_hub_revision": nextRevision}
	generic := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{"action": "project/workflow_policy_update", "input": genericInput}},
	})))
	if generic["is_error"] != false {
		t.Fatalf("generic policy mutation failed: %#v", generic)
	}
	result := generic["result"].(map[string]any)
	if result["policy"].(map[string]any)["revision"] != float64(current.Revision) {
		t.Fatalf("generic policy mutation diverged from legacy handler: %#v", generic)
	}
}
