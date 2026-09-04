package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func installExecutionSessionFixture(t *testing.T, s *Service, promptLog string) {
	t.Helper()
	dir := filepath.Dir(s.Config.AirelayCommand)
	active := filepath.Join(dir, "execution-active.json")
	history := filepath.Join(dir, "execution-history.json")
	script := fmt.Sprintf("#!/bin/sh\nactive='%s'\nhistory='%s'\ncase \"$1\" in\nsessions) if [ -f \"$active\" ]; then cat \"$active\"; else printf '[]'; fi ;;\nhistory) if [ -f \"$history\" ]; then cat \"$history\"; else printf '[]'; fi ;;\nsession-status) printf '{\"sessionKey\":\"%%s\",\"profile\":\"coding\",\"controllerReachable\":true,\"state\":\"idle\"}' \"$2\" ;;\nstatus) printf 'Controller: reachable\\nState: idle\\n' ;;\nstart) key=\"$4\"; cwd=\"$(pwd)\"; printf '[{\"sessionKey\":\"%%s\",\"profile\":\"%%s\",\"cwd\":\"%%s\"}]' \"$key\" \"$2\" \"$cwd\" > \"$active\"; printf '[{\"sessionKey\":\"%%s\",\"profile\":\"%%s\",\"invocationCwd\":\"%%s\"}]' \"$key\" \"$2\" \"$cwd\" > \"$history\" ;;\nprompt) printf 'prompt\\n' >> '%s' ;;\n*) exit 0 ;;\nesac\n", active, history, promptLog)
	if err := os.WriteFile(s.Config.AirelayCommand, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
}

func installServiceExecutionSessionFixture(t *testing.T, s *Service, promptLog string) {
	t.Helper()
	dir := filepath.Dir(s.Config.AirelayCommand)
	active := filepath.Join(dir, "service-execution-active.json")
	history := filepath.Join(dir, "service-execution-history.json")
	script := fmt.Sprintf(`#!/bin/sh
active='%s'
history='%s'
case "$1" in
sessions) if [ -f "$active" ]; then cat "$active"; else printf '[]'; fi ;;
history) if [ -f "$history" ]; then cat "$history"; else printf '[]'; fi ;;
session-status) if [ "$3" = --json ]; then printf '{"sessionKey":"%%s","profile":"coding","controllerReachable":true,"state":"idle"}' "$2"; else printf 'Controller: reachable\nState: idle\n'; fi ;;
start) key="$4"; cwd="$(pwd)"; printf '[{"sessionKey":"%%s","profile":"%%s","cwd":"%%s"}]' "$key" "$2" "$cwd" > "$active"; printf '[{"sessionKey":"%%s","profile":"%%s","invocationCwd":"%%s"}]' "$key" "$2" "$cwd" > "$history" ;;
prompt) printf 'prompt\n' >> '%s' ;;
*) exit 0 ;;
esac
`, active, history, promptLog)
	if err := os.WriteFile(s.Config.AirelayCommand, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
}

func planString(value string) *string { return &value }

func testServiceWithoutIdentifiers(t *testing.T) (*Service, string, string) {
	t.Helper()
	hubBare, _, hubHead := testutil.RepoWithBareRemote(t)
	_, projectRoot, projectHead := testutil.RepoWithBareRemote(t)
	dir := t.TempDir()
	airelay := filepath.Join(dir, "airelay")
	if err := os.WriteFile(airelay, []byte("#!/bin/sh\ncase \"$1\" in\nsession-status) printf 'Controller: reachable\\nState: idle\\n' ;;\ntail) printf 'idle\\n' ;;\n*) exit 0 ;;\nesac\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	c := config.Config{
		SchemaVersion: 1, GatewayID: "test_gateway", ListenAddr: "127.0.0.1:8875",
		StateDir: filepath.Join(dir, "state"), MaxReadBytes: 1 << 20, MaxDiffBytes: 1 << 20,
		MaxListItems: 1000, DispatchTimeoutSeconds: 5, RunTimeoutSeconds: 60, AirelayCommand: airelay,
		Hub:           config.HubConfig{RepositoryURL: hubBare, Branch: "main", AuthorName: "Gateway", AuthorEmail: "gateway@example.invalid"},
		AgentBindings: map[string]config.AgentBinding{"watcher-example": {SessionKey: "watcher_master"}},
		Projects:      map[string]config.ProjectConfig{"example": {Root: projectRoot, Mirror: filepath.Join(dir, "mirror.git"), Remote: "origin", DefaultBranch: "main", AirelaySessionKey: "example_master", Watcher: config.WatcherSettings{AgentID: "watcher-example"}}},
	}
	s := New(c)
	s.gateExecutor = func(_ context.Context, _ string, names []string) ([]model.CompletionGateResult, error) {
		out := make([]model.CompletionGateResult, len(names))
		for i, name := range names {
			out[i] = model.CompletionGateResult{ID: name, ExitCode: 0}
		}
		return out, nil
	}
	s.gateExecutorWithProjectCommands = func(ctx context.Context, root string, names []string, _ model.ProjectGateCommands, _ string) ([]model.CompletionGateResult, error) {
		return s.gateExecutor(ctx, root, names)
	}
	s.formatExecutor = func(context.Context, string, []string) error { return nil }
	project := model.Project{SchemaVersion: 1, ID: "example", RepositoryURL: "git@example.invalid:example.git", DefaultBranch: "main", WorkflowRepository: "rceman/gpt-review-planner", WorkflowCommit: "b1a45b1e9475ab29dfd3e84d523b70897c7b8918", Status: "active"}
	reg, err := s.ProjectRegister(context.Background(), ProjectRegisterInput{
		Project: project,
		WriteOptions: WriteOptions{
			ExpectedHubRevision: hubHead,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	policy := model.ProjectWorkflowPolicy{SchemaVersion: model.SchemaVersion, ProjectID: project.ID, Revision: 1, WorkflowStage: model.WorkflowStageTransitionalMain, IntegrationBranch: "main", Agent: model.WorkflowPolicyAgent{WaitForCI: false}, CI: model.WorkflowPolicyCI{Task: model.WorkflowCIModeDisabled, TaskMerge: model.WorkflowCIModeObserve, Release: model.WorkflowCIModeObserve}, UpdatedBy: "test", UpdatedAt: now}
	_, adopted, err := s.ProjectWorkflowPolicyAdopt(trustedWorkflowPolicyContext(context.Background(), "planner"), ProjectWorkflowPolicyInput{
		Policy: policy,
		WriteOptions: WriteOptions{
			ExpectedHubRevision: reg.Hub.After,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return s, adopted.Hub.After, projectHead
}

func testService(t *testing.T) (*Service, string, string) {
	s, revision, projectHead := testServiceWithoutIdentifiers(t)
	adopted, result, err := s.ProjectIdentifiersAdopt(context.Background(), ProjectIdentifiersAdoptInput{
		ProjectID:   "example",
		ProjectCode: "EXM",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: revision,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if adopted.NextTaskNumber != 1 || result.Status != "adopted" {
		t.Fatalf("unexpected adopted identifiers: %#v %#v", adopted, result)
	}
	if err := os.WriteFile(s.Config.AirelayCommand, []byte("#!/bin/sh\ncase \"$1\" in\nsession-status) printf 'Controller: reachable\\nState: idle\\n' ;;\ntail) printf 'idle\\n' ;;\nprompt) exit 0 ;;\n*) exit 0 ;;\nesac\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	s.Config.AgentBindings[config.ProjectAgentBindingKey("example", "coder-example")] = config.AgentBinding{SessionKey: "example_master"}
	now := time.Now().UTC()
	revision = result.Hub.After
	for _, agent := range []model.Agent{
		{SchemaVersion: model.AgentSchemaVersion, ProjectID: "example", AgentID: "coder-example", Role: model.AgentRoleCoding, Enabled: true, RecommendedReasoning: model.ReasoningHigh, CreatedAt: now, UpdatedAt: now},
		{SchemaVersion: model.AgentSchemaVersion, ProjectID: "example", AgentID: "watcher-example", Role: model.AgentRoleWatcher, Enabled: true, RecommendedReasoning: model.ReasoningBestAvailable, CreatedAt: now, UpdatedAt: now},
	} {
		path := s.agentPath("example", agent.AgentID)
		tx, err := s.Hub.Transact(context.Background(), revision, "test: seed agent "+agent.AgentID, func(worktree string) ([]string, error) {
			if err := hub.WriteJSON(worktree, path, agent); err != nil {
				return nil, err
			}
			return []string{path}, nil
		})
		if err != nil {
			t.Fatalf("seed test agent %s: %v", agent.AgentID, err)
		}
		revision = tx.After
	}
	return s, revision, projectHead
}

func TestValidateConfiguredProjectRecordsRejectsMissingDurableRecord(t *testing.T) {
	s, _, _ := testService(t)
	s.Config.Projects["missing"] = s.Config.Projects["example"]
	if err := s.ValidateConfiguredProjectRecords(context.Background()); err == nil {
		t.Fatal("missing durable project record was accepted")
	}
}

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
