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
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
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

func TestGitcmdResolvesManagedProjectAfterServiceConstruction(t *testing.T) {
	_, projectRoot, _ := testutil.RepoWithBareRemote(t)
	stateDir := t.TempDir()
	s := service.New(config.Config{StateDir: stateDir, MaxReadBytes: 1 << 20, MaxDiffBytes: 1 << 20, MaxListItems: 1000})
	current := config.EmptyManagedProjectRegistry()
	digest, err := current.Digest()
	if err != nil {
		t.Fatal(err)
	}
	next := config.ManagedProjectRegistry{SchemaVersion: config.ManagedProjectRegistrySchemaVersion, Revision: 1, Projects: map[string]config.ManagedProjectEntry{"managed": {Root: projectRoot, RepositoryURL: "git@example.invalid:managed.git", Remote: "origin", DefaultBranch: "main", AirelaySessionKey: "managed_master"}}}
	if _, err := config.WriteManagedProjectRegistry(stateDir, digest, next); err != nil {
		t.Fatal(err)
	}

	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	gitcmd(context.Background(), s, []string{"worktree-status", "managed"})
	_ = w.Close()
	os.Stdout = oldStdout
	output, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(output), `"branch"`) || !strings.Contains(string(output), `"clean"`) {
		t.Fatalf("managed CLI Git route returned unexpected output: %s", output)
	}
}

func TestGitcmdFailsClosedForMalformedManagedRegistry(t *testing.T) {
	if os.Getenv("GPT_TUNNEL_MALFORMED_REGISTRY_CHILD") == "1" {
		stateDir := os.Getenv("GPT_TUNNEL_MALFORMED_REGISTRY_STATE")
		s := service.New(config.Config{StateDir: stateDir})
		gitcmd(context.Background(), s, []string{"worktree-status", "managed"})
		return
	}
	stateDir := t.TempDir()
	if err := os.WriteFile(config.ManagedProjectRegistryPath(stateDir), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run", "^TestGitcmdFailsClosedForMalformedManagedRegistry$")
	cmd.Env = append(os.Environ(), "GPT_TUNNEL_MALFORMED_REGISTRY_CHILD=1", "GPT_TUNNEL_MALFORMED_REGISTRY_STATE="+stateDir)
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("malformed registry CLI route unexpectedly succeeded: %s", output)
	}
	if !strings.Contains(string(output), "managed project registry") {
		t.Fatalf("malformed registry CLI error was not surfaced: %s", output)
	}
}

func TestCancelAcknowledgeCLIArgumentsAreStrict(t *testing.T) {
	if got, err := expectedStrict([]string{"--expected-hub-revision", strings.Repeat("a", 40)}); err != nil || got != strings.Repeat("a", 40) {
		t.Fatalf("valid acknowledgement arguments rejected: value=%q err=%v", got, err)
	}
	for _, args := range [][]string{
		{"--unknown", "value"},
		{"--expected-hub-revision"},
		{"--expected-hub-revision", "one", "--expected-hub-revision", "two"},
		{"run-id-extra"},
	} {
		if _, err := expectedStrict(args); err == nil {
			t.Fatalf("invalid acknowledgement arguments accepted: %#v", args)
		}
	}
}

func TestWriteCompletionCLIArgumentsAreStrict(t *testing.T) {
	if got, err := completionFileStrict([]string{"--completion-file", "input.json"}); err != nil || got != "input.json" {
		t.Fatalf("valid completion arguments rejected: value=%q err=%v", got, err)
	}
	for _, args := range [][]string{
		{},
		{"--completion-file"},
		{"--completion-file", ""},
		{"--completion-file", "one", "--completion-file", "two"},
		{"--completion-file", "input.json", "extra"},
		{"--output", "manual.json"},
	} {
		if _, err := completionFileStrict(args); err == nil {
			t.Fatalf("invalid completion arguments accepted: %#v", args)
		}
	}
}

func TestProjectIdentifiersCLIArgumentsAreStrict(t *testing.T) {
	if got, err := expectedStrict([]string{"--expected-hub-revision", strings.Repeat("a", 40)}); err != nil || got != strings.Repeat("a", 40) {
		t.Fatalf("valid identifier arguments rejected: value=%q err=%v", got, err)
	}
	for _, args := range [][]string{
		{"--unknown", "value"},
		{"--expected-hub-revision"},
		{"--expected-hub-revision", "one", "--expected-hub-revision", "two"},
	} {
		if _, err := expectedStrict(args); err == nil {
			t.Fatalf("invalid identifier arguments accepted: %#v", args)
		}
	}
}

