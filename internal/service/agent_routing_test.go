package service

import (
	"context"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestResolveAgentDiscoversLegacyAutoBindingProfileByExactSession(t *testing.T) {
	s, _, _ := testService(t)
	delete(s.Config.AgentBindings, config.ProjectAgentBindingKey("example", "coder-example"))
	installServiceExecutionSessionFixture(t, s, t.TempDir()+"/prompts")

	resolved, err := s.ResolveAgent(context.Background(), AgentResolveInput{
		ProjectID: "example", Role: model.AgentRoleCoding, AgentID: "coder-example",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.SessionKey != "example_master" || resolved.Profile != "coding" {
		t.Fatalf("resolved legacy auto binding=%#v", resolved)
	}
	if binding, ok := s.Config.ResolveAutoAgentBinding("example"); !ok || binding.Profile != "" {
		t.Fatalf("auto binding was persisted or synthesized: %#v, %v", binding, ok)
	}
}

func TestTaskWorkBootstrapsLegacyAutoBindingProfile(t *testing.T) {
	s, revision, _ := testService(t)
	delete(s.Config.AgentBindings, config.ProjectAgentBindingKey("example", "coder-example"))
	installServiceExecutionSessionFixture(t, s, t.TempDir()+"/prompts")
	revision = enableTrainV2ForTest(t, s, revision)
	task, revision := readyTrainTaskForTest(t, s, revision, "Legacy auto Agent profile")
	train, _, err := s.TrainV2Create(context.Background(), TrainV2CreateInput{
		ProjectID: "example", TaskIDs: []string{task.ID}, CreatedBy: "planner",
		WriteOptions: WriteOptions{ExpectedHubRevision: revision},
	})
	if err != nil {
		t.Fatal(err)
	}
	work, err := s.TaskWork(context.Background(), TaskWorkInput{TaskID: task.ID})
	if err != nil {
		t.Fatal(err)
	}
	if work.TrainID != train.ID || work.TaskID != task.ID || work.Text == "" {
		t.Fatalf("legacy auto-bound TaskWork=%#v", work)
	}
	if binding, ok := s.Config.ResolveAutoAgentBinding("example"); !ok || binding.Profile != "" {
		t.Fatalf("TaskWork persisted discovered profile: %#v, %v", binding, ok)
	}
}
