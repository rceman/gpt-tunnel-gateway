package service

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

func TestProjectOperationalStatusUsesLocalSharedStateWhenHubUnavailable(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	projectRoot := filepath.Join(t.TempDir(), "project")
	projectMirror := filepath.Join(t.TempDir(), "mirror.git")
	projectID := "example"
	configProject := config.ProjectConfig{
		Root:              projectRoot,
		Mirror:            projectMirror,
		Remote:            "origin",
		DefaultBranch:     "main",
		ProjectCode:       "EXM",
		AirelaySessionKey: "example_master",
	}
	c := config.Config{
		SchemaVersion: 1,
		StateDir:      stateDir,
		Hub: config.HubConfig{
			RepositoryURL: filepath.Join(t.TempDir(), "unavailable-hub.git"),
			Branch:        "main",
		},
		Projects: map[string]config.ProjectConfig{projectID: configProject},
	}
	db, err := sqlitestore.Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

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

	s := NewWithDurabilityDeferredWorkers(c, db)
	session, err := durableSession.NewStore(stateDir).Create(durableSession.CreateInput{
		ProjectID:   projectID,
		ProjectCode: configProject.ProjectCode,
		Role:        durableSession.RolePlanner,
		SessionType: durableSession.SessionTypeChatGPT,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(WithAgentSessionID(context.Background(), session.ID), time.Second)
	defer cancel()
	result, err := s.ProjectOperationalStatus(ctx)
	if err != nil {
		t.Fatalf("Shared project/status failed with Hub unavailable: %v", err)
	}
	if result.Project.ID != projectID || result.Project.Code != configProject.ProjectCode {
		t.Fatalf("unexpected project identity: %#v", result.Project)
	}
	if result.Rules.Revision != configuration.Revision {
		t.Fatalf("unexpected rules revision: %#v", result.Rules)
	}
}
