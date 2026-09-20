package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

func TestAdminOnboardActionIsClosedAndAdminOnly(t *testing.T) {
	svc, db := mcpServiceWithSQLite(t, config.Config{GatewayID: "HOM", StateDir: t.TempDir()})
	server := &Server{Service: svc}
	entries := server.genericActionRegistry(server.tools())
	entry, ok := entries["admin/project/onboard"]
	if !ok || entry.AuthorityRole != durableSession.RoleAdmin || !entry.SessionRequired || entry.SessionBound {
		t.Fatalf("admin action registration=%#v found=%v", entry, ok)
	}
	input := entry.InputSchema["properties"].(map[string]any)
	if len(input) != 3 {
		t.Fatalf("input properties=%#v", input)
	}
	if _, ok := input["local_path"]; ok {
		t.Fatal("admin action exposes local_path")
	}
	output := entry.OutputSchema["properties"].(map[string]any)
	if len(output) != 3 {
		t.Fatalf("output properties=%#v", output)
	}
	if _, ok := output["root"]; ok {
		t.Fatal("admin action exposes host root")
	}
	store := durableSession.NewStoreWithGateway(db, "HOM")
	admin, err := store.CreateAdmin(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.genericCallWithEntries(context.Background(), entries, mustJSON(t, map[string]any{
		"session": admin.ID, "action": "project/status", "input": map[string]any{"project_id": "example"},
	})); err == nil || !strings.Contains(err.Error(), "Admin Session") {
		t.Fatalf("Admin Session reached non-admin action: %v", err)
	}
	workflow, err := store.Create(durableSession.CreateInput{ProjectID: "example", ProjectCode: "EXM", Role: durableSession.RolePlanner, SessionType: durableSession.SessionTypeChatGPT})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.genericCallWithEntries(context.Background(), entries, mustJSON(t, map[string]any{
		"session": workflow.ID, "action": "admin/project/onboard", "input": map[string]any{"repository": "acme/widget", "code": "WID"},
	})); err == nil || !strings.Contains(err.Error(), "Admin Session") {
		t.Fatalf("workflow Session reached admin action: %v", err)
	}
}

func TestAdminSchemaOnlyShowsAdminDomainToAdminSession(t *testing.T) {
	svc, db := mcpServiceWithSQLite(t, config.Config{GatewayID: "HOM", StateDir: t.TempDir()})
	server := &Server{Service: svc}
	admin, err := durableSession.NewStoreWithGateway(db, "HOM").CreateAdmin(nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := server.genericSchemaPublic(context.Background(), server.tools(), mustJSON(t, map[string]any{"session": admin.ID, "path": "admin"}))
	if err != nil {
		t.Fatal(err)
	}
	if result.(map[string]any)["kind"] != "domain" {
		t.Fatalf("schema=%#v", result)
	}
}
