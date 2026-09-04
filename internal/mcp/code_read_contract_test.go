package mcp

import (
	"reflect"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/mcpmanifest"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func TestCodeActionsAreSessionBoundAndProjectIsNotCallerSelectable(t *testing.T) {
	server := &Server{Service: service.NewWithDurabilityDeferredWorkers(config.Config{
		GatewayID: "code-contract-test", StateDir: t.TempDir(),
	}, nil)}
	entries := server.genericActionRegistry(server.tools())
	for _, path := range []string{"code/read", "code/search", "code/diff"} {
		entry, ok := entries[path]
		if !ok {
			t.Fatalf("missing generic code action %q", path)
		}
		if !entry.SessionBound || !entry.SessionRequired || !entry.LocalReadOnly {
			t.Fatalf("code action %q is not session-bound/local-read-only: %#v", path, entry)
		}
		properties := entry.InputSchema["properties"].(map[string]any)
		if _, ok := properties["project_id"]; ok {
			t.Fatalf("code action %q exposes project_id", path)
		}
		if entry.InputSchema["additionalProperties"] != false {
			t.Fatalf("code action %q input is not closed", path)
		}
	}
}

func TestCodeOutputSchemasRequireFullHead(t *testing.T) {
	server := &Server{Service: service.NewWithDurabilityDeferredWorkers(config.Config{
		GatewayID: "code-output-contract-test", StateDir: t.TempDir(),
	}, nil)}
	entries := server.genericActionRegistry(server.tools())
	for _, path := range []string{"code/tree", "code/read", "code/search", "code/diff"} {
		entry, ok := entries[path]
		if !ok {
			t.Fatalf("missing generic code action %q", path)
		}
		properties := entry.OutputSchema["properties"].(map[string]any)
		if _, ok := properties["head"]; !ok {
			t.Fatalf("code action %q output omits full head: %#v", path, entry.OutputSchema)
		}
		if !requiredOutputField(entry.OutputSchema, "head") {
			t.Fatalf("code action %q output does not require full head: %#v", path, entry.OutputSchema)
		}
	}
	worktree := entries["code/worktree"].OutputSchema["properties"].(map[string]any)
	item := worktree["items"].(map[string]any)["items"].(map[string]any)
	if _, ok := item["properties"].(map[string]any)["head"]; !ok {
		t.Fatalf("code/worktree item omits full head: %#v", item)
	}
	if !requiredOutputField(item, "head") {
		t.Fatalf("code/worktree item does not require full head: %#v", item)
	}
}

func TestCodeActionsUseTokenBudgetPaginationWithoutPublicLineOrByteControls(t *testing.T) {
	server := &Server{Service: service.NewWithDurabilityDeferredWorkers(config.Config{
		GatewayID: "code-diff-contract-test", StateDir: t.TempDir(),
	}, nil)}
	entries := server.genericActionRegistry(server.tools())
	for _, path := range []string{"code/worktree", "code/tree", "code/search", "code/diff"} {
		properties := entries[path].InputSchema["properties"].(map[string]any)
		for _, field := range []string{"limit", "max_bytes"} {
			if _, ok := properties[field]; ok {
				t.Fatalf("%s still exposes public pagination control %q", path, field)
			}
		}
	}
	readProperties := entries["code/read"].InputSchema["properties"].(map[string]any)
	lineCount, ok := readProperties["line_count"].(map[string]any)
	if !ok || lineCount["type"] != "integer" || lineCount["minimum"] != 1 {
		t.Fatalf("code/read does not expose an optional positive line_count range: %#v", readProperties)
	}
	if required, ok := entries["code/read"].InputSchema["required"].([]string); ok {
		for _, field := range required {
			if field == "line_count" {
				t.Fatal("code/read line_count must remain optional")
			}
		}
	}
	for _, path := range []string{"code/worktree", "code/tree", "code/read", "code/search", "code/diff"} {
		properties := entries[path].OutputSchema["properties"].(map[string]any)
		pagination, ok := properties["_pagination"].(map[string]any)
		if !ok || pagination["type"] != "object" {
			t.Fatalf("%s does not expose _pagination output metadata: %#v", path, entries[path].OutputSchema)
		}
		paginationProperties, ok := pagination["properties"].(map[string]any)
		if !ok || len(paginationProperties) != 1 {
			t.Fatalf("%s _pagination has unexpected fields: %#v", path, pagination)
		}
		if _, ok := paginationProperties["next_cursor"]; !ok {
			t.Fatalf("%s _pagination omits next_cursor: %#v", path, pagination)
		}
		if _, ok := pagination["required"]; ok {
			t.Fatalf("%s _pagination is required internally: %#v", path, pagination)
		}
		if requiredOutputField(entries[path].OutputSchema, "_pagination") {
			t.Fatalf("%s _pagination is required on terminal output: %#v", path, entries[path].OutputSchema)
		}
		if _, ok := properties["next_cursor"]; ok {
			t.Fatalf("%s exposes legacy top-level next_cursor", path)
		}
		if _, ok := properties["truncated"]; ok {
			t.Fatalf("%s exposes legacy top-level truncated", path)
		}
	}
}

func requiredOutputField(schema map[string]any, want string) bool {
	required, ok := schema["required"].([]string)
	if !ok {
		return false
	}
	for _, field := range required {
		if field == want {
			return true
		}
	}
	return false
}

func TestPublicMCPV1ManifestRemainsExactlySixTools(t *testing.T) {
	server := &Server{Service: service.NewWithDurabilityDeferredWorkers(config.Config{
		GatewayID: "v1-contract-test", StateDir: t.TempDir(),
	}, nil)}
	public := server.publicTools()
	want := mcpmanifest.CanonicalToolNames()
	got := make([]string, 0, len(public))
	for name := range public {
		got = append(got, name)
	}
	if !reflect.DeepEqual(sortedStrings(got), sortedStrings(want)) {
		t.Fatalf("public MCP tools changed: got=%v want=%v", got, want)
	}
	if _, ok := public["session_update"]; ok {
		t.Fatal("session_update leaked into public MCP V1")
	}
	for _, name := range []string{"status", "guide", "projects", "session_start", "schema", "call"} {
		if public[name].Execute == nil {
			t.Fatalf("public MCP V1 tool %q has no dispatch handler", name)
		}
	}
}

func sortedStrings(values []string) []string {
	result := append([]string(nil), values...)
	// The canonical manifest is already stable; sorting the copy keeps the
	// comparison independent of map iteration order without changing it.
	for i := 0; i < len(result); i++ {
		for j := i + 1; j < len(result); j++ {
			if result[j] < result[i] {
				result[i], result[j] = result[j], result[i]
			}
		}
	}
	return result
}
