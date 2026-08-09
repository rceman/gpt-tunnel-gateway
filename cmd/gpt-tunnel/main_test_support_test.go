package main

import (
	"context"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func planString(value string) *string { return &value }

func adoptTestWorkflowPolicyCLI(t *testing.T, s *service.Service, projectID, revision string) string {
	t.Helper()
	now := time.Now().UTC()
	policy := model.ProjectWorkflowPolicy{SchemaVersion: model.SchemaVersion, ProjectID: projectID, Revision: 1, WorkflowStage: model.WorkflowStageTransitionalMain, IntegrationBranch: "main", Agent: model.WorkflowPolicyAgent{WaitForCI: false}, CI: model.WorkflowPolicyCI{Task: model.WorkflowCIModeDisabled, TaskMerge: model.WorkflowCIModeObserve, Release: model.WorkflowCIModeObserve}, UpdatedBy: "test", UpdatedAt: now}
	path := hub.ProtocolRoot + "/projects/" + projectID + "/workflow-policy/current.json"
	result, err := s.Hub.Transact(context.Background(), revision, "test: install workflow policy", func(worktree string) ([]string, error) {
		return []string{path}, hub.WriteJSON(worktree, path, policy)
	})
	if err != nil {
		t.Fatal(err)
	}
	return result.After
}
