package mcp

import (
	"path/filepath"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func TestSharedAuthorityReadActionsSkipHubSnapshot(t *testing.T) {
	server := &Server{Service: service.New(config.Config{
		GatewayID: "test-gateway",
		StateDir:  filepath.Join(t.TempDir(), "state"),
	})}
	entries := server.genericActionRegistry(server.tools())
	for _, path := range []string{
		"project/read", "project/status", "project/identifiers_read", "project/workflow_policy_read", "rules/read", "workflow/rules", "query/run",
		"task/list", "task/read", "adr/list", "adr/read", "train/read", "train/list",
		"agent/read", "agent/list", "agent/status", "operator/history", "watcher/status", "watcher/guide", "watcher/watch",
	} {
		entry, ok := entries[path]
		if !ok {
			t.Fatalf("shared-authority read action %q is not registered", path)
		}
		if !entry.LocalReadOnly {
			t.Fatalf("shared-authority read action %q can still acquire a Hub snapshot", path)
		}
	}
	for _, path := range []string{"project/onboard_status"} {
		if entry, ok := entries[path]; ok && entry.LocalReadOnly {
			t.Fatalf("non-local read action %q was incorrectly classified local-only", path)
		}
	}
}
