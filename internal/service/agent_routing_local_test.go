package service

import (
	"context"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/session"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

func TestResolveTrainAgentLocalFirstRequiresExplicitLocalAuthority(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	db, err := sqlitestore.Open(s.Config.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s.Durability = db
	s.Config.AgentBindings[config.ProjectAgentBindingKey("example", "coder-example")] = config.AgentBinding{SessionKey: "example_master"}
	if _, err := s.resolveTrainAgentLocalFirst(context.Background(), AgentResolveInput{ProjectID: "example", Role: model.AgentRoleCoding, AgentID: "coder-example"}); err == nil {
		t.Fatal("local Train Agent authority was synthesized without an active Agent session")
	}
	if _, err := session.NewStore(s.Config.StateDir).Create(session.CreateInput{ProjectID: "example", ProjectCode: "EXM", Role: session.RoleAgent, SessionType: session.SessionTypeChatGPT, SessionRef: stringPtr("example_master")}); err != nil {
		t.Fatal(err)
	}
	resolved, err := s.resolveTrainAgentLocalFirst(context.Background(), AgentResolveInput{ProjectID: "example", Role: model.AgentRoleCoding, AgentID: "coder-example", RequireUsable: true})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.AgentID != "coder-example" || resolved.SessionKey != "example_master" || resolved.Role != model.AgentRoleCoding {
		t.Fatalf("unexpected local Agent authority: %#v", resolved)
	}
}

func stringPtr(value string) *string { return &value }
