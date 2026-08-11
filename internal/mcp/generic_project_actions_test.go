package mcp

import (
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func TestProjectConfigurationUpdateIsGenericAndStrictlyDiscoverable(t *testing.T) {
	server := &Server{Service: service.New(config.Config{GatewayID: "home_pc"})}
	entries := server.genericActionRegistry(server.tools())
	entry, ok := entries["project/update"]
	if !ok || entry.Execute == nil || entry.InputSchema == nil || entry.OutputSchema == nil {
		t.Fatalf("project/update generic action is incomplete: %#v", entry)
	}
	if _, ok := server.tools()["project_update"]; ok {
		t.Fatal("project configuration update was incorrectly added as a typed tool")
	}
	properties := entry.InputSchema["properties"].(map[string]any)
	if _, ok := properties["expected_revision"]; !ok {
		t.Fatal("project/update omits expected revision")
	}
	patch := properties["patch"].(map[string]any)
	if patch["additionalProperties"] != false {
		t.Fatal("project/update patch is not closed")
	}
}
