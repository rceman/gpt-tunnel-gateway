package service

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestCheckSessionAvailableSkipsLegacyProjectBeforeTrainScan(t *testing.T) {
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
	projects, err := s.ProjectList(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) == 0 || projects[0].ID != legacyID {
		t.Fatalf("legacy project was not ordered first: %#v", projects)
	}
	if err := s.checkSessionAvailableForTrainAttempt(context.Background(), "legacy_master", "GTW-TRN999"); err != nil {
		t.Fatalf("legacy project aborted session scan: %v", err)
	}
	_ = registered
}

func TestCheckSessionAvailableRejectsActiveTrainSessionCollision(t *testing.T) {
	s, hubRevision, _ := testService(t)
	hubRevision = enableTrainV2ForTest(t, s, hubRevision)
	task, hubRevision := readyTrainTaskForTest(t, s, hubRevision, "Session collision")
	train, operation, err := s.TrainV2Create(context.Background(), TrainV2CreateInput{
		ProjectID: "example",
		TaskIDs:   []string{task.ID},
		CreatedBy: "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: hubRevision,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	started, err := s.TrainV2Start(context.Background(), TrainV2StartInput{
		ProjectID: "example",
		TrainID:   train.ID,
		StartedBy: "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: operation.Hub.After,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	err = s.checkSessionAvailableForTrainAttempt(context.Background(), started.Attempt.AirelaySessionKey, "GTW-TRN999")
	if err == nil || !strings.Contains(err.Error(), "already owns the project session") {
		t.Fatalf("active Train session collision was not rejected: %v", err)
	}
}
