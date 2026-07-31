package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestReviewSnapshotCLISuccessRenderingPath(t *testing.T) {
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	output(model.ReviewSnapshot{SchemaVersion: 1, ReviewState: "active", NextAction: "wait_for_terminal"})
	_ = w.Close()
	os.Stdout = old
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"review_state": "active"`) {
		t.Fatalf("unexpected rendering: %s", data)
	}
}

func TestReviewSnapshotCLIErrorRenderingPathIsBounded(t *testing.T) {
	s := service.New(config.Config{})
	_, err := s.RunReviewSnapshot(context.Background(), "missing")
	if err == nil || !strings.Contains(err.Error(), "clone managed hub repository") {
		t.Fatalf("unexpected CLI error: %v", err)
	}
}

func TestAgentTailCLIRouteDefaultAndExplicitLines(t *testing.T) {
	hubBare, _, hubHead := testutil.RepoWithBareRemote(t)
	_, projectRoot, projectHead := testutil.RepoWithBareRemote(t)
	dir := t.TempDir()
	log := filepath.Join(dir, "args")
	script := filepath.Join(dir, "airelay")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \""+log+"\"\nprintf 'tail output\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	c := config.Config{SchemaVersion: 1, GatewayID: "test_gateway", ListenAddr: "127.0.0.1:8875", StateDir: filepath.Join(dir, "state"), MaxReadBytes: 1 << 20, MaxDiffBytes: 1 << 20, MaxListItems: 1000, DispatchTimeoutSeconds: 5, RunTimeoutSeconds: 60, AirelayCommand: script, Hub: config.HubConfig{RepositoryURL: hubBare, Branch: "main", AuthorName: "Gateway", AuthorEmail: "gateway@example.invalid"}, Projects: map[string]config.ProjectConfig{"example": {Root: projectRoot, Mirror: filepath.Join(dir, "mirror.git"), Remote: "origin", DefaultBranch: "main", AirelaySessionKey: "example_master"}}}
	s := service.New(c)
	project := model.Project{SchemaVersion: 1, ID: "example", RepositoryURL: "git@example.invalid:example.git", DefaultBranch: "main", WorkflowRepository: "rceman/gpt-review-planner", WorkflowCommit: strings.Repeat("a", 40), Status: "active"}
	registered, err := s.ProjectRegister(context.Background(), service.ProjectRegisterInput{Project: project, WriteOptions: service.WriteOptions{ExpectedHubRevision: hubHead}})
	if err != nil {
		t.Fatal(err)
	}
	task, created, err := s.TaskCreate(context.Background(), service.TaskCreateInput{ProjectID: "example", Title: "Tail", Objective: "Inspect tail", Branch: "feature/tail", BaseRevision: projectHead, AcceptanceCriteria: []string{"tail"}, CreatedBy: "test", WriteOptions: service.WriteOptions{ExpectedHubRevision: registered.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := s.PlanUpdate(context.Background(), service.PlanUpdateInput{ProjectID: "example", Summary: "Tail", Body: "Tail", ActiveTaskID: task.ID, UpdatedBy: "test", WriteOptions: service.WriteOptions{ExpectedHubRevision: created.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	agentRun, _, err := s.TaskDispatch(context.Background(), service.DispatchInput{TaskID: task.ID, WriteOptions: service.WriteOptions{ExpectedHubRevision: plan.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	run(context.Background(), s, []string{"agent-tail", agentRun.ID})
	_ = w.Close()
	os.Stdout = old
	data, _ := io.ReadAll(r)
	if string(data) != "tail output\n" {
		t.Fatalf("default CLI output=%q", data)
	}
	args, _ := os.ReadFile(log)
	if string(args) != "tail\nexample_master\n--lines\n4\n" {
		t.Fatalf("default CLI argv=%q", args)
	}
	run(context.Background(), s, []string{"agent-tail", agentRun.ID, "--lines", "9"})
	args, _ = os.ReadFile(log)
	if !strings.HasSuffix(string(args), "--lines\n9\n") {
		t.Fatalf("explicit CLI argv=%q", args)
	}
}
