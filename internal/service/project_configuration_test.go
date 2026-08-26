package service

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
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

func TestProjectStatusFailsClosedWhenSharedProjectConfigurationIsMissing(t *testing.T) {
	hubBare, _, _ := testutil.RepoWithBareRemote(t)
	_, projectRoot, _ := testutil.RepoWithBareRemote(t)
	c := config.Config{
		StateDir: t.TempDir(),
		Hub:      config.HubConfig{RepositoryURL: hubBare, Branch: "main", AuthorName: "Gateway", AuthorEmail: "gateway@example.invalid"},
		Projects: map[string]config.ProjectConfig{"example": {ProjectCode: "EXM", Root: projectRoot, Mirror: filepath.Join(t.TempDir(), "mirror.git"), Remote: "origin", DefaultBranch: "main", AirelaySessionKey: "example_master"}},
	}
	db, err := sqlitestore.Open(c.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s := NewWithDurability(c, db)
	_, err = s.ProjectStatus(context.Background(), "example")
	if err == nil || !strings.Contains(err.Error(), "Shared project configuration") {
		t.Fatalf("ProjectStatus error=%v, want missing Shared configuration", err)
	}
}
