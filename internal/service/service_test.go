package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func testService(t *testing.T) (*Service, string, string) {
	_, hubWork, hubHead := testutil.RepoWithBareRemote(t)
	_, projectWork, projectHead := testutil.RepoWithBareRemote(t)
	dir := t.TempDir()
	fake := filepath.Join(dir, "airelay")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	c := config.Config{SchemaVersion: 1, GatewayID: "test_gateway", ListenAddr: "127.0.0.1:8875", StateDir: filepath.Join(dir, "state"), MaxReadBytes: 1 << 20, MaxDiffBytes: 1 << 20, MaxListItems: 1000, DispatchTimeoutSeconds: 5, RunTimeoutSeconds: 60, AirelayCommand: fake, Hub: config.HubConfig{Root: hubWork, Remote: "origin", Branch: "main", AuthorName: "Gateway", AuthorEmail: "gateway@example.invalid"}, Projects: map[string]config.ProjectConfig{"example": {Root: projectWork, Mirror: filepath.Join(dir, "mirror.git"), Remote: "origin", DefaultBranch: "main", AirelaySessionKey: "example_master"}}}
	s := New(c)
	project := model.Project{SchemaVersion: 1, ID: "example", RepositoryURL: "git@example.invalid:example.git", DefaultBranch: "main", WorkflowRepository: "rceman/gpt-review-planner", WorkflowCommit: "b1a45b1e9475ab29dfd3e84d523b70897c7b8918", Status: "active"}
	reg, err := s.ProjectRegister(context.Background(), ProjectRegisterInput{Project: project, WriteOptions: WriteOptions{ExpectedHubRevision: hubHead}})
	if err != nil {
		t.Fatal(err)
	}
	return s, reg.Hub.After, projectHead
}
func TestTaskPlanDispatchReadFinalize(t *testing.T) {
	s, hubRev, projectHead := testService(t)
	ctx := context.Background()
	task, create, err := s.TaskCreate(ctx, TaskCreateInput{ProjectID: "example", Title: "Implement feature", Objective: "Implement exact behavior.", Branch: "feature/example", BaseRevision: projectHead, AcceptanceCriteria: []string{"feature works"}, Constraints: []string{"no redesign"}, RequiredGates: []string{"go test ./..."}, CreatedBy: "gpt", WriteOptions: WriteOptions{ExpectedHubRevision: hubRev}})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := s.PlanUpdate(ctx, PlanUpdateInput{ProjectID: "example", Summary: "Implement feature", Body: "Execute the prepared task.", ActiveTaskID: task.ID, UpdatedBy: "gpt", WriteOptions: WriteOptions{ExpectedHubRevision: create.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	run, dispatch, err := s.TaskDispatch(ctx, DispatchInput{TaskID: task.ID, WriteOptions: WriteOptions{ExpectedHubRevision: plan.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != "awaiting_result" {
		t.Fatalf("status=%s", run.Status)
	}
	packet, err := s.TaskRead(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if packet.Run.ID != run.ID || packet.FinalizeCommand == "" {
		t.Fatalf("bad packet: %#v", packet)
	}
	project := s.Config.Projects["example"]
	if err := os.WriteFile(filepath.Join(project.Root, "feature.txt"), []byte("done\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, project.Root, "add", "feature.txt")
	testutil.Git(t, project.Root, "commit", "-m", "implement feature")
	head := strings.TrimSpace(testutil.Git(t, project.Root, "rev-parse", "HEAD"))
	result := model.AgentResult{SchemaVersion: 1, TaskID: task.ID, TaskSHA256: task.SHA256, RunID: run.ID, Status: "succeeded", Summary: "Implemented.", Commits: []string{head}, ChangedFiles: []string{"feature.txt"}, Commands: []model.CommandResult{{Command: "go test ./...", ExitCode: 0, Result: "passed"}}, AcceptanceCoverage: []string{"feature works"}, FinishedAt: time.Now().UTC()}
	evidence := model.Evidence{SchemaVersion: 1, TaskID: task.ID, RunID: run.ID, ProjectHead: head, Branch: "feature/example", WorktreeClean: true, RecordedAt: time.Now().UTC()}
	if err := fsutil.WriteJSONAtomic(run.ResultPath, result, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := fsutil.WriteJSONAtomic(run.EvidencePath, evidence, 0o600); err != nil {
		t.Fatal(err)
	}
	report, final, err := s.RunFinalize(ctx, FinalizeInput{RunID: run.ID, WriteOptions: WriteOptions{ExpectedHubRevision: dispatch.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "succeeded" || final.Status != "TASK_FINALIZED" {
		t.Fatalf("bad final: %#v %#v", report, final)
	}
}