func TestProjectIdentifiersCLIRoutesAdoptAndRead(t *testing.T) {
	hubBare, _, hubHead := testutil.RepoWithBareRemote(t)
	_, projectRoot, _ := testutil.RepoWithBareRemote(t)
	dir := t.TempDir()
	c := config.Config{SchemaVersion: 1, GatewayID: "test_gateway", ListenAddr: "127.0.0.1:8875", StateDir: filepath.Join(dir, "state"), MaxReadBytes: 1 << 20, MaxDiffBytes: 1 << 20, MaxListItems: 1000, Hub: config.HubConfig{RepositoryURL: hubBare, Branch: "main", AuthorName: "Gateway", AuthorEmail: "gateway@example.invalid"}, Projects: map[string]config.ProjectConfig{"example": {Root: projectRoot, Mirror: filepath.Join(dir, "mirror.git"), Remote: "origin", DefaultBranch: "main", AirelaySessionKey: "example_master"}}}
	s := service.New(c)
	projectRecord := model.Project{SchemaVersion: 1, ID: "example", RepositoryURL: "git@example.invalid:example.git", DefaultBranch: "main", WorkflowRepository: "rceman/gpt-review-planner", WorkflowCommit: strings.Repeat("a", 40), Status: "active"}
	if _, err := s.ProjectRegister(context.Background(), service.ProjectRegisterInput{Project: projectRecord, WriteOptions: service.WriteOptions{ExpectedHubRevision: hubHead}}); err != nil {
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
	adopt := capture(func() { project(context.Background(), s, []string{"identifiers-adopt", "example", "GTW"}) })
	if !strings.Contains(adopt, `"project_code": "GTW"`) || !strings.Contains(adopt, `"status": "adopted"`) {
		t.Fatalf("unexpected identifier adoption output: %s", adopt)
	}
	read := capture(func() { project(context.Background(), s, []string{"identifiers-read", "example"}) })
	if !strings.Contains(read, `"project_code": "GTW"`) || !strings.Contains(read, `"next_task_number": 1`) || !strings.Contains(read, `"next_adr_number": 1`) {
		t.Fatalf("unexpected identifier read output: %s", read)
	}
}

func TestCancelAcknowledgeCLIRouteUsesCanonicalServiceResult(t *testing.T) {
	hubBare, _, hubHead := testutil.RepoWithBareRemote(t)
	_, projectRoot, _ := testutil.RepoWithBareRemote(t)
	dir := t.TempDir()
	airelay := filepath.Join(dir, "airelay")
	if err := os.WriteFile(airelay, []byte("#!/bin/sh\nprintf 'dispatch output\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	c := config.Config{SchemaVersion: 1, GatewayID: "test_gateway", ListenAddr: "127.0.0.1:8875", StateDir: filepath.Join(dir, "state"), MaxReadBytes: 1 << 20, MaxDiffBytes: 1 << 20, MaxListItems: 1000, DispatchTimeoutSeconds: 5, RunTimeoutSeconds: 60, AirelayCommand: airelay, Hub: config.HubConfig{RepositoryURL: hubBare, Branch: "main", AuthorName: "Gateway", AuthorEmail: "gateway@example.invalid"}, Projects: map[string]config.ProjectConfig{"example": {Root: projectRoot, Mirror: filepath.Join(dir, "mirror.git"), Remote: "origin", DefaultBranch: "main", AirelaySessionKey: "example_master"}}}
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
	task, created, err := s.TaskCreate(context.Background(), service.TaskCreateInput{ProjectID: "example", Title: "Cancel acknowledgement", Objective: "Exercise the CLI surface", Slug: "cancel-ack-cli", AcceptanceCriteria: []string{"cancel"}, OperationClass: "implementation", CreatedBy: "test", WriteOptions: service.WriteOptions{ExpectedHubRevision: adopted.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := s.PlanUpdate(context.Background(), service.PlanUpdateInput{ProjectID: "example", Title: planString("Cancel acknowledgement"), Summary: planString("Cancel acknowledgement"), CurrentObjective: planString("Exercise the CLI surface"), ActiveTaskID: &task.ID, UpdatedBy: "test", WriteOptions: service.WriteOptions{ExpectedHubRevision: created.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	runRecord, _, err := s.TaskDispatch(context.Background(), service.DispatchInput{TaskID: task.ID, WriteOptions: service.WriteOptions{ExpectedHubRevision: plan.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(airelay, []byte("#!/bin/sh\nprintf 'cancel acknowledged\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	cancelRevision, err := s.Hub.RemoteRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RunCancel(context.Background(), runRecord.ID, cancelRevision); err != nil {
		t.Fatal(err)
	}

	oldStdout := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = writer
	run(context.Background(), s, []string{"cancel-acknowledge-no-mutation", runRecord.ID})
	_ = writer.Close()
	os.Stdout = oldStdout
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"status": "cancelled_no_mutation"`) {
		t.Fatalf("unexpected acknowledgement output: %s", data)
	}
}

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
	_, projectRoot, _ := testutil.RepoWithBareRemote(t)
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
	policyRevision := adoptTestWorkflowPolicyCLI(t, s, "example", registered.Hub.After)
	_, adopted, err := s.ProjectIdentifiersAdopt(context.Background(), service.ProjectIdentifiersAdoptInput{ProjectID: "example", ProjectCode: "EXM", WriteOptions: service.WriteOptions{ExpectedHubRevision: policyRevision}})
	if err != nil {
		t.Fatal(err)
	}
	task, created, err := s.TaskCreate(context.Background(), service.TaskCreateInput{ProjectID: "example", Title: "Tail", Objective: "Inspect tail", Slug: "tail", AcceptanceCriteria: []string{"tail"}, OperationClass: "implementation", CreatedBy: "test", WriteOptions: service.WriteOptions{ExpectedHubRevision: adopted.Hub.After}})
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
	_, projectRoot, _ := testutil.RepoWithBareRemote(t)
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
	policyRevision := adoptTestWorkflowPolicyCLI(t, s, "example", registered.Hub.After)
	_, adopted, err := s.ProjectIdentifiersAdopt(context.Background(), service.ProjectIdentifiersAdoptInput{ProjectID: "example", ProjectCode: "EXM", WriteOptions: service.WriteOptions{ExpectedHubRevision: policyRevision}})
	if err != nil {
		t.Fatal(err)
	}
	task, created, err := s.TaskCreate(context.Background(), service.TaskCreateInput{ProjectID: "example", Title: "Tail", Objective: "Inspect tail", Slug: "tail", AcceptanceCriteria: []string{"tail"}, OperationClass: "implementation", CreatedBy: "test", WriteOptions: service.WriteOptions{ExpectedHubRevision: adopted.Hub.After}})
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
