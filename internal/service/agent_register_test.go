package service

import (
	"context"
	"reflect"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/session"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

func TestAgentRegisterCreatesLocalAgentWithoutHubState(t *testing.T) {
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
	paths, err := s.Hub.List(context.Background(), s.projectPrefix("example")+"/agents", ".json")
	if (err != nil && !IsNotFound(err)) || len(paths) != 0 {
		t.Fatalf("Agent registration published Hub current state: paths=%#v err=%v", paths, err)
	}
	localAgent, err := db.ReadLocalAgent(context.Background(), "example", agent.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	projected, err := s.AgentRead(context.Background(), "example", agent.AgentID)
	if err != nil || !reflect.DeepEqual(projected, agent) || string(localAgent.Payload) == "" || localAgent.ProjectID != "example" {
		t.Fatalf("invalid Local Agent authority: projected=%#v local=%#v err=%v", projected, localAgent, err)
	}
	sessions, err := session.NewStoreWithDurability(db).List()
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 0 {
		t.Fatalf("registration created durable sessions: %#v", sessions)
	}
}

func TestAgentRegisterAllowsMultipleEnabledAndRejectsDuplicateWithoutHubWrites(t *testing.T) {
	s, revision, _ := testServiceWithoutIdentifiers(t)
	_, _, err := s.ProjectIdentifiersAdopt(context.Background(), ProjectIdentifiersAdoptInput{
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
	}); err != nil {
		t.Fatal(err)
	}
	second, secondResult, err := s.AgentRegister(context.Background(), AgentRegisterInput{
		ProjectID: "example",
		AgentID:   "second-agent",
	})
	if err != nil || !second.Enabled || secondResult.Status != "registered" {
		t.Fatalf("second enabled coding Agent was not registered: agent=%#v result=%#v err=%v", second, secondResult, err)
	}
	if _, _, err := s.AgentRegister(context.Background(), AgentRegisterInput{
		ProjectID: "example",
		AgentID:   "coder-example",
	}); err == nil {
		t.Fatal("duplicate Agent identity unexpectedly succeeded")
	}
	paths, err := s.Hub.List(context.Background(), s.projectPrefix("example")+"/agents", ".json")
	if (err != nil && !IsNotFound(err)) || len(paths) != 0 {
		t.Fatalf("local Agent registration published Hub records: paths=%#v err=%v", paths, err)
	}
}
