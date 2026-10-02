package service

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

func tsk690Fixture(t *testing.T) (*Service, *sqlitestore.Databases) {
	t.Helper()
	s, revision, _ := testServiceWithoutIdentifiers(t)
	ctx := context.Background()
	if _, _, err := s.ProjectIdentifiersAdopt(ctx, ProjectIdentifiersAdoptInput{
		ProjectID:   "example",
		ProjectCode: "EXM",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: revision,
		},
	}); err != nil {
		t.Fatal(err)
	}
	s.Config.Controller.TunnelHealthListenAddr = "127.0.0.1:8876"
	s.ConfigPath = filepath.Join(t.TempDir(), "missing-config.json")
	db, err := sqlitestore.Open(s.Config.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s.Durability = db
	syncTSK409SharedConfigurationFromHub(t, s)
	return s, db
}

func tsk690Agent(t *testing.T, s *Service, agentID, role, relay string) {
	t.Helper()
	agent, _, err := s.AgentRegister(context.Background(), AgentRegisterInput{
		ProjectID:    "example",
		AgentID:      agentID,
		WorkflowRole: role,
	})
	if err != nil {
		t.Fatal(err)
	}
	if agent.WorkflowRole != role {
		t.Fatalf("agent workflow role=%q", agent.WorkflowRole)
	}
	if err := s.persistAgentBinding("example", agentID, config.AgentBinding{SessionKey: relay}); err != nil {
		t.Fatal(err)
	}
}

func TestTSK690SessionAttachCreatesAndReusesManagedRoleSession(t *testing.T) {
	s, _ := tsk690Fixture(t)
	tsk690Agent(t, s, "EXM-WORKER", "worker", "exm_worker")
	tsk690Agent(t, s, "EXM-LEAD", "lead", "exm_lead")
	tsk690Agent(t, s, "EXM-ADVISOR", "advisor", "exm_advisor")

	ctx := context.Background()
	worker, err := s.SessionAttach(ctx, SessionAttachInput{
		ProjectCode: "EXM",
		Role:        "worker",
		AgentID:     "EXM-WORKER",
	})
	if err != nil {
		t.Fatal(err)
	}
	if worker.Status != "attached" || worker.ProjectID != "example" || worker.ProjectCode != "EXM" || worker.AgentID != "EXM-WORKER" {
		t.Fatalf("worker attach=%#v", worker)
	}
	if worker.Session.Role != "worker" || worker.Session.Status != durableSession.StatusActive || worker.Session.SessionRef == nil || *worker.Session.SessionRef != "exm_worker" {
		t.Fatalf("worker session=%#v", worker.Session)
	}
	lead, err := s.SessionAttach(ctx, SessionAttachInput{
		ProjectCode: "EXM",
		Role:        "lead",
		AgentID:     "EXM-LEAD",
	})
	if err != nil {
		t.Fatal(err)
	}
	if lead.Status != "attached" || lead.Session.Role != "lead" {
		t.Fatalf("lead attach=%#v", lead)
	}
	resolved, err := s.ResolveProjectWorker(ctx, "example")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Session.ID != worker.Session.ID || resolved.Agent.AgentID != "EXM-WORKER" {
		t.Fatalf("resolved worker lane=%#v", resolved)
	}
	resolvedLead, err := s.ResolveProjectLead(ctx, "example")
	if err != nil || resolvedLead.Session.ID != lead.Session.ID {
		t.Fatalf("resolved lead lane=%#v err=%v", resolvedLead, err)
	}
	advisor, err := s.SessionAttach(ctx, SessionAttachInput{
		ProjectCode: "EXM",
		Role:        "advisor",
		AgentID:     "EXM-ADVISOR",
	})
	if err != nil {
		t.Fatal(err)
	}
	if advisor.Status != "attached" || advisor.Session.Role != "advisor" || *advisor.Session.SessionRef != "exm_advisor" {
		t.Fatalf("advisor attach=%#v", advisor)
	}
	repeat, err := s.SessionAttach(ctx, SessionAttachInput{
		ProjectCode: "EXM",
		Role:        "worker",
		AgentID:     "EXM-WORKER",
	})
	if err != nil {
		t.Fatal(err)
	}
	if repeat.Status != "already_attached" || repeat.Session.ID != worker.Session.ID {
		t.Fatalf("repeat attach=%#v", repeat)
	}
}

