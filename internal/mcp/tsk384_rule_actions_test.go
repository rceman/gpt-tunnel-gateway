package mcp

import (
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func TestTSK384RuleActionSurfaceIsCanonicalAndComplete(t *testing.T) {
	server := &Server{Service: service.New(config.Config{GatewayID: "rule-test", StateDir: t.TempDir()})}
	entries := server.genericActionRegistry(server.tools())
	want := []string{"rule/create", "rule/read", "rule/update", "rule/list", "rule/query", "rule/archive", "rule/history", "rule/effective"}
	for _, path := range want {
		entry, ok := entries[path]
		if !ok {
			t.Fatalf("canonical rule action %q is not registered", path)
		}
		if entry.InputSchema == nil || entry.OutputSchema == nil || entry.ExecutionInputSchema == nil {
			t.Fatalf("rule action %q has an incomplete contract", path)
		}
		if !entry.SessionBound {
			t.Fatalf("rule action %q is not session-bound", path)
		}
	}
	for _, retired := range []string{"rules/read", "workflow/rules", "rules/create", "rules/update", "rules/archive", "rules/list", "rules/query", "rules/history", "rules/effective"} {
		if _, ok := entries[retired]; ok {
			t.Fatalf("retired plural rule action %q is still registered", retired)
		}
	}
}

func TestTSK384RuleInputSchemasAreClosed(t *testing.T) {
	server := &Server{Service: service.New(config.Config{GatewayID: "rule-test", StateDir: t.TempDir()})}
	entries := server.genericActionRegistry(server.tools())
	for path, entry := range entries {
		if len(path) < 5 || path[:5] != "rule/" {
			continue
		}
		for _, schema := range []map[string]any{entry.InputSchema, entry.ExecutionInputSchema} {
			properties, _ := schema["properties"].(map[string]any)
			if _, ok := properties["expected_revision"]; ok {
				t.Fatalf("%s exposes caller-owned CAS input", path)
			}
			if _, ok := properties["name"]; ok && path == "rule/update" {
				t.Fatalf("rule/update exposes immutable name input")
			}
		}
	}
	create := entries["rule/create"].InputSchema
	properties, _ := create["properties"].(map[string]any)
	status, _ := properties["status"].(map[string]any)
	enum, _ := status["enum"].([]any)
	if len(enum) != 1 || enum[0] != "proposed" {
		t.Fatalf("rule/create status enum is not proposed-only: %#v", enum)
	}
	relation, _ := properties["relation_type"].(map[string]any)
	relationEnum, _ := relation["enum"].([]any)
	if len(relationEnum) != 1 || relationEnum[0] != "authority" {
		t.Fatalf("rule/create relation_type enum is not authority-only: %#v", relationEnum)
	}
	for _, required := range []string{"title", "summary"} {
		found := false
		for _, field := range create["required"].([]string) {
			if field == required {
				found = true
			}
		}
		if !found {
			t.Fatalf("rule/create does not require %s", required)
		}
	}
	name, _ := properties["name"].(map[string]any)
	if pattern, _ := name["pattern"].(string); pattern != `^[a-z0-9_]+(\.[a-z0-9_]+)*$` {
		t.Fatalf("rule/create name pattern is not the closed grammar: %#v", name)
	}
	effective := entries["rule/effective"]
	if effective.Annotations.ReadOnlyHint {
		t.Fatalf("rule/effective acknowledgement side effect is wrongly annotated read-only")
	}
	if !effective.Annotations.IdempotentHint {
		t.Fatalf("rule/effective acknowledgement is not annotated idempotent")
	}
	if !effective.SessionRequired {
		t.Fatalf("rule/effective is not session-required")
	}
	output := effective.OutputSchema
	outputProperties, _ := output["properties"].(map[string]any)
	for _, field := range []string{"digest", "rules", "acknowledged"} {
		if _, ok := outputProperties[field]; !ok {
			t.Fatalf("rule/effective output is missing %s", field)
		}
	}
	for _, absent := range []string{"project", "project_id", "session", "session_id", "revision"} {
		if _, ok := outputProperties[absent]; ok {
			t.Fatalf("rule/effective output echoes %s", absent)
		}
	}
}
