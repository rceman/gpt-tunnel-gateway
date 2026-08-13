package service

import (
	"context"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestProjectConfigurationRegisterReadUpdateAndStatus(t *testing.T) {
	s, revision, _ := testServiceWithoutIdentifiers(t)
	ctx := context.Background()
	configuration, err := s.ProjectConfigurationRead(ctx, "example")
	if err != nil {
		t.Fatal(err)
	}
	if configuration.Revision != 1 || configuration.ProjectID != "example" {
		t.Fatalf("unexpected registered configuration: %#v", configuration)
	}
	routing := configuration.AgentRouting
	routing.SingletonRecommendedReasoning = model.ReasoningMedium
	updated, operation, err := s.ProjectConfigurationUpdate(trustedWorkflowPolicyContext(ctx, "planner"), ProjectConfigurationUpdateInput{
		ProjectID:        "example",
		ExpectedRevision: configuration.Revision,
		Patch: ProjectConfigurationPatch{
			AgentRouting: &routing,
		},
		UpdatedBy: "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: revision,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if operation.Status != "updated" || updated.Revision != 2 || updated.AgentRouting.SingletonRecommendedReasoning != model.ReasoningMedium {
		t.Fatalf("unexpected update: %#v %#v", updated, operation)
	}
	status, err := s.ProjectStatus(ctx, "example")
	if err != nil {
		t.Fatal(err)
	}
	if status.ProjectConfiguration.State != "valid" || status.ProjectConfiguration.Revision != 2 || status.ProjectConfiguration.Configuration == nil {
		t.Fatalf("unexpected project configuration status: %#v", status.ProjectConfiguration)
	}
}
