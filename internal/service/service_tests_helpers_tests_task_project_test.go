package service

import (
	"context"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestTaskCreateRequiresDurableProjectRecordWithoutGitLookup(t *testing.T) {
	s, revision, _ := testService(t)
	ctx := context.Background()
	now := time.Now().UTC()
	identifiers := model.ProjectIdentifiers{SchemaVersion: model.SchemaVersion, ProjectID: "orphan", ProjectCode: "ORP", NextTaskNumber: 1, NextADRNumber: 1}
	policy := model.ProjectWorkflowPolicy{SchemaVersion: model.SchemaVersion, ProjectID: "orphan", Revision: 1, WorkflowStage: model.WorkflowStageTransitionalMain, IntegrationBranch: "main", Agent: model.WorkflowPolicyAgent{WaitForCI: false}, CI: model.WorkflowPolicyCI{Task: model.WorkflowCIModeDisabled, TaskMerge: model.WorkflowCIModeObserve, Release: model.WorkflowCIModeObserve}, UpdatedBy: "test", UpdatedAt: now}
	seeded, err := s.Hub.Transact(ctx, revision, "test: seed orphan project metadata", func(worktree string) ([]string, error) {
		paths := []string{s.projectIdentifiersPath("orphan"), s.workflowPolicyPath("orphan")}
		if err := hub.WriteJSON(worktree, paths[0], identifiers); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, paths[1], policy); err != nil {
			return nil, err
		}
		return paths, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.TaskCreate(ctx, TaskCreateInput{
		ProjectID:          "orphan",
		Slug:               "missing-project",
		Title:              "Missing project",
		Objective:          "Reject an orphan project record.",
		AcceptanceCriteria: []string{"reject"},
		OperationClass:     "implementation",
		CreatedBy:          "test",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: seeded.After,
		},
	}); err == nil {
		t.Fatal("task creation accepted metadata without a durable project record")
	}
	if got, err := s.Hub.RemoteRevision(ctx); err != nil || got != seeded.After {
		t.Fatalf("rejected orphan task creation mutated Hub: got=%s want=%s err=%v", got, seeded.After, err)
	}
}
