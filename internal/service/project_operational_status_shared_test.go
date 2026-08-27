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
		Watcher:           config.WatcherSettings{AgentID: "wrong-local-agent"},
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
	train := model.TrainV2{
		SchemaVersion: model.TrainV2SchemaVersion,
		ID:            "EXM-TRN1",
		ProjectID:     projectID,
		Revision:      1,
		Status:        model.TrainV2Running,
		CreatedBy:     "planner",
		CreatedAt:     now,
		UpdatedAt:     now,
		Items: []model.TrainV2Item{{
			Position:            0,
			TaskID:              "EXM-TSK1",
			TaskRevision:        1,
			TaskRevisionSHA256:  strings.Repeat("a", 64),
			Status:              model.TrainV2ItemRunning,
			AddedAt:             now,
			ActiveAttemptNumber: 1,
			Attempts: []model.TrainV2Attempt{{
				Number:            1,
				Status:            model.TrainV2AttemptRunning,
				AgentID:           "gpt-review-planner",
				AirelaySessionKey: "gpt-tunnel-gateway_master",
				GatewayID:         "gateway-one",
				StartHead:         strings.Repeat("b", 40),
				StartedAt:         now,
			}},
		}},
	}
	trainPayload, err := json.Marshal(train)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.PutSharedProjection(context.Background(), "train", sqlitestore.SharedEntity{
		ID: "EXM-TRN1", Revision: 1, Payload: trainPayload, UpdatedAt: now.Format(time.RFC3339Nano),
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
	if result.Agent.AgentID != "gpt-review-planner" || result.Agent.Expected != "gpt-review-planner" || !result.Agent.SessionReady {
		t.Fatalf("Shared active Attempt identity was not projected: %#v", result.Agent)
	}
	finished := now.Add(time.Minute)
	train.Status = model.TrainV2Completed
	train.Items[0].Status = model.TrainV2ItemFinalized
	train.Items[0].Attempts[0].Status = model.TrainV2AttemptSucceeded
	train.Items[0].Attempts[0].FinishedAt = &finished
	trainPayload, err = json.Marshal(train)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.PutSharedProjection(context.Background(), "train", sqlitestore.SharedEntity{
		ID: "EXM-TRN1", Revision: 2, Payload: trainPayload, UpdatedAt: finished.Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
	result, err = s.ProjectOperationalStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Agent.AgentID != "" || result.Agent.Expected != "coding" || result.Agent.SessionReady {
		t.Fatalf("local watcher identity leaked without active Shared Attempt: %#v", result.Agent)
	}
}
