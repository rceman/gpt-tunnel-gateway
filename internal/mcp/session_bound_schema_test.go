package mcp

import (
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func TestSessionBoundActionSchemasDoNotExposeProjectID(t *testing.T) {
	server := &Server{Service: service.New(config.Config{GatewayID: "test_gateway"})}
	entries := server.genericActionRegistry(server.tools())
	for path, entry := range entries {
		if !entry.SessionBound {
			continue
		}
		if path == "session/bind" {
			continue
		}
		if entry.ExecutionInputSchema == nil {
			t.Fatalf("session-bound action %s has no internal execution schema", path)
		}
		if schemaContainsPropertyForTest(entry.InputSchema, "project_id") {
			t.Fatalf("session-bound action %s exposes project_id in its public schema: %#v", path, entry.InputSchema)
		}
	}
	for _, path := range []string{"project/list", "runtime/logs"} {
		if entry := entries[path]; entry.SessionBound {
			t.Fatalf("explicit/sessionless action %s was marked session-bound", path)
		}
	}
}

func schemaContainsPropertyForTest(schema map[string]any, name string) bool {
	if schema == nil {
		return false
	}
	if properties, ok := schema["properties"].(map[string]any); ok {
		if _, exists := properties[name]; exists {
			return true
		}
		for _, child := range properties {
			if nested, ok := child.(map[string]any); ok && schemaContainsPropertyForTest(nested, name) {
				return true
			}
		}
	}
	if branches, ok := schema["oneOf"].([]any); ok {
		for _, branch := range branches {
			if nested, ok := branch.(map[string]any); ok && schemaContainsPropertyForTest(nested, name) {
				return true
			}
		}
	}
	return false
}
