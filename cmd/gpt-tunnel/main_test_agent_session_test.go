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

func TestDirectAgentSessionCLIParity(t *testing.T) {
	hubBare, _, hubHead := testutil.RepoWithBareRemote(t)
	_, projectRoot, _ := testutil.RepoWithBareRemote(t)
	dir := t.TempDir()
	script := filepath.Join(dir, "airelay")
	body := "#!/bin/sh\ncase \"$1\" in\nprompt) printf 'delivered\\n' ;;\ntail) printf 'one\\ntwo\\nthree\\nfour\\nfive\\n' ;;\nsession-status) printf 'Controller: reachable (5ms)\\nAirelay version: 0.1.54\\nProtocol version: 1\\nState: busy\\n' ;;\nesac\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	dirConfig := config.Config{SchemaVersion: 1, GatewayID: "test_gateway", ListenAddr: "127.0.0.1:8875", StateDir: filepath.Join(dir, "state"), MaxReadBytes: 1 << 20, MaxDiffBytes: 1 << 20, MaxListItems: 1000, DispatchTimeoutSeconds: 5, RunTimeoutSeconds: 60, AirelayCommand: script, Hub: config.HubConfig{RepositoryURL: hubBare, Branch: "main", AuthorName: "Gateway", AuthorEmail: "gateway@example.invalid"}, Projects: map[string]config.ProjectConfig{"example": {Root: projectRoot, Mirror: filepath.Join(dir, "mirror.git"), Remote: "origin", DefaultBranch: "main", AirelaySessionKey: "example_master"}}}
	s := service.New(dirConfig)
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
	capture := func(fn func()) string {
		t.Helper()
		old := os.Stdout
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		os.Stdout = w
		fn()
		_ = w.Close()
		os.Stdout = old
		data, err := io.ReadAll(r)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}

	send := capture(func() { agent(context.Background(), s, []string{"send", "example", "--text", "hello"}) })
	if !strings.Contains(send, `"delivered": true`) || strings.Contains(send, "session_key") {
		t.Fatalf("unexpected direct send output: %s", send)
	}
	tail := capture(func() { agent(context.Background(), s, []string{"tail", "example", "--lines", "4", "--skip", "1"}) })
	if !strings.Contains(tail, `"lines": 4`) || !strings.Contains(tail, `"skip": 1`) {
		t.Fatalf("unexpected direct tail output: %s", tail)
	}
	status := capture(func() { agent(context.Background(), s, []string{"status", "example"}) })
	if !strings.Contains(status, `"state": "running"`) || !strings.Contains(status, `"airelay_version": "0.1.54"`) {
		t.Fatalf("unexpected direct status output: %s", status)
	}
	task, created, err := s.TaskCreate(context.Background(), service.TaskCreateInput{ProjectID: "example", Title: "Resume", Objective: "Exercise CLI resume", Slug: "cli-resume", AcceptanceCriteria: []string{"resume"}, OperationClass: "implementation", CreatedBy: "test", WriteOptions: service.WriteOptions{ExpectedHubRevision: adopted.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := s.PlanUpdate(context.Background(), service.PlanUpdateInput{ProjectID: "example", Title: planString("Resume"), Summary: planString("Resume"), CurrentObjective: planString("Exercise CLI resume"), ActiveTaskID: &task.ID, UpdatedBy: "test", WriteOptions: service.WriteOptions{ExpectedHubRevision: created.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	agentRun, _, err := s.TaskDispatch(context.Background(), service.DispatchInput{TaskID: task.ID, WriteOptions: service.WriteOptions{ExpectedHubRevision: plan.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	compactionScript := "#!/bin/sh\ncase \"$1\" in\nsession-status) printf 'Controller: reachable\\nState: waiting\\n' ;;\ntail) printf 'Context compacted\\nAcknowledged; resuming\\nModel: test\\nContext window: 90%% remaining\\nWorkspace: /tmp/project\\nStatus: waiting\\n' ;;\nprompt) printf 'delivered\\n' ;;\nesac\n"
	if err := os.WriteFile(script, []byte(compactionScript), 0o700); err != nil {
		t.Fatal(err)
	}
	resume := capture(func() { run(context.Background(), s, []string{"resume", agentRun.ID}) })
	if !strings.Contains(resume, `"sent": true`) || strings.Contains(resume, "session_key") {
		t.Fatalf("unexpected CLI resume output: %s", resume)
	}
}
