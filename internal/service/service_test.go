package service

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func planString(value string) *string { return &value }

func testService(t *testing.T) (*Service, string, string) {
	hubBare, _, hubHead := testutil.RepoWithBareRemote(t)
	_, projectWork, projectHead := testutil.RepoWithBareRemote(t)
	dir := t.TempDir()
	fake := filepath.Join(dir, "airelay")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	c := config.Config{SchemaVersion: 1, GatewayID: "test_gateway", ListenAddr: "127.0.0.1:8875", StateDir: filepath.Join(dir, "state"), MaxReadBytes: 1 << 20, MaxDiffBytes: 1 << 20, MaxListItems: 1000, DispatchTimeoutSeconds: 5, RunTimeoutSeconds: 60, AirelayCommand: fake, Hub: config.HubConfig{RepositoryURL: hubBare, Branch: "main", AuthorName: "Gateway", AuthorEmail: "gateway@example.invalid"}, Projects: map[string]config.ProjectConfig{"example": {Root: projectWork, Mirror: filepath.Join(dir, "mirror.git"), Remote: "origin", DefaultBranch: "main", AirelaySessionKey: "example_master"}}}
	s := New(c)
	project := model.Project{SchemaVersion: 1, ID: "example", RepositoryURL: "git@example.invalid:example.git", DefaultBranch: "main", WorkflowRepository: "rceman/gpt-review-planner", WorkflowCommit: "b1a45b1e9475ab29dfd3e84d523b70897c7b8918", Status: "active"}
	reg, err := s.ProjectRegister(context.Background(), ProjectRegisterInput{Project: project, WriteOptions: WriteOptions{ExpectedHubRevision: hubHead}})
	if err != nil {
		t.Fatal(err)
	}
	return s, reg.Hub.After, projectHead
}

func TestValidateConfiguredProjectRecordsRejectsMissingDurableRecord(t *testing.T) {
	s, _, _ := testService(t)
	s.Config.Projects["missing"] = s.Config.Projects["example"]
	if err := s.ValidateConfiguredProjectRecords(context.Background()); err == nil {
		t.Fatal("missing durable project record was accepted")
	}
}

func TestReadOnlyBootstrapErrorIsBounded(t *testing.T) {
	state := t.TempDir()
	s := New(config.Config{StateDir: state})
	_, err := s.ProjectList(context.Background())
	if err == nil || err.Error() != "read-only hub lock unavailable" || strings.Contains(err.Error(), state) {
		t.Fatalf("unbounded read-only error: %v", err)
	}
}

func TestValidateConfiguredProjectRecordsRejectsMissingPlan(t *testing.T) {
	s, _, _ := testService(t)
	if err := s.ValidateConfiguredProjectRecords(context.Background()); err == nil || !strings.Contains(err.Error(), "plan") {
		t.Fatalf("missing durable plan was not rejected deterministically: %v", err)
	}
}

