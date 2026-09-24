package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

func newTSK600AgentMutationFixture(t *testing.T) (*Service, *sqlitestore.Databases) {
	t.Helper()
	ctx := context.Background()
	s, _, _ := testServiceSerial(t)
	configuration, err := s.ProjectConfigurationRead(ctx, "example")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := s.AgentRead(ctx, "example", "coder-example")
	if err != nil {
		t.Fatal(err)
	}
	db, err := sqlitestore.Open(s.Config.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s.Durability = db
	configurationPayload, err := json.Marshal(configuration)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.PutSharedProjection(ctx, "project_configuration", sqlitestore.SharedEntity{
		ID: "example", Revision: int64(configuration.Revision), Payload: configurationPayload, UpdatedAt: configuration.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.SeedSharedRulesFromConfiguration(ctx, configuration, "EXM"); err != nil {
		t.Fatal(err)
	}
	agentPayload, err := json.Marshal(agent)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertLocalAgent(ctx, sqlitestore.LocalAgent{
		ProjectID: "example", AgentID: agent.AgentID, Payload: agentPayload, UpdatedAt: agent.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
	return s, db
}

func tsk600Binding(s *Service, agentID, runtime string) {
	if s.Config.ProjectAgentBindings == nil {
		s.Config.ProjectAgentBindings = map[string]map[string]config.AgentBinding{}
	}
	if s.Config.ProjectAgentBindings["example"] == nil {
		s.Config.ProjectAgentBindings["example"] = map[string]config.AgentBinding{}
	}
	s.Config.ProjectAgentBindings["example"][agentID] = config.AgentBinding{SessionKey: runtime}
}

func registerTSK600Agent(t *testing.T, s *Service, agentID string) model.Agent {
	t.Helper()
	agent, result, err := s.AgentRegister(context.Background(), AgentRegisterInput{
		ProjectID: "example",
		AgentID:   agentID,
	})
	if err != nil || result.Status != "registered" {
		t.Fatalf("register Agent %q: agent=%#v result=%#v err=%v", agentID, agent, result, err)
	}
	return agent
}

func createTSK600RoleSession(t *testing.T, db *sqlitestore.Databases, role, runtime string) durableSession.Record {
	t.Helper()
	ref := runtime
	record, err := durableSession.NewStoreWithDurability(db).Create(durableSession.CreateInput{
		ProjectID: "example", ProjectCode: "EXM", Role: role, SessionType: durableSession.SessionTypeChatGPT, SessionRef: &ref,
	})
	if err != nil {
		t.Fatalf("create %s Session: %v", role, err)
	}
	return record
}

func TestTSK600TwoEnabledAgentsBindLeadAndWorkerRuntimes(t *testing.T) {
	s, db := newTSK600AgentMutationFixture(t)
	ctx := context.Background()
	tsk600Binding(s, "coder-example", "runtime-lead")
	tsk600Binding(s, "worker-agent", "runtime-worker")
	registerTSK600Agent(t, s, "worker-agent")
	leadSession := createTSK600RoleSession(t, db, durableSession.RoleLead, "runtime-lead")
	workerSession := createTSK600RoleSession(t, db, durableSession.RoleWorker, "runtime-worker")

	lead, err := s.ResolveProjectLead(ctx, "example")
	if err != nil {
		t.Fatalf("resolve Lead runtime: %v", err)
	}
	worker, err := s.ResolveProjectWorker(ctx, "example")
	if err != nil {
		t.Fatalf("resolve Worker runtime: %v", err)
	}
	if lead.Agent.AgentID != "coder-example" || lead.Session.ID != leadSession.ID || lead.Session.Role != durableSession.RoleLead {
		t.Fatalf("unexpected Lead binding: %#v", lead)
	}
	if worker.Agent.AgentID != "worker-agent" || worker.Session.ID != workerSession.ID || worker.Session.Role != durableSession.RoleWorker {
		t.Fatalf("unexpected Worker binding: %#v", worker)
	}
	if lead.Agent.AgentID == worker.Agent.AgentID || lead.Session.ID == worker.Session.ID || lead.Binding.SessionKey == worker.Binding.SessionKey {
		t.Fatalf("Lead and Worker runtime identities were merged: lead=%#v worker=%#v", lead, worker)
	}
	if _, err := s.ResolveProjectLead(ctx, "other"); err == nil || !strings.Contains(err.Error(), "RUNTIME_IDENTITY_UNAVAILABLE") {
		t.Fatalf("wrong-project Lead resolution was not rejected: %v", err)
	}
	if _, err := s.ResolveProjectWorker(ctx, "other"); err == nil || !strings.Contains(err.Error(), "RUNTIME_IDENTITY_UNAVAILABLE") {
		t.Fatalf("wrong-project Worker resolution was not rejected: %v", err)
	}
}

func TestTSK600RuntimeBindingCollisionRejectedOnRegisterAndUpdate(t *testing.T) {
	t.Run("register", func(t *testing.T) {
		s, _ := newTSK600AgentMutationFixture(t)
		tsk600Binding(s, "coder-example", "runtime-shared")
		tsk600Binding(s, "second-agent", "runtime-shared")
		if _, _, err := s.AgentRegister(context.Background(), AgentRegisterInput{
			ProjectID: "example",
			AgentID:   "second-agent",
		}); err == nil || !strings.Contains(err.Error(), "RUNTIME_IDENTITY_AMBIGUOUS") {
			t.Fatalf("runtime binding collision was accepted during registration: %v", err)
		}
	})

	t.Run("update", func(t *testing.T) {
		s, _ := newTSK600AgentMutationFixture(t)
		tsk600Binding(s, "coder-example", "runtime-lead")
		tsk600Binding(s, "second-agent", "runtime-worker")
		registerTSK600Agent(t, s, "second-agent")
		tsk600Binding(s, "second-agent", "runtime-lead")
		if _, _, err := s.AgentUpdate(authority.WithPlanner(context.Background()), AgentUpdateInput{
			ProjectID:            "example",
			AgentID:              "second-agent",
			RecommendedReasoning: pointer(model.ReasoningMax),
			UpdatedBy:            "planner",
		}); err == nil || !strings.Contains(err.Error(), "RUNTIME_IDENTITY_AMBIGUOUS") {
			t.Fatalf("runtime binding collision was accepted during update: %v", err)
		}
	})
}

func TestTSK600DisableEnablePreservesBindingInvariants(t *testing.T) {
	s, db := newTSK600AgentMutationFixture(t)
	tsk600Binding(s, "coder-example", "runtime-lead")
	tsk600Binding(s, "second-agent", "runtime-worker")
	registerTSK600Agent(t, s, "second-agent")
	workerSession := createTSK600RoleSession(t, db, durableSession.RoleWorker, "runtime-worker")
	ctx := context.Background()
	mutationCtx := authority.WithPlanner(ctx)

	disabled := false
	if _, _, err := s.AgentUpdate(mutationCtx, AgentUpdateInput{
		ProjectID: "example",
		AgentID:   "second-agent",
		Enabled:   &disabled,
		UpdatedBy: "planner",
	}); err != nil {
		t.Fatalf("disable Worker Agent: %v", err)
	}
	if _, err := s.ResolveProjectWorker(ctx, "example"); err == nil || !strings.Contains(err.Error(), "RUNTIME_IDENTITY_UNAVAILABLE") {
		t.Fatalf("disabled Worker Agent remained resolvable: %v", err)
	}

	enabled := true
	if _, _, err := s.AgentUpdate(mutationCtx, AgentUpdateInput{
		ProjectID: "example",
		AgentID:   "second-agent",
		Enabled:   &enabled,
		UpdatedBy: "planner",
	}); err != nil {
		t.Fatalf("re-enable Worker Agent: %v", err)
	}
	worker, err := s.ResolveProjectWorker(ctx, "example")
	if err != nil || worker.Agent.AgentID != "second-agent" || worker.Session.ID != workerSession.ID {
		t.Fatalf("re-enabled Worker binding did not recover: worker=%#v err=%v", worker, err)
	}
}

func pointer(value string) *string {
	return &value
}
