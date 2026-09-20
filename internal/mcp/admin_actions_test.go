package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
)

func TestAdminOnboardActionIsFrozenAndUnregistered(t *testing.T) {
	svc, db := mcpServiceWithSQLite(t, config.Config{GatewayID: "HOM", StateDir: t.TempDir()})
	defer db.Close()
	server := &Server{Service: svc}
	entries := server.genericActionRegistry(server.tools())
	if _, ok := entries["admin/project/onboard"]; ok {
		t.Fatal("frozen admin/project/onboard action is still registered")
	}
	for _, tool := range server.tools() {
		if strings.Contains(tool.Description, "admin/project/onboard") {
			t.Fatal("frozen admin onboarding action is advertised")
		}
	}
}

func TestAdminSchemaHidesFrozenOnboardDomain(t *testing.T) {
	svc, db := mcpServiceWithSQLite(t, config.Config{GatewayID: "HOM", StateDir: t.TempDir()})
	defer db.Close()
	server := &Server{Service: svc}
	if _, err := server.genericSchemaPublic(context.Background(), server.tools(), mustJSON(t, map[string]any{"path": "admin"})); err == nil || !strings.Contains(err.Error(), "schema path") {
		t.Fatalf("frozen admin schema result=%v", err)
	}
}
