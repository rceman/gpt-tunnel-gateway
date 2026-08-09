package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestTaskLifecycleDeferCLIRoute(t *testing.T) {
	hubBare, _, hubHead := testutil.RepoWithBareRemote(t)
	_, projectRoot, _ := testutil.RepoWithBareRemote(t)
	dir := t.TempDir()
	c := config.Config{SchemaVersion: 1, GatewayID: "test_gateway", ListenAddr: "127.0.0.1:8875", StateDir: filepath.Join(dir, "state"), MaxReadBytes: 1 << 20, MaxDiffBytes: 1 << 20, MaxListItems: 1000, Hub: config.HubConfig{RepositoryURL: hubBare, Branch: "main", AuthorName: "Gateway", AuthorEmail: "gateway@example.invalid"}, Projects: map[string]config.ProjectConfig{"example": {Root: projectRoot, Mirror: filepath.Join(dir, "mirror.git"), Remote: "origin", DefaultBranch: "main", AirelaySessionKey: "example_master"}}}
	s := service.New(c)
	project := model.Project{SchemaVersion: 1, ID: "example", RepositoryURL: "git@example.invalid:example.git", DefaultBranch: "main", WorkflowRepository: "rceman/gpt-review-planner", WorkflowCommit: strings.Repeat("a", 40), Status: "active"}
	registered, err := s.ProjectRegister(context.Background(), service.ProjectRegisterInput{Project: project, WriteOptions: service.WriteOptions{ExpectedHubRevision: hubHead}})
	if err != nil {
		t.Fatal(err)
	}
	policyRevision := adoptTestWorkflowPolicyCLI(t, s, "example", registered.Hub.After)
	_, adopted, err := s.ProjectIdentifiersAdopt(context.Background(), service.ProjectIdentifiersAdoptInput{ProjectID: "example", ProjectCode: "EXM", WriteOptions: service.WriteOptions{ExpectedHubRevision: policyRevision}})
	if err != nil {
		t.Fatal(err)
	}
	taskRecord, created, err := s.TaskCreate(context.Background(), service.TaskCreateInput{ProjectID: "example", Title: "CLI lifecycle", Objective: "Exercise the defer command.", Slug: "cli-lifecycle", AcceptanceCriteria: []string{"state"}, OperationClass: "implementation", CreatedBy: "test", WriteOptions: service.WriteOptions{ExpectedHubRevision: adopted.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	state := model.TaskState{SchemaVersion: model.SchemaVersion, TaskID: taskRecord.ID, TaskSHA256: taskRecord.SHA256, Status: "merge_ready", ReviewedHead: strings.Repeat("b", 40), UpdatedAt: time.Now().UTC()}
	statePath := hub.ProtocolRoot + "/projects/example/tasks/" + taskRecord.ID + ".state.json"
	installed, err := s.Hub.Transact(context.Background(), created.Hub.After, "test: install CLI lifecycle state", func(worktree string) ([]string, error) {
		return []string{statePath}, hub.WriteJSON(worktree, statePath, state)
	})
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	task(context.Background(), s, []string{"defer", taskRecord.ID, "--reason", "later integration", "--expected-hub-revision", installed.After})
	_ = w.Close()
	os.Stdout = old
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"status": "deferred"`) {
		t.Fatalf("unexpected CLI lifecycle output: %s", data)
	}
	read, err := s.TaskReadRecord(context.Background(), taskRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	if read.State.Status != "deferred" || read.State.DeferredReason != "later integration" {
		t.Fatalf("CLI did not persist defer state: %#v", read.State)
	}
}
