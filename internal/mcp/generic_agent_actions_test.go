package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func TestAgentActionsAreGenericDiscoverableAndInvokable(t *testing.T) {
	server := &Server{Service: service.New(config.Config{GatewayID: "home_pc"})}
	entries := server.genericActionRegistry(server.tools())
	for _, path := range []string{"agent/register", "agent/update", "agent/disable", "agent/read", "agent/list", "agent/status"} {
		entry, ok := entries[path]
		if !ok {
			t.Fatalf("missing agent generic action %s", path)
		}
		if entry.InputSchema == nil || entry.OutputSchema == nil || entry.Execute == nil {
			t.Fatalf("incomplete agent generic action %s", path)
		}
	}
	if _, ok := server.tools()["agent_register"]; ok {
		t.Fatal("agent registry action was incorrectly added as a typed tool")
	}
	entry := entries["agent/read"]
	if _, err := entry.Execute(authority.WithDelivery(context.Background()), json.RawMessage(`{"project_id":"missing","agent_id":"coder"}`)); err == nil {
		t.Fatal("agent/read unexpectedly succeeded for an unknown project")
	}
}
