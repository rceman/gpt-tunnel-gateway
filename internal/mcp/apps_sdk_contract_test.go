package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func TestRunReviewSnapshotToolCallUsesOnlyRunIDAndReturnsToolErrorForUnknownRun(t *testing.T) {
	srv := &Server{Service: service.New(config.Config{GatewayID: "home_pc"})}
	response := callMCP(t, srv, []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"run_review_snapshot","arguments":{"run_id":"missing"}}}`))
	result, ok := response["result"].(map[string]any)
	if !ok || result["isError"] != true {
		t.Fatalf("unexpected snapshot tool result: %#v", response)
	}
	if _, ok := result["structuredContent"]; ok {
		t.Fatalf("tool error exposed structuredContent: %#v", result)
	}
	tool := srv.tools()["run_review_snapshot"]
	if got := tool.InputSchema["required"].([]string); len(got) != 1 || got[0] != "run_id" {
		t.Fatalf("unexpected input contract: %#v", tool.InputSchema)
	}
}

func TestToolCallRejectsInvalidAndOversizedMeta(t *testing.T) {
	srv := &Server{Service: service.New(config.Config{GatewayID: "home_pc"})}
	for _, meta := range []string{`null`, `[]`, `"value"`} {
		body := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"system_ping","arguments":{},"_meta":` + meta + `}}`)
		response := callMCP(t, srv, body)
		errorObject, ok := response["error"].(map[string]any)
		if !ok || errorObject["code"] != float64(-32602) {
			t.Fatalf("invalid _meta %s was accepted: %#v", meta, response)
		}
	}
	large := strings.Repeat("x", maxToolCallMetaBytes)
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "system_ping", "arguments": map[string]any{}, "_meta": map[string]any{"value": large}},
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
	tools := srv.tools()
	if len(tools) != len(canonicalToolManifest) {
		t.Fatalf("tool count=%d want manifest count %d", len(tools), len(canonicalToolManifest))
	}
	if len(toolOutputSchemas) != len(tools) || len(toolAnnotations) != len(tools) {
		t.Fatalf("contract coverage mismatch: tools=%d outputs=%d annotations=%d", len(tools), len(toolOutputSchemas), len(toolAnnotations))
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
	properties := tools["plan_update"].InputSchema["properties"].(map[string]any)
	if _, ok := properties["body"]; ok {
		t.Fatal("plan_update advertises obsolete body input")
	}
}

func TestToolAnnotationsMatchActualSideEffects(t *testing.T) {
	srv := &Server{Service: service.New(config.Config{})}
	tools := srv.tools()
	assert := func(name string, want ToolAnnotations) {
		t.Helper()
		if got := tools[name].Annotations; got != want {
			t.Errorf("%s annotations=%+v want %+v", name, got, want)
		}
	}
	assert("system_ping", readOnlyAnnotations())
	assert("git_read_file", readOnlyAnnotations())
	assert("project_identifiers_read", readOnlyAnnotations())
	assert("project_identifiers_adopt", additiveExternalAnnotations())
	assert("project_workflow_policy_read", readOnlyAnnotations())
	assert("project_workflow_policy_adopt", additiveExternalAnnotations())
	assert("project_workflow_policy_update", additiveExternalAnnotations())
	for _, name := range []string{"project_workflow_policy_adopt", "project_workflow_policy_update"} {
		tool := tools[name]
		properties := tool.InputSchema["properties"].(map[string]any)
		if _, ok := properties["authorization_context"]; ok {
			t.Fatalf("%s exposes caller-controlled authorization_context", name)
		}
	}
	assert("run_review_snapshot", ToolAnnotations{
		ReadOnlyHint:    true,
		DestructiveHint: false,
		IdempotentHint:  true,
		OpenWorldHint:   true,
	})
	assert("adr_create", additiveExternalAnnotations())
	assert("task_create", additiveExternalAnnotations())
	assert("plan_cutover", destructiveExternalAnnotations())
	assert("plan_update", destructiveExternalAnnotations())
	assert("plan_section_create", additiveExternalAnnotations())
	assert("plan_section_update", destructiveExternalAnnotations())
	assert("plan_section_delete", destructiveExternalAnnotations())
	assert("plan_render", readOnlyAnnotations())
	assert("task_dispatch", destructiveExternalAnnotations())
	assert("run_cancel", destructiveExternalAnnotations())
	assert("run_cancel_acknowledge_no_mutation", destructiveExternalAnnotations())
	assert("git_refresh", ToolAnnotations{
		ReadOnlyHint:    false,
		DestructiveHint: false,
		IdempotentHint:  true,
		OpenWorldHint:   true,
	})
	supersedeTask := tools["task_supersede"].InputSchema["properties"].(map[string]any)["task"].(map[string]any)
	if supersedeTask["additionalProperties"] != false {
		t.Fatal("task_supersede task input is not closed")
	}
	supersedeProperties := supersedeTask["properties"].(map[string]any)
	if _, ok := supersedeProperties["operation_class"]; !ok {
		t.Fatal("task_supersede does not advertise operation_class")
	}
}
