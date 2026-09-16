package service

import (
	"context"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestResolveAgentUsesExplicitProjectBinding(t *testing.T) {
	s, _, _ := testService(t)
	s.Config.ProjectAgentBindings["example"]["coder-example"] = config.AgentBinding{SessionKey: "example_master"}
	installServiceExecutionSessionFixture(t, s, t.TempDir()+"/prompts")

	resolved, err := s.ResolveAgent(context.Background(), AgentResolveInput{
		ProjectID: "example",
		Role:      model.AgentRoleCoding,
		AgentID:   "coder-example",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.SessionKey != "example_master" {
		t.Fatalf("resolved explicit project binding=%#v", resolved)
	}
}

func TestResolveAgentFailsClosedWithoutExplicitProjectBinding(t *testing.T) {
	s, _, _ := testService(t)
	delete(s.Config.ProjectAgentBindings["example"], "coder-example")
	installServiceExecutionSessionFixture(t, s, t.TempDir()+"/prompts")

	if _, err := s.ResolveAgent(context.Background(), AgentResolveInput{
		ProjectID: "example",
		Role:      model.AgentRoleCoding,
		AgentID:   "coder-example",
	}); err == nil {
		t.Fatal("Agent resolution succeeded without an explicit project binding")
	}
}
