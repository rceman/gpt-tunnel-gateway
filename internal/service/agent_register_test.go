package service

import (
	"context"
	"reflect"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/session"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

func TestAgentRegisterCreatesPortableHubAndLocalAgent(t *testing.T) {
	s, revision, _ := testServiceWithoutIdentifiers(t)
	identifiers, adopted, err := s.ProjectIdentifiersAdopt(context.Background(), ProjectIdentifiersAdoptInput{
		ProjectID:   "example",
		ProjectCode: "EXM",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: revision,
		},
	})
	if err != nil || identifiers.ProjectCode != "EXM" || adopted.Hub.After == "" {
		t.Fatalf("adopt identifiers: %#v %#v %v", identifiers, adopted, err)
	}
	db, err := sqlitestore.Open(s.Config.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s.Durability = db

	agent, result, err := s.AgentRegister(context.Background(), AgentRegisterInput{
		ProjectID: "example",
		AgentID:   "coder-example",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "registered" || agent.Role != model.AgentRoleCoding || !agent.Enabled {
		t.Fatalf("unexpected registration: %#v %#v", agent, result)
	}
	if agent.RecommendedReasoning != model.ReasoningHigh || len(agent.Capabilities) != 2 {
		t.Fatalf("unexpected portable defaults: %#v", agent)
	}
	var hubAgent model.Agent
	if err := s.Hub.ReadJSON(context.Background(), s.agentPath("example", agent.AgentID), &hubAgent); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(hubAgent, agent) {
		t.Fatalf("Hub and returned Agent differ: hub=%#v returned=%#v", hubAgent, agent)
	}
	localAgent, err := db.ReadLocalAgent(context.Background(), "example", agent.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	if string(localAgent.Payload) == "" || localAgent.ProjectID != "example" {
		t.Fatalf("missing Local projection: %#v", localAgent)
	}
	sessions, err := session.NewStoreWithDurability(db).List()
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 0 {
		t.Fatalf("registration created durable sessions: %#v", sessions)
	}
}

func TestAgentRegisterRejectsDuplicateEnabledAndStaleCAS(t *testing.T) {
	s, revision, _ := testServiceWithoutIdentifiers(t)
	_, adopted, err := s.ProjectIdentifiersAdopt(context.Background(), ProjectIdentifiersAdoptInput{
		ProjectID:   "example",
		ProjectCode: "EXM",
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
	defer db.Close()
	s.Durability = db

	if _, _, err := s.AgentRegister(context.Background(), AgentRegisterInput{
		ProjectID: "example",
		AgentID:   "coder-example",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: "stale",
		},
	}); err == nil {
		t.Fatal("stale Hub CAS unexpectedly succeeded")
	}
	if _, _, err := s.AgentRegister(context.Background(), AgentRegisterInput{
		ProjectID: "example",
		AgentID:   "coder-example",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: adopted.Hub.After,
		},
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AgentRegister(context.Background(), AgentRegisterInput{
		ProjectID: "example",
		AgentID:   "second-agent",
	}); err == nil {
		t.Fatal("second enabled coding Agent unexpectedly succeeded")
	}
}
