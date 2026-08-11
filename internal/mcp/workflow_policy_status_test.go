package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func newWorkflowPolicyStatusService(t *testing.T) (*service.Service, string) {
	t.Helper()
	hubBare, _, hubHead := testutil.RepoWithBareRemote(t)
	_, projectRoot, _ := testutil.RepoWithBareRemote(t)
	dir := t.TempDir()
	airelay := filepath.Join(dir, "airelay")
	if err := os.WriteFile(airelay, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	c := config.Config{
		SchemaVersion:          1,
		GatewayID:              "test_gateway",
		ListenAddr:             "127.0.0.1:8875",
		StateDir:               filepath.Join(dir, "state"),
		MaxReadBytes:           1 << 20,
		MaxDiffBytes:           1 << 20,
		MaxListItems:           1000,
		DispatchTimeoutSeconds: 5,
		RunTimeoutSeconds:      60,
		AirelayCommand:         airelay,
		Hub: config.HubConfig{
			RepositoryURL: hubBare,
			Branch:        "main",
			AuthorName:    "Gateway",
			AuthorEmail:   "gateway@example.invalid",
		},
		Projects: map[string]config.ProjectConfig{
			"example": {
				Root:              projectRoot,
				Mirror:            filepath.Join(dir, "mirror.git"),
				Remote:            "origin",
				DefaultBranch:     "main",
				AirelaySessionKey: "example_master",
			},
		},
	}
	s := service.New(c)
	project := model.Project{
		SchemaVersion:      1,
		ID:                 "example",
		RepositoryURL:      "git@example.invalid:example.git",
		DefaultBranch:      "main",
		WorkflowRepository: "rceman/gpt-review-planner",
		WorkflowCommit:     "b1a45b1e9475ab29dfd3e84d523b70897c7b8918",
		Status:             "active",
	}
	registered, err := s.ProjectRegister(context.Background(), service.ProjectRegisterInput{Project: project, WriteOptions: service.WriteOptions{ExpectedHubRevision: hubHead}})
	if err != nil {
		t.Fatal(err)
	}
	policy := model.ProjectWorkflowPolicy{
		SchemaVersion:     model.SchemaVersion,
		ProjectID:         "example",
		Revision:          1,
		WorkflowStage:     model.WorkflowStageTransitionalMain,
		IntegrationBranch: "main",
		Agent:             model.WorkflowPolicyAgent{WaitForCI: false},
		CI: model.WorkflowPolicyCI{
			Task:      model.WorkflowCIModeDisabled,
			TaskMerge: model.WorkflowCIModeObserve,
			Release:   model.WorkflowCIModeRequire,
		},
		UpdatedBy: "test",
		UpdatedAt: time.Now().UTC(),
	}
	_, operation, err := s.ProjectWorkflowPolicyAdopt(service.WithPlannerWorkflowPolicyAuthority(context.Background()), service.ProjectWorkflowPolicyInput{Policy: policy, WriteOptions: service.WriteOptions{ExpectedHubRevision: registered.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	return s, operation.Hub.After
}

func mutateWorkflowPolicyStatusFixture(t *testing.T, s *service.Service, revision, state string) string {
	t.Helper()
	path := "gpt-tunnel/v1/projects/example/configuration/current.json"
	result, err := s.Hub.Transact(context.Background(), revision, "test: workflow policy status fixture", func(worktree string) ([]string, error) {
		if state == "missing" {
			if err := os.Remove(filepath.Join(worktree, filepath.FromSlash(path))); err != nil {
				return nil, err
			}
		} else {
			if err := hub.WriteText(worktree, path, `{"schema_version":1,"project_id":"example"}`); err != nil {
				return nil, err
			}
		}
		return []string{path}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result.After
}

func readAndValidateProjectStatus(t *testing.T, s *service.Service) map[string]any {
	t.Helper()
	response := callMCP(t, &Server{Service: s}, []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"project_status","arguments":{"project_id":"example"}}}`))
	result, ok := response["result"].(map[string]any)
	if !ok || result["isError"] != false {
		t.Fatalf("project_status failed: %#v", response)
	}
	structured, ok := result["structuredContent"].(map[string]any)
	if !ok {
		t.Fatalf("project_status omitted structuredContent: %#v", response)
	}
	wire, err := json.Marshal(structured)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(wire, &decoded); err != nil {
		t.Fatal(err)
	}
	if err := validateOutputValue(toolOutputSchemas["project_status"], decoded); err != nil {
		t.Fatalf("serialized project_status violates declared MCP output schema: %v\n%s", err, wire)
	}
	return decoded
}

func TestProjectStatusMCPOutputMatchesWorkflowPolicyStateMatrix(t *testing.T) {
	for _, state := range []string{"adopted", "missing", "invalid"} {
		t.Run(state, func(t *testing.T) {
			s, revision := newWorkflowPolicyStatusService(t)
			if state != "adopted" {
				revision = mutateWorkflowPolicyStatusFixture(t, s, revision, state)
			}
			status := readAndValidateProjectStatus(t, s)
			policy := status["workflow_policy"].(map[string]any)
			if policy["state"] != state {
				t.Fatalf("workflow policy state=%v want=%s", policy["state"], state)
			}
			gates, ok := policy["gates"].([]any)
			if !ok || len(gates) != 3 || gates[0] != model.WorkflowGateFormat || gates[1] != model.WorkflowGateCheck || gates[2] != model.WorkflowGateTest {
				t.Fatalf("%s policy emitted non-effective gates: %#v", state, policy["gates"])
			}
			ci := policy["ci"].(map[string]any)
			if state == "adopted" {
				if policy["revision"] != float64(1) || policy["workflow_stage"] != model.WorkflowStageTransitionalMain || policy["integration_branch"] != "main" || ci["task"] != string(model.WorkflowCIModeDisabled) || ci["task_merge"] != string(model.WorkflowCIModeObserve) || ci["release"] != string(model.WorkflowCIModeRequire) {
					t.Fatalf("adopted policy projection changed: %#v", policy)
				}
			} else {
				for _, field := range []string{"task", "task_merge", "release"} {
					if ci[field] != string(model.WorkflowCIModeDisabled) {
						t.Fatalf("%s policy emitted non-fail-closed CI mode: %#v", state, policy)
					}
				}
				if policy["corrective_action"] == "none" || len(policy["conflicts"].([]any)) == 0 {
					t.Fatalf("%s policy omitted corrective semantics: %#v", state, policy)
				}
			}
		})
	}
}