func TestHistoricalRunDoesNotBreakDispatchOrSessionSafety(t *testing.T) {
	s, hubRev, projectHead := testService(t)
	fixture, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "historical-run-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	reportFixture, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "historical-report-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	tx, err := s.Hub.Transact(context.Background(), hubRev, "test: historical terminal run", func(worktree string) ([]string, error) {
		path := s.runPath("example", "11111111-1111-4111-8111-111111111111")
		return []string{path}, hub.WriteText(worktree, path, string(fixture))
	})
	if err != nil {
		t.Fatal(err)
	}
	reportTx, err := s.Hub.Transact(context.Background(), tx.After, "test: historical report", func(worktree string) ([]string, error) {
		path := s.reportPath("example", "11111111-1111-4111-8111-111111111111")
		return []string{path}, hub.WriteText(worktree, path, string(reportFixture))
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RunReport(context.Background(), "11111111-1111-4111-8111-111111111111"); err == nil || !strings.Contains(err.Error(), "history-only") {
		t.Fatalf("historical report was not isolated: %v", err)
	}
	runs, err := s.RunList(context.Background(), "example")
	if err != nil || len(runs) != 1 || !runs[0].Historical || runs[0].CompletionPath != "" {
		t.Fatalf("historical run list failed: %#v %v", runs, err)
	}
	task, created, err := s.TaskCreate(context.Background(), TaskCreateInput{ProjectID: "example", Title: "Dispatch after history", Objective: "Dispatch after history.", Branch: "feature/history-safe", BaseRevision: projectHead, AcceptanceCriteria: []string{"works"}, CreatedBy: "gpt", WriteOptions: WriteOptions{ExpectedHubRevision: reportTx.After}})
	if err != nil {
		t.Fatal(err)
	}
	_ = reportTx
	title, summary, objective := "History safe", "Dispatch after history", "Dispatch the new task after historical runs."
	plan, err := s.PlanUpdate(context.Background(), PlanUpdateInput{ProjectID: "example", Title: &title, Summary: &summary, CurrentObjective: &objective, ActiveTaskID: &task.ID, UpdatedBy: "gpt", WriteOptions: WriteOptions{ExpectedHubRevision: created.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.TaskDispatch(context.Background(), DispatchInput{TaskID: task.ID, WriteOptions: WriteOptions{ExpectedHubRevision: plan.Hub.After}}); err != nil {
		t.Fatalf("dispatch blocked by terminal historical run: %v", err)
	}
	root := t.TempDir()
	path := filepath.Join(root, "gpt-tunnel", "v1", "projects", "example", "runs", "old", "run.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	activeFixture := strings.Replace(string(fixture), `"status": "succeeded"`, `"status": "awaiting_result"`, 1)
	if err := os.WriteFile(path, []byte(activeFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ensureSessionAvailableInWorktree(root, "example_master", 1<<20); err == nil {
		t.Fatal("active historical run did not protect session")
	}
}
func TestTaskPlanDispatchReadFinalize(t *testing.T) {
	s, hubRev, projectHead := testService(t)
	ctx := context.Background()
	task, create, err := s.TaskCreate(ctx, TaskCreateInput{ProjectID: "example", Title: "Implement feature", Objective: "Implement exact behavior.", Branch: "feature/example", BaseRevision: projectHead, AcceptanceCriteria: []string{"feature works"}, Constraints: []string{"no redesign"}, RequiredGates: []string{"go test ./..."}, CreatedBy: "gpt", WriteOptions: WriteOptions{ExpectedHubRevision: hubRev}})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := s.PlanUpdate(ctx, PlanUpdateInput{ProjectID: "example", Title: planString("Implementation"), Summary: planString("Implement feature"), CurrentObjective: planString("Execute the prepared task."), ActiveTaskID: planString(task.ID), UpdatedBy: "gpt", WriteOptions: WriteOptions{ExpectedHubRevision: create.Hub.After}})
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
	testutil.Git(t, project.Root, "push", "-u", "origin", "feature/example")
	completion := model.Completion{SchemaVersion: 1, RunID: run.ID, TaskSHA256: task.SHA256, Status: "succeeded", Summary: "Implemented.", GateResults: []model.CompletionGateResult{{ID: "G1", ExitCode: 0}}, AcceptanceCoverage: []string{"AC1"}, Deviations: []string{}, RemainingRisks: []string{}}
	if err := fsutil.WriteJSONAtomic(run.CompletionPath, completion, 0o600); err != nil {
		t.Fatal(err)
	}
	report, final, err := s.RunFinalize(ctx, FinalizeInput{RunID: run.ID, WriteOptions: WriteOptions{ExpectedHubRevision: dispatch.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "succeeded" || final.Status != "TASK_FINALIZED" {
		t.Fatalf("bad final: %#v %#v", report, final)
	}
	snapshot, err := s.RunReviewSnapshot(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ReviewState != "reviewable" {
		t.Fatalf("expected reviewable snapshot, got %s checks=%#v", snapshot.ReviewState, snapshot.Checks)
	}
	if snapshot.Report.HubCommit == "" || !snapshot.Evidence.Available || !snapshot.Repository.TaskBranchPublished {
		t.Fatalf("missing canonical review proof: %#v", snapshot)
	}
}

func TestRunReviewSnapshotActiveIsBounded(t *testing.T) {
	s, hubRev, projectHead := testService(t)
	ctx := context.Background()
	task, create, err := s.TaskCreate(ctx, TaskCreateInput{ProjectID: "example", Title: "Review feature", Objective: "Review exact behavior.", Branch: "feature/review", BaseRevision: projectHead, AcceptanceCriteria: []string{"feature works"}, Constraints: []string{"no redesign"}, RequiredGates: []string{"go test ./..."}, CreatedBy: "gpt", WriteOptions: WriteOptions{ExpectedHubRevision: hubRev}})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := s.PlanUpdate(ctx, PlanUpdateInput{ProjectID: "example", Title: planString("Review"), Summary: planString("Review feature"), CurrentObjective: planString("Execute the prepared task."), ActiveTaskID: planString(task.ID), UpdatedBy: "gpt", WriteOptions: WriteOptions{ExpectedHubRevision: create.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := s.TaskDispatch(ctx, DispatchInput{TaskID: task.ID, WriteOptions: WriteOptions{ExpectedHubRevision: plan.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.RunReviewSnapshot(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ReviewState != "active" || snapshot.NextAction != "wait_for_terminal" {
		t.Fatalf("snapshot state=%s next=%s", snapshot.ReviewState, snapshot.NextAction)
	}
	if snapshot.Report.Available || snapshot.Evidence.Available {
		t.Fatal("active snapshot exposed terminal artifacts")
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"session_key", "result_path", "evidence_path", "dispatch_stdout", "dispatch_stderr"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("snapshot exposed forbidden field %q", forbidden)
		}
	}
}

func TestRunReviewSnapshotRejectsOversizedAggregate(t *testing.T) {
	s, hubRev, projectHead := testService(t)
	ctx := context.Background()
	task, create, err := s.TaskCreate(ctx, TaskCreateInput{ProjectID: "example", Title: "Bounded review", Objective: "Review bounded output.", Branch: "feature/bounded", BaseRevision: projectHead, AcceptanceCriteria: []string{"bounded"}, CreatedBy: "gpt", WriteOptions: WriteOptions{ExpectedHubRevision: hubRev}})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := s.PlanUpdate(ctx, PlanUpdateInput{ProjectID: "example", Title: planString("Review"), Summary: planString("Bounded review"), CurrentObjective: planString("Review."), ActiveTaskID: planString(task.ID), UpdatedBy: "gpt", WriteOptions: WriteOptions{ExpectedHubRevision: create.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := s.TaskDispatch(ctx, DispatchInput{TaskID: task.ID, WriteOptions: WriteOptions{ExpectedHubRevision: plan.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	s.Config.MaxReadBytes = 100
	if _, err := s.RunReviewSnapshot(ctx, run.ID); err == nil || !strings.Contains(err.Error(), "output limit") {
		t.Fatalf("expected explicit output bound error, got %v", err)
	}
}

func createActiveTailRun(t *testing.T, s *Service, hubRevision, projectHead string) model.Run {
	t.Helper()
	ctx := context.Background()
	revision := hubRevision
	task, create, err := s.TaskCreate(ctx, TaskCreateInput{ProjectID: "example", Title: "Tail", Objective: "Inspect tail.", Branch: "feature/tail", BaseRevision: projectHead, AcceptanceCriteria: []string{"tail"}, CreatedBy: "gpt", WriteOptions: WriteOptions{ExpectedHubRevision: revision}})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := s.PlanUpdate(ctx, PlanUpdateInput{ProjectID: "example", Title: planString("Tail"), Summary: planString("Tail"), CurrentObjective: planString("Tail."), ActiveTaskID: planString(task.ID), UpdatedBy: "gpt", WriteOptions: WriteOptions{ExpectedHubRevision: create.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := s.TaskDispatch(ctx, DispatchInput{TaskID: task.ID, WriteOptions: WriteOptions{ExpectedHubRevision: plan.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func TestRunAgentTailUsesStoredSessionAndDefaultAndExplicitLines(t *testing.T) {
	s, hubRevision, projectHead := testService(t)
	dir := t.TempDir()
	log := filepath.Join(dir, "args")
	script := filepath.Join(dir, "airelay")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \""+log+"\"\nprintf 'tail text\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	run := createActiveTailRun(t, s, hubRevision, projectHead)
	s.Airelay.Command = script
	text, err := s.RunAgentTail(context.Background(), run.ID, 0)
	if err != nil || text != "tail text\n" {
		t.Fatalf("default tail=%q err=%v", text, err)
	}
	args, _ := os.ReadFile(log)
	if string(args) != "tail\nexample_master\n--lines\n4\n" {
		t.Fatalf("default argv=%q", args)
	}
	_, err = s.RunAgentTail(context.Background(), run.ID, 9)
	if err != nil {
		t.Fatal(err)
	}
	args, _ = os.ReadFile(log)
	if !strings.HasSuffix(string(args), "--lines\n9\n") {
		t.Fatalf("explicit argv=%q", args)
	}
}

func TestRunAgentTailRejectsBoundsTerminalAndForeignBeforeAirelay(t *testing.T) {
	s, revision, _ := testService(t)
	now := time.Now().UTC()
	for _, run := range []model.Run{
		{SchemaVersion: 1, ID: "terminal-tail", TaskID: "task", TaskSHA256: strings.Repeat("a", 64), ProjectID: "example", GatewayID: s.Config.GatewayID, SessionKey: "terminal_secret", Branch: "feature/x", BaseRevision: strings.Repeat("b", 40), Status: "succeeded", CreatedAt: now},
		{SchemaVersion: 1, ID: "foreign-tail", TaskID: "task", TaskSHA256: strings.Repeat("a", 64), ProjectID: "example", GatewayID: "other_gateway", SessionKey: "foreign_secret", Branch: "feature/x", BaseRevision: strings.Repeat("b", 40), Status: "awaiting_result", CreatedAt: now},
	} {
		tx, err := s.Hub.Transact(context.Background(), revision, "test tail run", func(worktree string) ([]string, error) {
			path := s.runPath(run.ProjectID, run.ID)
			return []string{path}, hub.WriteJSON(worktree, path, run)
		})
		if err != nil {
			t.Fatal(err)
		}
		revision = tx.After
	}
	for _, test := range []struct {
		id    string
		want  string
		lines int
	}{{"terminal-tail", "run is not active", 4}, {"foreign-tail", "assigned to gateway", 4}, {"foreign-tail", "", 201}} {
		if _, err := s.RunAgentTail(context.Background(), test.id, test.lines); err == nil || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("%s error=%v", test.id, err)
		}
	}
}

func TestHistoricalOperationalPathsAreReadOnly(t *testing.T) {
	s, hubRevision, _ := testService(t)
	fixture, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "historical-run-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	active := strings.Replace(string(fixture), `"status": "succeeded"`, `"status": "awaiting_result"`, 1)
	active = strings.Replace(active, `"gateway_id": "home_pc"`, `"gateway_id": "test_gateway"`, 1)
	path := s.runPath("example", "11111111-1111-4111-8111-111111111111")
	tx, err := s.Hub.Transact(context.Background(), hubRevision, "test: historical active run", func(worktree string) ([]string, error) {
		return []string{path}, hub.WriteText(worktree, path, active)
	})
	if err != nil {
		t.Fatal(err)
	}
	foreign := strings.Replace(active, `"id": "11111111-1111-4111-8111-111111111111"`, `"id": "22222222-2222-4222-8222-222222222222"`, 1)
	foreign = strings.Replace(foreign, `"gateway_id": "test_gateway"`, `"gateway_id": "other_gateway"`, 1)
	foreignPath := s.runPath("example", "22222222-2222-4222-8222-222222222222")
	foreignTx, err := s.Hub.Transact(context.Background(), tx.After, "test: foreign historical active run", func(worktree string) ([]string, error) {
		return []string{foreignPath}, hub.WriteText(worktree, foreignPath, foreign)
	})
	if err != nil {
		t.Fatal(err)
	}
	tx = foreignTx
	before, err := s.Hub.ReadFile(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.RunRead(context.Background(), "11111111-1111-4111-8111-111111111111")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RunAgentTail(context.Background(), run.ID, 4); err == nil || !strings.Contains(err.Error(), "history-only") {
		t.Fatalf("historical agent tail was not rejected: %v", err)
	}
	if _, err := s.RunCancel(context.Background(), run.ID, tx.After); err == nil || !strings.Contains(err.Error(), "history-only") {
		t.Fatalf("historical cancellation was not rejected: %v", err)
	}
	sweep, err := s.RunSweep(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sweep.Items) != 1 || !strings.Contains(sweep.Items[0].Error, "history-only") {
		t.Fatalf("historical sweep was not rejected: %#v", sweep)
	}
	if _, err := s.updateRun(context.Background(), run, tx.After, "test: historical update"); err == nil || !strings.Contains(err.Error(), "history-only") {
		t.Fatalf("historical shared update was not rejected: %v", err)
	}
	if _, err := s.failRun(context.Background(), run, model.Task{}, "failed", "test", tx.After); err == nil || !strings.Contains(err.Error(), "history-only") {
		t.Fatalf("historical synthetic failure was not rejected: %v", err)
	}
	after, err := s.Hub.ReadFile(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("historical run bytes changed after rejected operations")
	}
	current, err := s.Hub.RemoteRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if current != tx.After {
		t.Fatalf("historical operation changed hub revision: got %s want %s", current, tx.After)
	}
}

func TestGatewayCompletionPathRejectsOverridesAndSymlinks(t *testing.T) {
	dir := t.TempDir()
	completion := filepath.Join(dir, "runs", "run", "completion.json")
	if err := fsutil.WriteJSONAtomic(completion, map[string]any{"ok": true}, 0o600); err != nil {
		t.Fatal(err)
	}
	run := model.Run{CompletionPath: completion}
	got, err := gatewayCompletionPath(run, filepath.Join(dir, "runs", "run", ".", "completion.json"))
	if err != nil || got != completion {
		t.Fatalf("normalized gateway path rejected: %q %v", got, err)
	}
	other := filepath.Join(dir, "source-tree-completion.json")
	if err := os.WriteFile(other, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := gatewayCompletionPath(run, other); err == nil {
		t.Fatal("arbitrary completion path accepted")
	}
	symlink := filepath.Join(dir, "runs", "symlink", "completion.json")
	if err := os.MkdirAll(filepath.Dir(symlink), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, symlink); err != nil {
		t.Fatal(err)
	}
	if _, err := gatewayCompletionPath(model.Run{CompletionPath: symlink}, ""); err == nil {
		t.Fatal("symlink completion path accepted")
	}
}

func TestReportReadsRecomputeGitProof(t *testing.T) {
	s, hubRevision, projectHead := testService(t)
	ctx := context.Background()
	task, created, err := s.TaskCreate(ctx, TaskCreateInput{ProjectID: "example", Title: "Proof task", Objective: "Verify report proof.", Branch: "feature/proof", BaseRevision: projectHead, AcceptanceCriteria: []string{"proof"}, CreatedBy: "gpt", WriteOptions: WriteOptions{ExpectedHubRevision: hubRevision}})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := s.PlanUpdate(ctx, PlanUpdateInput{ProjectID: "example", Title: planString("Proof"), Summary: planString("Proof"), CurrentObjective: planString("Verify proof."), ActiveTaskID: planString(task.ID), UpdatedBy: "gpt", WriteOptions: WriteOptions{ExpectedHubRevision: created.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	run, dispatch, err := s.TaskDispatch(ctx, DispatchInput{TaskID: task.ID, WriteOptions: WriteOptions{ExpectedHubRevision: plan.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	project := s.Config.Projects["example"]
	if err := os.WriteFile(filepath.Join(project.Root, "proof.txt"), []byte("proof\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, project.Root, "add", "proof.txt")
	testutil.Git(t, project.Root, "commit", "-m", "proof")
	testutil.Git(t, project.Root, "push", "-u", "origin", "feature/proof")
	completion := model.Completion{SchemaVersion: 1, RunID: run.ID, TaskSHA256: task.SHA256, Status: "succeeded", Summary: "proof", GateResults: []model.CompletionGateResult{}, AcceptanceCoverage: []string{"AC1"}, Deviations: []string{}, RemainingRisks: []string{}}
	if err := fsutil.WriteJSONAtomic(run.CompletionPath, completion, 0o600); err != nil {
		t.Fatal(err)
	}
	final, _, err := s.RunFinalize(ctx, FinalizeInput{RunID: run.ID, WriteOptions: WriteOptions{ExpectedHubRevision: dispatch.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	remote := strings.TrimSpace(testutil.Git(t, project.Root, "remote", "get-url", "origin"))
	coldParent := t.TempDir()
	cold := filepath.Join(coldParent, "cold")
	testutil.Git(t, coldParent, "clone", "--no-local", "--single-branch", "--branch", "main", remote, cold)
	missing := exec.Command("git", "cat-file", "-e", final.Repository.Head+"^{commit}")
	missing.Dir = cold
	if err := missing.Run(); err == nil {
		t.Fatal("cold worktree unexpectedly contains the feature commit")
	}
	project.Root = cold
	s.Config.Projects["example"] = project
	stored, err := s.RunReport(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.RunReviewSnapshot(ctx, run.ID)
	if err != nil || !snapshot.Report.Available {
		t.Fatalf("cold review snapshot failed: available=%v err=%v", snapshot.Report.Available, err)
	}
	stored.Repository.ChangedFiles = []string{"injected.txt"}
	if _, err := s.Hub.Transact(ctx, final.HubCommit, "test: tamper report proof", func(worktree string) ([]string, error) {
		path := s.reportPath(run.ProjectID, run.ID)
		return []string{path}, hub.WriteJSON(worktree, path, stored)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RunReport(ctx, run.ID); err == nil || !strings.Contains(err.Error(), "changed files") {
		t.Fatalf("tampered report was accepted: %v", err)
	}
	snapshot, err = s.RunReviewSnapshot(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Report.Available || snapshot.Report.Error == "" {
		t.Fatalf("tampered report was exposed by review snapshot: %#v", snapshot.Report)
	}
}

func mirrorProofReport(t *testing.T, s *Service, project config.ProjectConfig, run model.Run, head string) model.Report {
	t.Helper()
	ancestor, err := s.Git.MirrorAncestor(context.Background(), project, run.BaseRevision, head)
	if err != nil {
		t.Fatal(err)
	}
	files, err := s.Git.MirrorChangedFiles(context.Background(), project, run.BaseRevision, head)
	if err != nil {
		t.Fatal(err)
	}
	commits, err := s.Git.MirrorLog(context.Background(), project, run.BaseRevision, head, s.Config.MaxListItems)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(commits))
	for _, commit := range commits {
		ids = append(ids, commit.SHA)
	}
	return model.Report{Repository: model.RepositoryProof{Branch: run.Branch, Head: head, BaseAncestor: ancestor, Commits: ids, ChangedFiles: files, DiffScope: run.BaseRevision + ".." + head}}
}

func TestMirrorReportBranchReachability(t *testing.T) {
	s, _, base := testService(t)
	ctx := context.Background()
	project := s.Config.Projects["example"]
	if err := os.WriteFile(filepath.Join(project.Root, "mirror-proof.txt"), []byte("proof\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, project.Root, "add", "mirror-proof.txt")
	testutil.Git(t, project.Root, "commit", "-m", "mirror proof")
	testutil.Git(t, project.Root, "push", "origin", "main")
	head := strings.TrimSpace(testutil.Git(t, project.Root, "rev-parse", "HEAD"))
	testutil.Git(t, project.Root, "branch", "feature/mirror")
	testutil.Git(t, project.Root, "push", "origin", "feature/mirror")
	if err := s.Git.Refresh(ctx, project); err != nil {
		t.Fatal(err)
	}
	run := model.Run{ProjectID: "example", Branch: "feature/mirror", BaseRevision: base}
	report := mirrorProofReport(t, s, project, run, head)
	if err := s.validateCanonicalReportProof(ctx, report, run, project); err != nil {
		t.Fatalf("published task branch rejected: %v", err)
	}
	testutil.Git(t, project.Root, "push", "origin", "--delete", "feature/mirror")
	if err := s.Git.Refresh(ctx, project); err != nil {
		t.Fatal(err)
	}
	if err := s.validateCanonicalReportProof(ctx, report, run, project); err != nil {
		t.Fatalf("deleted task branch with default reachability rejected: %v", err)
	}

	testutil.Git(t, project.Root, "switch", "-c", "feature/unmerged")
	if err := os.WriteFile(filepath.Join(project.Root, "unmerged.txt"), []byte("unmerged\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, project.Root, "add", "unmerged.txt")
	testutil.Git(t, project.Root, "commit", "-m", "unmerged proof")
	testutil.Git(t, project.Root, "push", "origin", "feature/unmerged")
	unmergedHead := strings.TrimSpace(testutil.Git(t, project.Root, "rev-parse", "HEAD"))
	if err := s.Git.Refresh(ctx, project); err != nil {
		t.Fatal(err)
	}
	unmergedRun := model.Run{ProjectID: "example", Branch: "feature/unmerged", BaseRevision: base}
	unmergedReport := mirrorProofReport(t, s, project, unmergedRun, unmergedHead)
	testutil.Git(t, project.Root, "push", "origin", "--delete", "feature/unmerged")
	if err := s.Git.Refresh(ctx, project); err != nil {
		t.Fatal(err)
	}
	if err := s.validateCanonicalReportProof(ctx, unmergedReport, unmergedRun, project); err == nil || !strings.Contains(err.Error(), "reachable") {
		t.Fatalf("unmerged deleted branch was accepted: %v", err)
	}

	testutil.Git(t, project.Root, "branch", "feature/existing")
	testutil.Git(t, project.Root, "push", "origin", "feature/existing")
	if err := s.Git.Refresh(ctx, project); err != nil {
		t.Fatal(err)
	}
	existingRun := model.Run{ProjectID: "example", Branch: "feature/existing", BaseRevision: base}
	existingReport := mirrorProofReport(t, s, project, existingRun, head)
	if err := s.validateCanonicalReportProof(ctx, existingReport, existingRun, project); err == nil || !strings.Contains(err.Error(), "does not point") {
		t.Fatalf("existing branch at another HEAD was accepted: %v", err)
	}

	absentReport := report
	absentReport.Repository.Head = strings.Repeat("f", 40)
	if err := s.validateCanonicalReportProof(ctx, absentReport, run, project); err == nil || !strings.Contains(err.Error(), "does not resolve") {
		t.Fatalf("absent report HEAD was accepted: %v", err)
	}
}

func TestRunFinalizeRequiresPublishedBranchAtomically(t *testing.T) {
	s, hubRevision, projectHead := testService(t)
	ctx := context.Background()
	task, created, err := s.TaskCreate(ctx, TaskCreateInput{ProjectID: "example", Title: "Push first", Objective: "Require durable finalization.", Branch: "feature/push-first", BaseRevision: projectHead, AcceptanceCriteria: []string{"durable"}, CreatedBy: "gpt", WriteOptions: WriteOptions{ExpectedHubRevision: hubRevision}})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := s.PlanUpdate(ctx, PlanUpdateInput{ProjectID: "example", Title: planString("Push first"), Summary: planString("Push first"), CurrentObjective: planString("Push before finalize."), ActiveTaskID: planString(task.ID), UpdatedBy: "gpt", WriteOptions: WriteOptions{ExpectedHubRevision: created.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	run, dispatch, err := s.TaskDispatch(ctx, DispatchInput{TaskID: task.ID, WriteOptions: WriteOptions{ExpectedHubRevision: plan.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	project := s.Config.Projects["example"]
	if err := os.WriteFile(filepath.Join(project.Root, "push-first.txt"), []byte("first\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, project.Root, "add", "push-first.txt")
	testutil.Git(t, project.Root, "commit", "-m", "push first")
	completion := model.Completion{SchemaVersion: 1, RunID: run.ID, TaskSHA256: task.SHA256, Status: "succeeded", Summary: "done", GateResults: []model.CompletionGateResult{}, AcceptanceCoverage: []string{"AC1"}, Deviations: []string{}, RemainingRisks: []string{}}
	if err := fsutil.WriteJSONAtomic(run.CompletionPath, completion, 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := s.Hub.RemoteRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.RunFinalize(ctx, FinalizeInput{RunID: run.ID, WriteOptions: WriteOptions{ExpectedHubRevision: dispatch.Hub.After}}); err == nil || !strings.Contains(err.Error(), "pushed") {
		t.Fatalf("pre-push finalization was accepted: %v", err)
	}
	active, err := s.RunRead(ctx, run.ID)
	if err != nil || active.Status != "awaiting_result" {
		t.Fatalf("pre-push finalization changed run state: %#v %v", active, err)
	}
	if _, err := s.RunReport(ctx, run.ID); err == nil {
		t.Fatal("pre-push finalization created a report")
	}
	state, err := s.taskState(ctx, task)
	if err != nil || state.Status != "dispatched" {
		t.Fatalf("pre-push finalization changed task state: %#v %v", state, err)
	}
	currentPlan, err := s.PlanRead(ctx, task.ProjectID)
	if err != nil || currentPlan.ActiveRunID != run.ID {
		t.Fatalf("pre-push finalization changed plan state: %#v %v", currentPlan, err)
	}
	after, err := s.Hub.RemoteRevision(ctx)
	if err != nil || after != before {
		t.Fatalf("pre-push finalization changed hub revision: before=%s after=%s err=%v", before, after, err)
	}

	testutil.Git(t, project.Root, "push", "origin", "feature/push-first")
	remote := strings.TrimSpace(testutil.Git(t, project.Root, "remote", "get-url", "origin"))
	moverParent := t.TempDir()
	mover := filepath.Join(moverParent, "mover")
	testutil.Git(t, moverParent, "clone", "--no-local", "--branch", "feature/push-first", remote, mover)
	testutil.Git(t, mover, "config", "user.name", "Test User")
	testutil.Git(t, mover, "config", "user.email", "test@example.invalid")
	if err := os.WriteFile(filepath.Join(mover, "moved.txt"), []byte("moved\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, mover, "add", "moved.txt")
	testutil.Git(t, mover, "commit", "-m", "move published branch")
	testutil.Git(t, mover, "push", "origin", "feature/push-first")
	if _, _, err := s.RunFinalize(ctx, FinalizeInput{RunID: run.ID, WriteOptions: WriteOptions{ExpectedHubRevision: dispatch.Hub.After}}); err == nil || !strings.Contains(err.Error(), "pushed") {
		t.Fatalf("finalization accepted a branch pointing elsewhere: %v", err)
	}
}

func TestSyntheticFailureUsesDurableBaseProof(t *testing.T) {
	s, hubRevision, projectHead := testService(t)
	ctx := context.Background()
	task, created, err := s.TaskCreate(ctx, TaskCreateInput{ProjectID: "example", Title: "Durable failure", Objective: "Keep synthetic proof durable.", Branch: "feature/durable-failure", BaseRevision: projectHead, AcceptanceCriteria: []string{"durable"}, CreatedBy: "gpt", WriteOptions: WriteOptions{ExpectedHubRevision: hubRevision}})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := s.PlanUpdate(ctx, PlanUpdateInput{ProjectID: "example", Title: planString("Durable failure"), Summary: planString("Durable failure"), CurrentObjective: planString("Use durable proof."), ActiveTaskID: planString(task.ID), UpdatedBy: "gpt", WriteOptions: WriteOptions{ExpectedHubRevision: created.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := s.TaskDispatch(ctx, DispatchInput{TaskID: task.ID, WriteOptions: WriteOptions{ExpectedHubRevision: plan.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	project := s.Config.Projects["example"]
	if err := os.WriteFile(filepath.Join(project.Root, "unpublished.txt"), []byte("unpublished\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, project.Root, "add", "unpublished.txt")
	testutil.Git(t, project.Root, "commit", "-m", "unpublished local commit")
	expected, err := s.Hub.RemoteRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.failRun(ctx, run, task, "failed", "synthetic timeout", expected); err != nil {
		t.Fatal(err)
	}
	report, err := s.RunReport(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if report.Repository.Head != projectHead || len(report.Repository.Commits) != 0 || len(report.Repository.ChangedFiles) != 0 {
		t.Fatalf("synthetic report used non-durable proof: %#v", report.Repository)
	}
	foundRisk := false
	for _, risk := range report.RemainingRisks {
		if strings.Contains(risk, "not remotely durable") || strings.Contains(risk, "unpublished") {
			foundRisk = true
		}
	}
	if !foundRisk {
		t.Fatalf("synthetic report omitted bounded durability risk: %#v", report.RemainingRisks)
	}
	snapshot, err := s.RunReviewSnapshot(ctx, run.ID)
	if err != nil || !snapshot.Report.Available {
		t.Fatalf("synthetic report failed review snapshot validation: available=%v err=%v", snapshot.Report.Available, err)
	}
}
