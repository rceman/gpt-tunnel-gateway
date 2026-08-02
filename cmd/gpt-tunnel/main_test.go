package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func planString(value string) *string { return &value }

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
	if err == nil || err.Error() != "read-only hub lock unavailable" || strings.Contains(err.Error(), "state") || strings.Contains(err.Error(), "lock/") {
		t.Fatalf("unexpected CLI error: %v", err)
	}
}

func TestPlanCutoverCLIRouteUsesCurrentFixture(t *testing.T) {
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
	fixture, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "plan_v1_current.json"))
	if err != nil {
		t.Fatal(err)
	}
	var legacy map[string]any
	if err := json.Unmarshal(fixture, &legacy); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Hub.Transact(context.Background(), registered.Hub.After, "test: install current plan fixture", func(w string) ([]string, error) {
		path := hub.ProtocolRoot + "/projects/example/plan/current.json"
		if err := hub.WriteJSON(w, path, legacy); err != nil {
			return nil, err
		}
		return []string{path}, nil
	}); err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(dir, "cutover.json")
	if err := os.WriteFile(input, []byte(`{"project_id":"example","updated_by":"owner"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	oldStdout := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = writer
	plan(context.Background(), s, []string{"cutover", "--file", input})
	_ = writer.Close()
	os.Stdout = oldStdout
	outputBytes, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(outputBytes), `"status": "cut over"`) {
		t.Fatalf("CLI cutover output=%s", outputBytes)
	}
	planResult, err := s.PlanRead(context.Background(), "example")
	if err != nil || strings.Join(planResult.Queue, ",") != "P0,P1,P2" {
		t.Fatalf("CLI did not perform exact cutover: err=%v plan=%#v", err, planResult)
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
	plan, err := s.PlanUpdate(context.Background(), service.PlanUpdateInput{ProjectID: "example", Title: planString("Tail"), Summary: planString("Tail"), CurrentObjective: planString("Tail"), ActiveTaskID: planString(task.ID), UpdatedBy: "test", WriteOptions: service.WriteOptions{ExpectedHubRevision: created.Hub.After}})
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

func TestAgentTailCLICommandPathErrorsAreBounded(t *testing.T) {
	hubBare, _, hubHead := testutil.RepoWithBareRemote(t)
	_, projectRoot, projectHead := testutil.RepoWithBareRemote(t)
	dir := t.TempDir()
	log := filepath.Join(dir, "args")
	script := filepath.Join(dir, "airelay")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" >> \""+log+"\"\nprintf 'tail output\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	c := config.Config{SchemaVersion: 1, GatewayID: "test_gateway", ListenAddr: "127.0.0.1:8875", StateDir: filepath.Join(dir, "state"), MaxReadBytes: 1 << 20, MaxDiffBytes: 1 << 20, MaxListItems: 1000, DispatchTimeoutSeconds: 5, RunTimeoutSeconds: 60, AirelayCommand: script, Controller: config.ControllerConfig{TunnelHealthListenAddr: "127.0.0.1:8876"}, Hub: config.HubConfig{RepositoryURL: hubBare, Branch: "main", AuthorName: "Gateway", AuthorEmail: "gateway@example.invalid"}, Projects: map[string]config.ProjectConfig{"example": {Root: projectRoot, Mirror: filepath.Join(dir, "mirror.git"), Remote: "origin", DefaultBranch: "main", AirelaySessionKey: "example_master"}}}
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
	plan, err := s.PlanUpdate(context.Background(), service.PlanUpdateInput{ProjectID: "example", Title: planString("Tail"), Summary: planString("Tail"), CurrentObjective: planString("Tail"), ActiveTaskID: planString(task.ID), UpdatedBy: "test", WriteOptions: service.WriteOptions{ExpectedHubRevision: created.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	agentRun, _, err := s.TaskDispatch(context.Background(), service.DispatchInput{TaskID: task.ID, WriteOptions: service.WriteOptions{ExpectedHubRevision: plan.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "config.json")
	configData, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, configData, 0o600); err != nil {
		t.Fatal(err)
	}
	cliPath := filepath.Join(dir, "gpt-tunnel")
	build := exec.Command("go", "build", "-o", cliPath, ".")
	build.Dir, err = os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
	runCLI := func(args ...string) (string, string, error) {
		cmd := exec.Command(cliPath, append([]string{"run", "agent-tail", agentRun.ID}, args...)...)
		cmd.Env = append(os.Environ(), "GPT_TUNNEL_CONFIG="+configPath)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		return stdout.String(), stderr.String(), err
	}
	stdout, stderr, err := runCLI()
	if err != nil || stdout != "tail output\n" || stderr != "" {
		t.Fatalf("default CLI result stdout=%q stderr=%q err=%v", stdout, stderr, err)
	}
	stdout, stderr, err = runCLI("--lines", "9")
	if err != nil || stdout != "tail output\n" || stderr != "" {
		t.Fatalf("explicit CLI result stdout=%q stderr=%q err=%v", stdout, stderr, err)
	}
	args, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(args), "tail\nexample_master\n--lines\n9\n") {
		t.Fatalf("explicit CLI argv=%q", args)
	}
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf 'tail failure output\\n'\nprintf 'example_master CONTROL_PLANE_API_KEY=secret-marker\\n' >&2\nexit 23\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	_, stderr, err = runCLI("--lines", "9")
	if err == nil || !strings.Contains(stderr, "Airelay tail failed") || len(stderr) > 512 {
		t.Fatalf("nonzero CLI error stderr=%q err=%v", stderr, err)
	}
	for _, forbidden := range []string{"example_master", "CONTROL_PLANE_API_KEY", "secret-marker"} {
		if strings.Contains(stderr, forbidden) {
			t.Fatalf("CLI error leaked %q: %q", forbidden, stderr)
		}
	}
	_, stderr, err = runCLI("--lines", "201")
	if err == nil || !strings.Contains(stderr, "invalid tail line count") || len(stderr) > 512 {
		t.Fatalf("invalid CLI error stderr=%q err=%v", stderr, err)
	}
	for _, forbidden := range []string{"example_master", "CONTROL_PLANE_API_KEY", "secret-marker"} {
		if strings.Contains(stderr, forbidden) {
			t.Fatalf("invalid CLI error leaked %q: %q", forbidden, stderr)
		}
	}
}

func TestDirectAgentSessionCLIParity(t *testing.T) {
	hubBare, _, hubHead := testutil.RepoWithBareRemote(t)
	_, projectRoot, projectHead := testutil.RepoWithBareRemote(t)
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
	task, created, err := s.TaskCreate(context.Background(), service.TaskCreateInput{ProjectID: "example", Title: "Resume", Objective: "Exercise CLI resume", Branch: "feature/cli-resume", BaseRevision: projectHead, AcceptanceCriteria: []string{"resume"}, CreatedBy: "test", WriteOptions: service.WriteOptions{ExpectedHubRevision: registered.Hub.After}})
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
