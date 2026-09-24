package service

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

func tsk620WorkerMigrationFixture(t *testing.T) (*Service, *sqlitestore.Databases, string) {
	t.Helper()
	s, revision, _ := testServiceWithoutIdentifiers(t)
	projectConfig := s.Config.Projects["example"]
	projectConfig.Root = t.TempDir()
	projectConfig.Mirror = filepath.Join(t.TempDir(), "mirror.git")
	projectConfig.ProjectCode = "GTW"
	projectConfig.AirelaySessionKey = "gpt-tunnel-gateway_master"
	s.Config.Projects[config.GTWProjectID] = projectConfig
	_, err := s.ProjectRegister(context.Background(), ProjectRegisterInput{
		Project: model.Project{
			SchemaVersion:      1,
			ID:                 config.GTWProjectID,
			RepositoryURL:      "git@example.invalid:gpt-tunnel-gateway.git",
			DefaultBranch:      "main",
			WorkflowRepository: "workflow",
			WorkflowCommit:     strings.Repeat("a", 40),
			Status:             "active",
		},
		WriteOptions: WriteOptions{
			ExpectedHubRevision: revision,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	db, err := sqlitestore.Open(s.Config.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	s.Durability = db
	t.Cleanup(func() { _ = db.Close() })
	s.Config.ProjectAgentBindings = map[string]map[string]config.AgentBinding{
		config.GTWProjectID: {
			config.LegacyGTWWorkerAgentID: {SessionKey: "gpt-tunnel-gateway_master"},
		},
	}
	if _, _, err := s.AgentRegister(context.Background(), AgentRegisterInput{
		ProjectID: config.GTWProjectID,
		AgentID:   config.LegacyGTWWorkerAgentID,
	}); err != nil {
		t.Fatalf("register legacy GTW Worker Local Agent: %v", err)
	}
	workerSession, err := durableSession.NewStoreWithDurability(db).Create(durableSession.CreateInput{
		ProjectID: config.GTWProjectID, ProjectCode: "GTW", Role: durableSession.RoleWorker,
		SessionType: durableSession.SessionTypeChatGPT, SessionRef: sessionRef("gpt-tunnel-gateway_master"),
	})
	if err != nil {
		t.Fatal(err)
	}
	return s, db, workerSession.ID
}

func sessionRef(value string) *string { return &value }

func TestTSK620ManagedAgentMigrationPreservesWorkerSessionAndRestarts(t *testing.T) {
	s, db, sessionID := tsk620WorkerMigrationFixture(t)
	s.Config.ProjectAgentBindings[config.GTWProjectID] = map[string]config.AgentBinding{
		config.GTWWorkerAgentID: {SessionKey: "gpt-tunnel-gateway_master"},
	}
	if err := s.MigrateGTWWorkerIdentityLocalShared(context.Background()); err != nil {
		t.Fatal(err)
	}
	paths, err := s.Hub.List(context.Background(), s.projectPrefix(config.GTWProjectID)+"/agents", ".json")
	if (err != nil && !IsNotFound(err)) || len(paths) != 0 {
		t.Fatalf("Local Agent migration published Hub current state: paths=%#v err=%v", paths, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := sqlitestore.Open(s.Config.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	s.Durability = reopened
	if err := s.ReconcileGTWWorkerIdentity(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.ReconcileGTWWorkerIdentity(context.Background()); err != nil {
		t.Fatal(err)
	}
	paths, err = s.Hub.List(context.Background(), s.projectPrefix(config.GTWProjectID)+"/agents", ".json")
	if (err != nil && !IsNotFound(err)) || len(paths) != 0 {
		t.Fatalf("restart reconciliation published Hub Agent state: paths=%#v err=%v", paths, err)
	}
	restarted, err := s.ResolveProjectWorker(context.Background(), config.GTWProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if restarted.Agent.AgentID != config.GTWWorkerAgentID || restarted.Session.ID != sessionID || restarted.Session.Role != durableSession.RoleWorker || restarted.Binding.SessionKey != "gpt-tunnel-gateway_master" {
		t.Fatalf("Worker continuity was not preserved after restart reconciliation: %#v", restarted)
	}
	records, err := durableSession.NewStoreWithDurability(reopened).List()
	if err != nil || len(records) != 1 || records[0].ID != sessionID {
		t.Fatalf("Worker Session cardinality changed: records=%#v err=%v", records, err)
	}
}

func TestTSK620LocalAgentIdentityMigrationRejectsCollision(t *testing.T) {
	s, db, _ := tsk620WorkerMigrationFixture(t)
	s.Config.ProjectAgentBindings[config.GTWProjectID][config.GTWWorkerAgentID] = config.AgentBinding{SessionKey: "gpt-tunnel-gateway_other"}
	if _, _, err := s.AgentRegister(context.Background(), AgentRegisterInput{
		ProjectID: config.GTWProjectID,
		AgentID:   config.GTWWorkerAgentID,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.MigrateGTWWorkerIdentityLocalShared(context.Background()); err == nil {
		t.Fatal("Local Agent identity collision was accepted")
	}
	if _, err := db.ReadLocalAgent(context.Background(), config.GTWProjectID, config.LegacyGTWWorkerAgentID); err != nil {
		t.Fatalf("failed migration removed legacy Local Agent: %v", err)
	}
	if _, err := db.ReadLocalAgent(context.Background(), config.GTWProjectID, config.GTWWorkerAgentID); err != nil {
		t.Fatalf("failed migration removed canonical Local Agent: %v", err)
	}
	paths, err := s.Hub.List(context.Background(), s.projectPrefix(config.GTWProjectID)+"/agents", ".json")
	if (err != nil && !IsNotFound(err)) || len(paths) != 0 {
		t.Fatalf("local Agent identity migration published Hub records: paths=%#v err=%v", paths, err)
	}
}
