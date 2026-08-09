package service

import (
	"context"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestTaskMarkMergedDoesNotMutateWhenRemoteReceiptIsUnavailable(t *testing.T) {
	s, revision, projectHead := testService(t)
	ctx := context.Background()
	task, created, err := s.TaskCreate(ctx, TaskCreateInput{
		ProjectID:          "example",
		Title:              "Merge receipt",
		Objective:          "Require remote receipt.",
		Slug:               "merge-receipt",
		AcceptanceCriteria: []string{"remote"},
		OperationClass:     "implementation",
		CreatedBy:          "test",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: revision,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	reviewed := projectHead
	state := model.TaskState{SchemaVersion: model.SchemaVersion, TaskID: task.ID, TaskSHA256: task.SHA256, Status: "merge_ready", ReviewedHead: reviewed, UpdatedAt: time.Now().UTC()}
	revision = installTaskLifecycleState(t, s, task, state, created.Hub.After)
	project := s.Config.Projects[task.ProjectID]
	if err := s.Git.Refresh(ctx, project); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, project.Mirror, "update-ref", "refs/heads/"+task.Branch, reviewed)
	testutil.Git(t, project.Mirror, "update-ref", "refs/heads/develop", reviewed)
	if _, err := s.TaskMarkMerged(ctx, TaskMarkMergedInput{
		TaskID:          task.ID,
		IntegrationHead: reviewed,
		WriteOptions: WriteOptions{
			ExpectedHubRevision: revision,
		},
	}); err == nil {
		t.Fatal("accepted merged receipt without exact remote branches")
	}
	current, err := s.Hub.RemoteRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if current != revision {
		t.Fatalf("rejected merged receipt changed hub revision: got %s want %s", current, revision)
	}
}
