package mcp

import (
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func TestWatcherActionsAreGenericAndDiscoverable(t *testing.T) {
	server := &Server{Service: service.New(config.Config{GatewayID: "home_pc"})}
	entries := server.genericActionRegistry(server.tools())
	for _, path := range []string{"watcher/watch", "watcher/nudge", "watcher/status", "watcher/guide", "watcher/guide_update"} {
		entry, ok := entries[path]
		if !ok {
			t.Fatalf("missing watcher generic action %s", path)
		}
		if entry.InputSchema == nil || entry.OutputSchema == nil || entry.Execute == nil {
			t.Fatalf("incomplete watcher action %s", path)
		}
	}
	if _, ok := server.tools()["watcher_watch"]; ok {
		t.Fatal("watcher action was incorrectly added to typed MCP tools")
	}
}
