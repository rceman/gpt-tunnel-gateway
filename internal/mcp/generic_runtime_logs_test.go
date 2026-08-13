package mcp

import (
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func TestRuntimeLogsIsBoundedReadOnlyGenericAction(t *testing.T) {
	server := &Server{Service: service.New(config.Config{StateDir: t.TempDir()})}
	entry, ok := server.genericActionRegistry(server.tools())["runtime/logs"]
	if !ok || entry.Execute == nil {
		t.Fatal("runtime/logs action was not registered")
	}
	if !entry.Annotations.ReadOnlyHint || entry.AuthorityRole != "" {
		t.Fatalf("runtime/logs authority/annotations = %#v/%q", entry.Annotations, entry.AuthorityRole)
	}
	properties := entry.InputSchema["properties"].(map[string]any)
	if len(properties) != 5 || entry.InputSchema["additionalProperties"] != false {
		t.Fatalf("runtime/logs input schema is not bounded/closed: %#v", entry.InputSchema)
	}
	if _, ok := properties["path"]; ok {
		t.Fatal("runtime/logs exposed an arbitrary path")
	}
	if _, ok := server.publicTools()["runtime/logs"]; ok {
		t.Fatal("runtime/logs became a top-level MCP tool")
	}
}
