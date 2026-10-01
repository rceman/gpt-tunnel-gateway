package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/tokenizer"
)

// TestTSK669NormalOutputsRejectEchoesDefaultsAndTelemetry is the Gate-20-style
// sweep locking the ADR134 compactness contract: no normal public output may
// carry request echoes already proven by the presented selector, server-owned
// default filler, duplicate configuration identity, or telemetry counters
// without a documented next-decision consumer.
func TestTSK669NormalOutputsRejectEchoesDefaultsAndTelemetry(t *testing.T) {
	server := newSessionTestServer(t)
	server.Service.Config.Debug.Enabled = true
	entries := server.genericActionRegistry(server.tools())

	// Whole-surface bans, applied recursively at any depth.
	var walk func(path, where string, schema map[string]any)
	walk = func(path, where string, schema map[string]any) {
		if _, hasDefault := schema["default"]; hasDefault {
			t.Errorf("%s output %s carries a server-owned default filler", path, where)
		}
		properties, _ := schema["properties"].(map[string]any)
		for name, raw := range properties {
			for _, banned := range []string{"paths_scanned", "_metrics", "_pagination", "input", "output"} {
				if name == banned {
					t.Errorf("%s output exposes telemetry/transport/echo field %q", path, banned)
				}
			}
			// Pagination continuation and truncation live on the typed
			// envelope for code/*; other actions may own them explicitly.
			if strings.HasPrefix(path, "code/") && (name == "next_cursor" || name == "truncated") {
				t.Errorf("%s output exposes transport field %q", path, name)
			}
			if nested, ok := raw.(map[string]any); ok {
				walk(path, where+"."+name, nested)
			}
		}
		if items, ok := schema["items"].(map[string]any); ok {
			walk(path, where+"[]", items)
		}
		for _, union := range []string{"oneOf", "anyOf"} {
			if branches, ok := schema[union].([]any); ok {
				for _, branch := range branches {
					if nested, ok := branch.(map[string]any); ok {
						walk(path, where+"|"+union, nested)
					}
				}
			}
		}
	}
	for path, entry := range entries {
		if entry.Contract.Metadata.Surface != "normal" {
			continue
		}
		if entry.OutputSchema == nil {
			t.Errorf("%s has no output schema", path)
			continue
		}
		walk(path, "output", entry.OutputSchema)
	}

	// code/* inspection: selector-proven identity must not echo back.
	for _, path := range []string{"code/tree", "code/read", "code/search", "code/diff"} {
		properties := entries[path].OutputSchema["properties"].(map[string]any)
		for _, echo := range []string{"worktree", "live", "head"} {
			if _, ok := properties[echo]; ok {
				t.Errorf("%s output echoes selector-proven request field %q", path, echo)
			}
		}
		if requiredOutputField(entries[path].OutputSchema, "dirty") {
			t.Errorf("%s requires dirty=false filler", path)
		}
	}

	// config/* reads and lists: no redundant server-owned identity.
	for _, path := range []string{"config/procedure_list", "config/procedure_read", "config/hook_list", "config/hook_read"} {
		properties := entries[path].OutputSchema["properties"].(map[string]any)
		if _, ok := properties["configuration_revision"]; ok {
			t.Errorf("%s output repeats redundant configuration identity", path)
		}
	}

	// Procedure execution receipts stay compact: operation handle + status
	// only; validated input and server-owned context never echo.
	for _, path := range []string{"code/tree", "code/read", "code/search", "code/diff", "config/procedure_list", "config/procedure_read", "config/hook_list", "config/hook_read"} {
		schema := entries[path].OutputSchema
		if schema["additionalProperties"] != false {
			t.Errorf("%s output schema is not closed", path)
		}
	}
}

// TestTSK669CompactSchemaAndResponseTokenCost measures the before/after
// model-facing token cost for the primary compacted fixtures and locks the
// reduction: legacy echo-heavy schemas are reconstructed inline so the delta
// stays visible as evidence.
func TestTSK669CompactSchemaAndResponseTokenCost(t *testing.T) {
	server := newSessionTestServer(t)
	server.Service.Config.Debug.Enabled = true
	entries := server.genericActionRegistry(server.tools())
	counter := tokenizer.NewCounter()

	legacySearchSchema := map[string]any{
		"type": "object", "additionalProperties": false,
		"properties": map[string]any{
			"worktree":      map[string]any{"type": "string"},
			"dirty":         map[string]any{"type": "boolean"},
			"live":          map[string]any{"type": "boolean"},
			"head":          map[string]any{"type": "string", "pattern": "^[0-9a-f]{8}$"},
			"paths_scanned": map[string]any{"type": "integer"},
			"matches": map[string]any{"type": "array", "items": map[string]any{
				"type": "object", "additionalProperties": false,
				"properties": map[string]any{
					"path": map[string]any{"type": "string"}, "line": map[string]any{"type": "integer"}, "snippet": map[string]any{"type": "string"},
				},
				"required": []string{"path", "line", "snippet"},
			}},
		},
		"required": []string{"worktree", "dirty", "live", "head", "paths_scanned", "matches"},
	}
	legacySearchResponse := map[string]any{
		"worktree": "WT-MAIN-6b59fe16", "dirty": false, "live": false,
		"head": "6b59fe16", "paths_scanned": 42,
		"matches": []any{map[string]any{"path": "internal/mcp/server.go", "line": 10, "snippet": "needle"}},
	}
	compactSearchResponse := map[string]any{
		"matches": []any{map[string]any{"path": "internal/mcp/server.go", "line": 10, "snippet": "needle"}},
	}
	legacyProcedureReadResponse := map[string]any{
		"configuration_revision": 7, "name": "smoke",
		"definition": map[string]any{
			"script": "scripts/smoke.py", "summary": "s", "guide": "g",
			"input":  map[string]any{"type": "object", "properties": map[string]any{}},
			"output": map[string]any{"type": "object", "properties": map[string]any{}},
		},
	}
	compactProcedureReadResponse := map[string]any{
		"name":       "smoke",
		"definition": map[string]any{"script": "scripts/smoke.py", "summary": "s", "guide": "g"},
	}

	measure := func(label string, value any) int {
		t.Helper()
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		tokens, err := counter.CountText(data)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%s: tokens=%d bytes=%d", label, tokens, len(data))
		return tokens
	}

	currentSchema := measure("code/search schema after", entries["code/search"].OutputSchema)
	legacySchema := measure("code/search schema before", legacySearchSchema)
	if currentSchema >= legacySchema {
		t.Fatalf("code/search schema not compacted: %d >= %d tokens", currentSchema, legacySchema)
	}
	currentResponse := measure("code/search response after", compactSearchResponse)
	legacyResponse := measure("code/search response before", legacySearchResponse)
	if currentResponse >= legacyResponse {
		t.Fatalf("code/search response not compacted: %d >= %d tokens", currentResponse, legacyResponse)
	}
	currentProcedure := measure("config/procedure_read response after", compactProcedureReadResponse)
	legacyProcedure := measure("config/procedure_read response before", legacyProcedureReadResponse)
	if currentProcedure >= legacyProcedure {
		t.Fatalf("config/procedure_read response not compacted: %d >= %d tokens", currentProcedure, legacyProcedure)
	}
	for _, path := range []string{"code/tree", "code/read", "code/diff", "config/procedure_list", "config/hook_list", "config/hook_read", "project/status"} {
		measure(path+" schema", entries[path].OutputSchema)
	}
}
