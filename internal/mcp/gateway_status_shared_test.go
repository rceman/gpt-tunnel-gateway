package mcp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

func TestGatewayStatusUsesSharedProjectProjectionWithoutHub(t *testing.T) {
	stateDir := t.TempDir()
	db, err := sqlitestore.Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	projectID := "example"
	now := time.Now().UTC()
	configuration := model.DefaultProjectConfiguration(projectID, now)
	payload, err := json.Marshal(configuration)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.PutSharedProjection(context.Background(), "project_configuration", sqlitestore.SharedEntity{
		ID:        projectID,
		Revision:  int64(configuration.Revision),
		Payload:   payload,
		UpdatedAt: now.Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}

	c := config.Config{
		GatewayID: "test-gateway",
		StateDir:  stateDir,
		Hub: config.HubConfig{
			RepositoryURL: filepath.Join(t.TempDir(), "unavailable-hub.git"),
			Branch:        "main",
		},
		Projects: map[string]config.ProjectConfig{
			projectID: {
				ProjectCode:       "EXM",
				Root:              t.TempDir(),
				Mirror:            filepath.Join(t.TempDir(), "mirror.git"),
				Remote:            "origin",
				DefaultBranch:     "main",
				AirelaySessionKey: "example_master",
			},
		},
	}
	s := service.NewWithDurabilityDeferredWorkers(c, db)
	session, err := durableSession.NewStore(stateDir).Create(durableSession.CreateInput{
		ProjectID:   projectID,
		ProjectCode: "EXM",
		Role:        durableSession.RolePlanner,
		SessionType: durableSession.SessionTypeChatGPT,
	})
	if err != nil {
		t.Fatal(err)
	}

	server := &Server{Service: s}
	entries := map[string]genericActionEntry{}
	server.addBootstrapActions(entries, map[string]Tool{
		"system_ping": {Execute: func(context.Context, json.RawMessage) (any, error) {
			return map[string]any{"service": "test"}, nil
		}},
	})
	ctx, cancel := context.WithTimeout(service.WithAgentSessionID(context.Background(), session.ID), time.Second)
	defer cancel()
	started := time.Now()
	value, err := entries["gateway/status"].Execute(ctx, nil)
	if err != nil {
		t.Fatalf("gateway/status failed with unavailable Hub: %v", err)
	}
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("gateway/status took %s with Shared state and unavailable Hub", elapsed)
	}
	base, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("gateway/status result=%#v", value)
	}
	projectStatus, ok := base["project_status"].(service.ProjectOperationalStatus)
	if !ok || projectStatus.Project.ID != projectID || projectStatus.Project.Code != "EXM" {
		t.Fatalf("gateway/status did not use Shared project projection: %#v", base)
	}
}
