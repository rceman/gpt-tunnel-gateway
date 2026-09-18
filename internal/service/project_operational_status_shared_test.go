package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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
		AirelaySessionKey: "wrong_local_master",
	}
	airelay := filepath.Join(t.TempDir(), "airelay")
	if err := os.WriteFile(airelay, []byte("#!/bin/sh\n[ \"$2\" = gpt-tunnel-gateway_master ] || exit 9\nprintf 'Controller: reachable\\nState: idle\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	c := config.Config{
		SchemaVersion:          1,
		StateDir:               stateDir,
		DispatchTimeoutSeconds: 1,
		AirelayCommand:         airelay,
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
	if err := db.SeedSharedRulesFromConfiguration(context.Background(), configuration, "EXM"); err != nil {
		t.Fatal(err)
	}
	head := strings.Repeat("b", 40)
	if err := db.CreateTaskExecutionState(context.Background(), model.TaskExecutionState{
		TaskID: "EXM-TSK1", ProjectID: projectID, TaskRevision: 1, TaskRevisionSHA256: strings.Repeat("a", 64),
		Status: model.TaskExecutionInProgress, Stage: "code", Worktree: "WT-TSK1-" + head[:8],
		BaseHead: strings.Repeat("a", 40), Head: head, Branch: "task/EXM-TSK1-lane", Agent: "gtw-worker",
		ExecutionRevision: 1, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	s := NewWithDurabilityDeferredWorkers(c, db)
	session, err := durableSession.NewStoreWithDurability(db).Create(durableSession.CreateInput{
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
	if result.Rules.Acknowledged || result.Rules.Fresh {
		t.Fatalf("unexpected rules acknowledgement: %#v", result.Rules)
	}
	if result.Agent.State != "unavailable" || result.Agent.AgentID != "" {
		t.Fatalf("worker identity resolved without Hub project authority: %#v", result.Agent)
	}
	if result.TaskID != "" {
		t.Fatalf("Task projection leaked without worker identity: %#v", result.TaskID)
	}
}
