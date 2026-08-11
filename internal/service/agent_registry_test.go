package service

import (
	"context"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestAgentRegistryRegisterUpdateDisableReadListAndStatus(t *testing.T) {
	s, revision, _ := testServiceWithoutIdentifiers(t)
	nowAgent := model.Agent{
		SchemaVersion: model.AgentSchemaVersion, ProjectID: "example", AgentID: "coder",
		Role: model.AgentRoleCoding, Enabled: true, RecommendedReasoning: model.ReasoningHigh,
		Capabilities: []string{"git", "review"},
	}
	ctx := authority.WithPlanner(context.Background())
	agent, registered, err := s.AgentRegister(ctx, AgentRegisterInput{Agent: nowAgent, WriteOptions: WriteOptions{ExpectedHubRevision: revision}})
	if err != nil {
		t.Fatal(err)
	}
	if registered.Status != "registered" || agent.CreatedAt.IsZero() || agent.UpdatedAt.IsZero() {
		t.Fatalf("unexpected registration: %#v %#v", agent, registered)
	}
	if _, _, err := s.AgentRegister(ctx, AgentRegisterInput{Agent: nowAgent, WriteOptions: WriteOptions{ExpectedHubRevision: registered.Hub.After}}); err == nil {
		t.Fatal("duplicate agent was accepted")
	}
	medium := model.ReasoningMedium
	disabled := false
	updated, updateResult, err := s.AgentUpdate(ctx, AgentUpdateInput{
		ProjectID: "example", AgentID: "coder", Enabled: &disabled, RecommendedReasoning: &medium,
		UpdatedBy: "planner", WriteOptions: WriteOptions{ExpectedHubRevision: registered.Hub.After},
	})
	if err != nil {
		t.Fatal(err)
	}
	if updateResult.Status != "updated" || updated.Enabled || updated.RecommendedReasoning != model.ReasoningMedium {
		t.Fatalf("unexpected update: %#v %#v", updated, updateResult)
	}
	read, err := s.AgentRead(context.Background(), "example", "coder")
	if err != nil || read.AgentID != "coder" || read.Enabled {
		t.Fatalf("unexpected read: %#v %v", read, err)
	}
	agents, err := s.AgentList(context.Background(), "example")
	if err != nil || len(agents) != 1 || agents[0].AgentID != "coder" {
		t.Fatalf("unexpected list: %#v %v", agents, err)
	}
	status, err := s.AgentRegistryStatus(context.Background(), "example", "coder")
	if err != nil || status.State != "disabled" || status.Usable {
		t.Fatalf("unexpected disabled status: %#v %v", status, err)
	}
}

func TestAgentRegistryUsesProjectScopedLocalBinding(t *testing.T) {
	s, revision, _ := testServiceWithoutIdentifiers(t)
	s.Config.ProjectAgentBindings = map[string]map[string]config.AgentBinding{
		"example": {"coder": {SessionKey: "example_master"}},
	}
	ctx := authority.WithDelivery(context.Background())
	agent := model.Agent{SchemaVersion: model.AgentSchemaVersion, ProjectID: "example", AgentID: "coder", Role: model.AgentRoleCoding, Enabled: true, RecommendedReasoning: model.ReasoningBestAvailable}
	registered, _, err := s.AgentRegister(ctx, AgentRegisterInput{Agent: agent, WriteOptions: WriteOptions{ExpectedHubRevision: revision}})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Config.ResolveAgentBinding("other", "coder"); ok {
		t.Fatal("project-scoped binding leaked to another project")
	}
	status, err := s.AgentRegistryStatus(context.Background(), "example", registered.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Bound || status.State != "unavailable" {
		t.Fatalf("expected bound but unavailable fake session: %#v", status)
	}
}
