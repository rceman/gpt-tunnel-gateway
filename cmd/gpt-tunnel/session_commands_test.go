package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

func TestSessionTokenCLIReadsHostLocalPlannerGrant(t *testing.T) {
	stateDir := t.TempDir()
	root := filepath.Join(stateDir, "project")
	cfg := config.Config{
		GatewayID: "HOM",
		StateDir:  stateDir,
		Projects: map[string]config.ProjectConfig{
			"example": {Root: root, Mirror: filepath.Join(stateDir, "mirror.git"), Remote: "origin", DefaultBranch: "main", ProjectCode: "EXM", AirelaySessionKey: "example_worker"},
		},
	}
	db, err := sqlitestore.Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	svc := service.NewWithDurabilityDeferredWorkers(cfg, db)
	grant, err := svc.EnsureProjectSessionBootstrapGrant(context.Background(), "example", "EXM")
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := sessionToken(context.Background(), service.New(cfg), "EXM")
	if err != nil {
		t.Fatal(err)
	}
	if got != grant.Token || got == "" {
		t.Fatalf("CLI token=%q grant=%q", got, grant.Token)
	}
}
