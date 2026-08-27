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

func TestCodeDiffUsesLinePaginationInsteadOfPublicByteLimit(t *testing.T) {
	server := &Server{Service: service.NewWithDurabilityDeferredWorkers(config.Config{
		GatewayID: "code-diff-contract-test", StateDir: t.TempDir(),
	}, nil)}
	entry := server.genericActionRegistry(server.tools())["code/diff"]
	properties := entry.InputSchema["properties"].(map[string]any)
	if _, ok := properties["max_bytes"]; ok {
		t.Fatal("code/diff still exposes public max_bytes")
	}
	if _, ok := properties["line_count"]; !ok {
		t.Fatal("code/diff omits public line_count")
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

func TestPublicMCPV1ManifestRemainsExactlyFiveTools(t *testing.T) {
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
	for _, name := range []string{"status", "session_start", "schema", "call", "batch"} {
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
