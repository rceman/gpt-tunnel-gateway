package service

import (
	"context"
	"encoding/json"
	"os"
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

func TestValidateConfiguredProjectRecordsRejectsMissingPlan(t *testing.T) {
	s, _, _ := testService(t)
	if err := s.ValidateConfiguredProjectRecords(context.Background()); err == nil || !strings.Contains(err.Error(), "plan") {
		t.Fatalf("missing durable plan was not rejected deterministically: %v", err)
	}
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
	testutil.Git(t, project.Root, "push", "-u", "origin", "feature/example")
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
	plan, err := s.PlanUpdate(ctx, PlanUpdateInput{ProjectID: "example", Summary: "Review feature", Body: "Execute the prepared task.", ActiveTaskID: task.ID, UpdatedBy: "gpt", WriteOptions: WriteOptions{ExpectedHubRevision: create.Hub.After}})
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
	plan, err := s.PlanUpdate(ctx, PlanUpdateInput{ProjectID: "example", Summary: "Bounded review", Body: "Review.", ActiveTaskID: task.ID, UpdatedBy: "gpt", WriteOptions: WriteOptions{ExpectedHubRevision: create.Hub.After}})
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
	plan, err := s.PlanUpdate(ctx, PlanUpdateInput{ProjectID: "example", Summary: "Tail", Body: "Tail.", ActiveTaskID: task.ID, UpdatedBy: "gpt", WriteOptions: WriteOptions{ExpectedHubRevision: create.Hub.After}})
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
