package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func TestRemovedRunToolsAreNotRegistered(t *testing.T) {
	srv := &Server{Service: service.New(config.Config{GatewayID: "home_pc"})}
	for _, name := range []string{"run_list", "run_read", "run_status", "run_report", "run_review_snapshot", "run_agent_tail", "run_resume", "run_sweep", "run_cancel", "run_cancel_acknowledge_no_mutation"} {
		if _, ok := srv.tools()[name]; ok {
			t.Fatalf("obsolete run tool is still registered: %s", name)
		}
	}
}

func TestToolCallRejectsInvalidAndOversizedMeta(t *testing.T) {
	srv := &Server{Service: service.New(config.Config{GatewayID: "home_pc"})}
	for _, meta := range []string{`null`, `[]`, `"value"`} {
		body := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"status","arguments":{},"_meta":` + meta + `}}`)
		response := callMCP(t, srv, body)
		errorObject, ok := response["error"].(map[string]any)
		if !ok || errorObject["code"] != float64(-32602) {
			t.Fatalf("invalid _meta %s was accepted: %#v", meta, response)
		}
	}
	large := strings.Repeat("x", maxToolCallMetaBytes)
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "status", "arguments": map[string]any{}, "_meta": map[string]any{"value": large}},
	})
	if err != nil {
		t.Fatal(err)
	}
	response := callMCP(t, srv, body)
	errorObject, ok := response["error"].(map[string]any)
	if !ok || errorObject["code"] != float64(-32602) {
		t.Fatalf("oversized _meta was accepted: %#v", response)
	}
}

func TestEveryToolDeclaresOutputSchemaAndExplicitAnnotations(t *testing.T) {
	srv := &Server{Service: service.New(config.Config{})}
	tools := srv.publicTools()
	if len(tools) != len(canonicalToolManifest) {
		t.Fatalf("tool count=%d want manifest count %d", len(tools), len(canonicalToolManifest))
	}
	for name, tool := range tools {
		if tool.OutputSchema == nil || tool.OutputSchema["type"] != "object" {
			t.Errorf("%s has invalid output schema: %#v", name, tool.OutputSchema)
		}
		if _, ok := tool.InputSchema["additionalProperties"]; !ok {
			t.Errorf("%s input schema is not explicit", name)
		}
		if _, ok := toolOutputSchemas[name]; !ok {
			t.Errorf("%s missing output schema registry entry", name)
		}
		if _, ok := toolAnnotations[name]; !ok {
			t.Errorf("%s missing annotation registry entry", name)
		}
	}
	if _, ok := tools["call"]; !ok {
		t.Fatal("canonical call tool is missing")
	}
}

func TestToolAnnotationsMatchActualSideEffects(t *testing.T) {
	srv := &Server{Service: service.New(config.Config{})}
	tools := srv.publicTools()
	assert := func(name string, want ToolAnnotations) {
		t.Helper()
		if got := tools[name].Annotations; got != want {
			t.Errorf("%s annotations=%+v want %+v", name, got, want)
		}
	}
	assert("call", additiveExternalAnnotations())
	assert("schema", readOnlyAnnotations())
}