func TestTSK690SessionAttachRejectsPlannerAndRoleMismatch(t *testing.T) {
	s, _ := tsk690Fixture(t)
	tsk690Agent(t, s, "EXM-WORKER", "worker", "exm_worker")
	ctx := context.Background()

	if _, err := s.SessionAttach(ctx, SessionAttachInput{
		ProjectCode: "EXM",
		Role:        "planner",
		AgentID:     "EXM-WORKER",
	}); err == nil || !strings.Contains(err.Error(), "session/start") {
		t.Fatalf("planner attach err=%v", err)
	}
	if _, err := s.SessionAttach(ctx, SessionAttachInput{
		ProjectCode: "EXM",
		Role:        "lead",
		AgentID:     "EXM-WORKER",
	}); err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("role-mismatched attach err=%v", err)
	}
	if _, err := s.SessionAttach(ctx, SessionAttachInput{
		ProjectCode: "EXM",
		Role:        "worker",
		AgentID:     "EXM-MISSING",
	}); err == nil || !strings.Contains(err.Error(), "not registered") {
		t.Fatalf("unregistered agent attach err=%v", err)
	}
	if _, err := s.SessionAttach(ctx, SessionAttachInput{
		ProjectCode: "ZZZ",
		Role:        "worker",
		AgentID:     "EXM-WORKER",
	}); err == nil || !strings.Contains(err.Error(), "not a configured Local project") {
		t.Fatalf("unknown project attach err=%v", err)
	}
	if _, err := s.SessionAttach(ctx, SessionAttachInput{
		ProjectCode: "EXM",
		Role:        "worker",
	}); err == nil {
		t.Fatal("attach without Agent reference succeeded")
	}
}

func TestTSK690SessionAttachConflictingActiveBindingFailsClosed(t *testing.T) {
	s, db := tsk690Fixture(t)
	tsk690Agent(t, s, "EXM-WORKER", "worker", "exm_worker")
	tsk690Agent(t, s, "EXM-WORKER2", "worker", "exm_worker_two")
	ctx := context.Background()

	first, err := s.SessionAttach(ctx, SessionAttachInput{
		ProjectCode: "EXM",
		Role:        "worker",
		AgentID:     "EXM-WORKER",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SessionAttach(ctx, SessionAttachInput{
		ProjectCode: "EXM",
		Role:        "worker",
		AgentID:     "EXM-WORKER2",
	}); err == nil || !strings.Contains(err.Error(), "different managed runtime") {
		t.Fatalf("conflicting active binding attach err=%v", err)
	}
	staleRef := "exm_worker_stale"
	if _, err := durableSession.NewStoreWithGateway(db, s.Config.GatewayID).Create(durableSession.CreateInput{ProjectID: "example", ProjectCode: "EXM", Role: "worker", SessionType: durableSession.SessionTypeChatGPT, SessionRef: &staleRef}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SessionAttach(ctx, SessionAttachInput{
		ProjectCode: "EXM",
		Role:        "worker",
		AgentID:     "EXM-WORKER",
	}); err == nil || !strings.Contains(err.Error(), "different managed runtime") {
		t.Fatalf("stale active worker Session attach err=%v", err)
	}
	resolved, err := s.ResolveProjectWorker(ctx, "example")
	if err != nil || resolved.Session.ID != first.Session.ID {
		t.Fatalf("resolved worker lane=%#v err=%v", resolved, err)
	}
}

func TestTSK690SessionAttachRejectsUnboundAndDisabledAgents(t *testing.T) {
	s, _ := tsk690Fixture(t)
	ctx := context.Background()
	if _, _, err := s.AgentRegister(ctx, AgentRegisterInput{
		ProjectID:    "example",
		AgentID:      "EXM-UNBOUND",
		WorkflowRole: "worker",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SessionAttach(ctx, SessionAttachInput{
		ProjectCode: "EXM",
		Role:        "worker",
		AgentID:     "EXM-UNBOUND",
	}); err == nil || !strings.Contains(err.Error(), "no managed runtime binding") {
		t.Fatalf("unbound agent attach err=%v", err)
	}
	if _, _, err := s.AgentDisable(ctx, AgentDisableInput{
		ProjectID: "example",
		AgentID:   "EXM-UNBOUND",
		UpdatedBy: "test",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SessionAttach(ctx, SessionAttachInput{
		ProjectCode: "EXM",
		Role:        "worker",
		AgentID:     "EXM-UNBOUND",
	}); err == nil || !strings.Contains(err.Error(), "enabled") {
		t.Fatalf("disabled agent attach err=%v", err)
	}
}
