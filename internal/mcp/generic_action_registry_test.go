package mcp

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func TestFrozenRegistryUsesCanonicalCursorTailAndTypedCompatibilityKeepsSkip(t *testing.T) {
	server := &Server{Service: service.New(config.Config{GatewayID: "home_pc"})}
	tools := server.tools()

	typed := tools["agent_tail"].InputSchema["properties"].(map[string]any)
	if _, ok := typed["skip"]; !ok {
		t.Fatal("typed agent_tail lost legacy skip compatibility")
	}
	entries := server.genericActionRegistry(tools)
	entry, ok := entries["agent/tail"]
	if !ok {
		t.Fatal("canonical agent/tail action is not registered")
	}
	properties := entry.InputSchema["properties"].(map[string]any)
	if _, ok := properties["skip"]; ok {
		t.Fatal("canonical agent/tail still advertises legacy skip")
	}
	if _, ok := properties["cursor"]; !ok {
		t.Fatal("canonical agent/tail does not advertise cursor continuation")
	}
}

func TestFrozenRegistrySchemaCallAndBatchShareCanonicalContracts(t *testing.T) {
	server := &Server{
		Service:          service.New(config.Config{GatewayID: "home_pc"}),
		AuthorityContext: authority.WithDelivery(context.Background()),
	}
	tools := server.tools()
	entries := server.genericActionRegistry(tools)
	for _, path := range []string{"project/list", "task/list", "run/list", "adr/list", "task/revision_list", "plan/history", "delivery/handoff_list", "planner/report_list", "operator/history", "git/refs", "git/log", "git/tree", "agent/tail"} {
		entry, ok := entries[path]
		if !ok {
			t.Fatalf("canonical registry missing %s", path)
		}
		if entry.InputSchema == nil || entry.OutputSchema == nil || entry.Execute == nil {
			t.Fatalf("canonical registry entry incomplete for %s", path)
		}
	}

	contract := genericActionContract(entries["task/list"])
	if !reflect.DeepEqual(contract["input_schema"], entries["task/list"].InputSchema) || !reflect.DeepEqual(contract["output_schema"], entries["task/list"].OutputSchema) {
		t.Fatal("schema contract diverges from canonical task/list entry")
	}
	encoded, err := json.Marshal(entries["agent/tail"].InputSchema)
	if err != nil || len(encoded) == 0 {
		t.Fatalf("canonical agent/tail schema is not serializable: %v", err)
	}
}

func TestCanonicalAgentTailRejectsLegacySkipBeforeServiceExecution(t *testing.T) {
	server := &Server{
		Service:          service.New(config.Config{GatewayID: "home_pc", StateDir: t.TempDir()}),
		AuthorityContext: authority.WithDelivery(context.Background()),
	}
	sessionID := genericSession(t, server.Service, "example")
	contract := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 0, "method": "tools/call",
		"params": map[string]any{"name": "schema", "arguments": map[string]any{"path": "agent/tail"}},
	})))
	contractProperties := contract["contract"].(map[string]any)["input_schema"].(map[string]any)["properties"].(map[string]any)
	if _, ok := contractProperties["skip"]; ok {
		t.Fatalf("transport schema still advertises skip: %#v", contract)
	}
	result := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{
			"name": "call",
			"arguments": map[string]any{
				"session_id": sessionID,
				"action":     "agent/tail",
				"input":      map[string]any{"project_id": "example", "skip": 1},
			},
		},
	})))
	if result["is_error"] != true {
		t.Fatalf("canonical agent/tail accepted legacy skip: %#v", result)
	}
	if !strings.Contains(result["result"].(map[string]any)["error"].(string), `unknown argument "skip"`) {
		t.Fatalf("unexpected canonical skip rejection: %#v", result)
	}
}
