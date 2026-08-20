package service

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
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
	hubRevision = enableTrainV2ForTest(t, s, hubRevision)
	task, _, err := s.TaskAuthoringCreate(context.Background(), TaskAuthoringCreateInput{
		ProjectID:   "example",
		Title:       "Canonical task",
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
