package mcp

import (
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func TestTSK645CodeDiffSchemaExposesAuthoritativeHead(t *testing.T) {
	server := &Server{Service: service.NewWithDurabilityDeferredWorkers(config.Config{
		GatewayID: "tsk645-schema-test", StateDir: t.TempDir(),
	}, nil)}
	entries := server.genericActionRegistry(server.tools())
	entry, ok := entries["code/diff"]
	if !ok {
		t.Fatal("code/diff is not registered")
	}
	if entry.InputSchema["additionalProperties"] != false {
		t.Fatalf("code/diff input is not closed: %#v", entry.InputSchema)
	}
	properties := schemaProperties(entry.InputSchema)
	head, ok := properties["head"].(map[string]any)
	if !ok || head["type"] != "string" || head["pattern"] != "^[0-9a-f]{8}$" {
		t.Fatalf("code/diff head schema=%#v", properties["head"])
	}
	if _, has := head["default"]; has {
		t.Fatal("code/diff head must not carry a default")
	}
	description, _ := head["description"].(string)
	if !strings.Contains(description, "head") || !strings.Contains(description, "live") {
		t.Fatalf("code/diff head description omitted its semantics: %q", description)
	}
	base, ok := properties["base"].(map[string]any)
	if !ok || base["pattern"] != "^[0-9a-f]{8}$" {
		t.Fatalf("code/diff base schema=%#v", properties["base"])
	}
	live, ok := properties["live"].(map[string]any)
	if !ok || live["type"] != "boolean" || live["default"] != false {
		t.Fatalf("code/diff live schema=%#v", properties["live"])
	}
	required := stringList(entry.InputSchema["required"])
	if len(required) != 1 || required[0] != "worktree" {
		t.Fatalf("code/diff required=%v", required)
	}
	for _, field := range []string{"head", "base"} {
		for _, entry := range required {
			if entry == field {
				t.Fatalf("code/diff %s must remain optional", field)
			}
		}
	}
	if !strings.Contains(entry.Description, "recorded head") {
		t.Fatalf("code/diff description omitted the optional recorded head: %q", entry.Description)
	}

	output := entry.OutputSchema
	if output["additionalProperties"] != false {
		t.Fatalf("code/diff output is not closed: %#v", output)
	}
	outputProperties := schemaProperties(output)
	for _, field := range []string{"worktree", "dirty", "live", "head", "base", "paths", "diff"} {
		if _, has := outputProperties[field]; !has {
			t.Fatalf("code/diff output lost field %q: %#v", field, outputProperties)
		}
	}
	for _, field := range []string{"_pagination", "next_cursor", "has_more"} {
		if _, has := outputProperties[field]; has {
			t.Fatalf("code/diff result exposes continuation field %q: %#v", field, outputProperties)
		}
	}
	if len(outputProperties) != 7 {
		t.Fatalf("code/diff output envelope changed: %#v", outputProperties)
	}
	if outputProperties["base"].(map[string]any)["pattern"] != "^[0-9a-f]{8}$" {
		t.Fatalf("code/diff output base schema=%#v", outputProperties["base"])
	}
}

func tsk645DiffError(t *testing.T, harness publicCodeCallHarness, input map[string]any) string {
	t.Helper()
	response := harness.client.request(t, "tools/call", map[string]any{
		"name": "call", "arguments": map[string]any{
			"session": harness.sessionID, "action": "code/diff", "input": input,
		},
	})
	structured := genericStructured(t, response)
	if structured["is_error"] != true {
		t.Fatalf("code/diff accepted unauthorized input: %#v", structured)
	}
	result, _ := structured["result"].(map[string]any)
	errorValue, _ := result["error"].(map[string]any)
	message, _ := errorValue["message"].(string)
	return message
}

func TestTSK645PublicCodeDiffHeadFailsClosedE2E(t *testing.T) {
	fixture := newPublicCodeE2EFixture(t)
	harness := newPublicCodeCallHarness(t, fixture)

	for name, input := range map[string]map[string]any{
		"live with head":  {"worktree": fixture.mainSelector, "head": fixture.currentHead[:8], "live": true},
		"non-task head":   {"worktree": fixture.mainSelector, "head": fixture.currentHead[:8]},
		"unknown head":    {"worktree": fixture.mainSelector, "head": "00000000"},
		"non-sha8 head":   {"worktree": fixture.mainSelector, "head": strings.ToUpper(fixture.currentHead[:8])},
		"non-sha8 length": {"worktree": fixture.mainSelector, "head": fixture.currentHead[:9]},
	} {
		message := tsk645DiffError(t, harness, input)
		if message == "" {
			t.Fatalf("%s produced an empty error", name)
		}
	}
}
