package service

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestTaskAuthoringFindSkipsEarlierLegacyProject(t *testing.T) {
	s, hubRevision, _ := testServiceWithoutIdentifiers(t)
	legacyID := "aaa-legacy"
	_, legacyRoot, _ := testutil.RepoWithBareRemote(t)
	s.Config.Projects[legacyID] = config.ProjectConfig{
		Root: legacyRoot, Mirror: filepath.Join(t.TempDir(), "legacy-mirror.git"), Remote: "origin",
		DefaultBranch: "main", AirelaySessionKey: "legacy_master",
	}
	registered, err := s.ProjectRegister(context.Background(), ProjectRegisterInput{
		Project: model.Project{
			ID: legacyID, RepositoryURL: "git@example.invalid:legacy.git", DefaultBranch: "main",
			WorkflowRepository: "rceman/gpt-review-planner", WorkflowCommit: "b1a45b1e9475ab29dfd3e84d523b70897c7b8918",
		},
		WriteOptions: WriteOptions{
			ExpectedHubRevision: hubRevision,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	hubRevision = registered.Hub.After
	hubRevision = adoptAuthoringIdentifiersForTest(t, s, hubRevision)
	hubRevision = enableCanonicalExecutionForTest(t, s, hubRevision)
	task, _, err := s.TaskAuthoringCreate(context.Background(), TaskAuthoringCreateInput{
		ProjectID:   "example",
		Title:       "Canonical task",
		Summary:     "Find the canonical task.",
		Objective:   "Find the canonical train_v2 task.",
		ADRRelation: model.TaskADRNoRequired,
		CreatedBy:   "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: hubRevision,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	found, err := s.TaskAuthoringFind(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if found.ProjectID != "example" || found.ID != task.ID {
		t.Fatalf("found task = %#v, want canonical example task %s", found, task.ID)
	}
}
func jsonFieldPresent(data []byte, field string) bool {
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil {
		return false
	}
	_, ok := fields[field]
	return ok
}
func adoptAuthoringIdentifiersForTest(t *testing.T, s *Service, hubRevision string) string {
	t.Helper()
	result, operation, err := s.ProjectIdentifiersAdopt(context.Background(), ProjectIdentifiersAdoptInput{
		ProjectID:   "example",
		ProjectCode: "EXM",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: hubRevision,
		},
	})
	if err != nil || result.ProjectCode != "EXM" || result.NextTaskNumber != 1 || operation.Status != "adopted" {
		t.Fatalf("unexpected identifiers: %#v %#v %v", result, operation, err)
	}
	return operation.Hub.After
}

// enableCanonicalExecutionForTest establishes the stored canonical execution
// marker. The stored "train_v2" value is the provenance spelling existing
// configurations carry; CanonicalExecutionEnabled reads it.
func enableCanonicalExecutionForTest(t *testing.T, s *Service, hubRevision string) string {
	t.Helper()
	configuration, err := s.ProjectConfigurationRead(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	expected := hubRevision
	tx, err := s.Hub.Transact(context.Background(), expected, "test: seed canonical execution authority", func(worktree string) ([]string, error) {
		var latest model.ProjectConfiguration
		if err := readWorktreeJSON(worktree, s.projectConfigurationPath("example"), &latest); err != nil {
			return nil, err
		}
		latest.ExecutionModel = "train_v2"
		latest.Revision = configuration.Revision + 1
		if err := model.ValidateProjectConfiguration(latest); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, s.projectConfigurationPath("example"), latest); err != nil {
			return nil, err
		}
		return []string{s.projectConfigurationPath("example")}, nil
	})
	if err != nil {
		t.Fatalf("seed canonical execution configuration: %v", err)
	}
	return tx.After
}

func mustHubRevision(t *testing.T, s *Service) string {
	t.Helper()
	revision, err := s.hubRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return revision
}
