package main

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/mcp"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	"github.com/rceman/gpt-tunnel-gateway/internal/session"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

func TestPromptReadViaMCPUsesServerOwnedPMTStore(t *testing.T) {
	stateDir := t.TempDir()
	c := config.Config{GatewayID: "test", StateDir: stateDir, MaxReadBytes: 1 << 20, MaxDiffBytes: 1 << 20, MaxListItems: 1000, Projects: map[string]config.ProjectConfig{
		"example": {Root: stateDir, Mirror: filepath.Join(stateDir, "mirror.git"), Remote: "origin", DefaultBranch: "main", ProjectCode: "EXM", AirelaySessionKey: "example_master"},
	}}
	db, err := sqlitestore.Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	agent, err := session.NewStore(stateDir).Create(session.CreateInput{
		ProjectID: "example", ProjectCode: "EXM", Role: session.RoleAgent, SessionType: session.SessionTypeChatGPT,
	})
	if err != nil {
		t.Fatal(err)
	}
	pmt, err := db.CreatePMT(context.Background(), model.PMT{
		SchemaVersion: model.PMTSchemaVersion, ProjectID: "example", ProjectCode: "EXM",
		Title: "server-owned", Instruction: "read from the Gateway store", PlannerSessionID: "SP-ABCDEFGH",
		TargetSessionID: agent.ID, TargetAirelaySessionKey: "example_master", TargetAgentID: "coding",
		CreatedAt: time.Now().UTC(), State: model.PMTStateUnread, Reference: "pending",
	})
	if err != nil {
		t.Fatal(err)
	}
	server := &mcp.Server{Service: service.NewWithDurability(c, db), AuthorityContext: authority.WithDelivery(context.Background())}
	httpServer := httptest.NewServer(server.Router())
	defer httpServer.Close()
	c.ListenAddr = strings.TrimPrefix(httpServer.URL, "http://")
	cli := service.New(c)
	if cli.Durability != nil {
		t.Fatal("CLI service unexpectedly owns SQLite durability")
	}
	result, err := promptReadViaMCP(context.Background(), cli, pmt.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.ID != pmt.ID || result.Instruction != pmt.Instruction || result.State != model.PMTStateFetched {
		t.Fatalf("result=%#v", result)
	}
}
