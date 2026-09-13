package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/gates"
	"github.com/rceman/gpt-tunnel-gateway/internal/gitx"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

func tsk585Setup(t *testing.T) (*Service, *sqlitestore.Databases) {
	t.Helper()
	s, _, _ := testServiceSerial(t)
	project := s.Config.Projects["example"]
	project.ProjectCode = "EXM"
	s.Config.Projects["example"] = project
	configuration, err := s.ProjectConfigurationRead(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := s.AgentRead(context.Background(), "example", "coder-example")
	if err != nil {
		t.Fatal(err)
	}
	db, err := sqlitestore.Open(s.Config.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	s.Durability = db
	payload, err := json.Marshal(configuration)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.PutSharedProjection(context.Background(), "project_configuration", sqlitestore.SharedEntity{ID: "example", Revision: int64(configuration.Revision), Payload: payload, UpdatedAt: configuration.UpdatedAt.UTC().Format(time.RFC3339Nano)}); err != nil {
		t.Fatal(err)
	}
	agentPayload, err := json.Marshal(agent)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertLocalAgent(context.Background(), sqlitestore.LocalAgent{ProjectID: "example", AgentID: agent.AgentID, Payload: agentPayload, UpdatedAt: agent.UpdatedAt.UTC().Format(time.RFC3339Nano)}); err != nil {
		t.Fatal(err)
	}
	s.gateExecutorWithProjectCommands = func(ctx context.Context, root string, names []string, _ model.ProjectGateCommands, _ string) ([]model.CompletionGateResult, error) {
		tree, err := s.Git.TreeID(ctx, config.ProjectConfig{Root: root})
		if err != nil {
			return nil, err
		}
		out := make([]model.CompletionGateResult, len(names))
		for i, name := range names {
			out[i] = model.CompletionGateResult{ID: name, ExitCode: 0, TreeID: tree}
		}
		return out, nil
	}
	return s, db
}
func tsk585Task(t *testing.T, s *Service, idem, title string) model.TaskAuthoring {
	t.Helper()
	task, _, err := s.taskAuthoringCreateShared(context.Background(), idem, TaskAuthoringCreateInput{
		ProjectID:   "example",
		Title:       title,
		Summary:     "Verification summary.",
		Objective:   "Run task/test.",
		ADRRelation: model.TaskADRNoRequired,
		CreatedBy:   "planner",
	})
	if err != nil {
		t.Fatal(err)
	}
	return task
}
func tsk585Dispatch(t *testing.T, s *Service, key string) string {
	t.Helper()
	out, err := s.TaskExecutionDispatch(context.Background(), TaskExecutionDispatchInput{
		ProjectID: "example",
		Key:       key,
	})
	if err != nil {
		t.Fatal(err)
	}
	return out.Worktree
}
func tsk585LaneCommit(t *testing.T, s *Service, key, message string) {
	t.Helper()
	path, err := gitx.TaskWorktreePath(s.Config.StateDir, "example", key)
	if err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, path, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", message)
}
func tsk585DriveToVerification(t *testing.T, s *Service, key string) {
	t.Helper()
	ctx := context.Background()
	if _, err := s.TaskExecutionSubmitCode(ctx, "example", key); err != nil {
		t.Fatalf("submit code: %v", err)
	}
	if _, err := s.TaskExecutionReviewDecide(ctx, TaskExecutionReviewDecisionInput{
		ProjectID: "example",
		Key:       key,
		Stage:     "code",
		Decision:  "accept",
	}); err != nil {
		t.Fatalf("accept code: %v", err)
	}
	if _, err := s.TaskExecutionSubmitTests(ctx, "example", key); err != nil {
		t.Fatalf("submit tests: %v", err)
	}
	if _, err := s.TaskExecutionReviewDecide(ctx, TaskExecutionReviewDecisionInput{
		ProjectID: "example",
		Key:       key,
		Stage:     "tests",
		Decision:  "accept",
	}); err != nil {
		t.Fatalf("accept tests: %v", err)
	}
}
func tsk585WaitOperation(t *testing.T, s *Service, operationID string) durableMutationOperation {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		operation, err := s.readDurableMutation(operationID)
		if err == nil && (operation.Status == "completed" || operation.Status == "failed" || operation.Status == "outcome_unknown") {
			return operation
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("operation %s did not finish", operationID)
	return durableMutationOperation{}
}
func tsk585AdvanceCanonical(t *testing.T, s *Service) {
	t.Helper()
	root := s.Config.Projects["example"].Root
	testutil.Git(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "advance main")
	testutil.Git(t, root, "push", "origin", "main")
}
func TestTSK585TaskTestEndToEnd(t *testing.T) {
	s, db := tsk585Setup(t)
	defer db.Close()
	ctx := context.Background()
	task := tsk585Task(t, s, "tsk585-e2e", "Verify Task")
	tsk585Dispatch(t, s, task.ID)
	tsk585LaneCommit(t, s, task.ID, "candidate work")
	if _, err := s.TaskExecutionTestAsync(ctx, TaskExecutionTestInput{
		ProjectID: "example",
		Key:       task.ID,
	}); err == nil {
		t.Fatal("task/test must reject a Task that is not ready for verification")
	}
	tsk585DriveToVerification(t, s, task.ID)

	operation := tsk585VerifyTask(t, s, task.ID)
	if operation.Status != "completed" {
		t.Fatalf("operation status=%q error=%q", operation.Status, operation.Error)
	}
	if len(operation.CapturedState) == 0 {
		t.Fatal("durable operation did not persist the admitted verification snapshot")
	}
	state, found, err := db.ReadTaskExecutionState(ctx, "example", task.ID)
	if err != nil || !found || state.Status != model.TaskExecutionVerified {
		t.Fatalf("state=%#v found=%v err=%v", state, found, err)
	}
	latest, found, err := db.ReadLatestTaskExecutionVerification(ctx, "example", task.ID)
	if err != nil || !found || latest.Outcome != model.TaskExecutionVerificationSucceeded || latest.OperationID != operation.OperationID {
		t.Fatalf("latest receipt=%#v found=%v err=%v", latest, found, err)
	}
	if latest.CandidateHead != state.Head || latest.BaseHead != state.BaseHead || latest.TaskRevisionSHA256 != state.TaskRevisionSHA256 || latest.CodeReviewID < 1 || latest.TestsReviewID < 1 || len(latest.GateProfileSHA256) != 64 || len(latest.Gates) == 0 {
		t.Fatalf("receipt binding=%#v state=%#v", latest, state)
	}
	status, err := s.TaskExecutionStatus(ctx, "example", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if status.Verification == nil || status.Verification.OperationID != operation.OperationID || status.Verification.CandidateHead != strings.ToLower(state.Head[:8]) || status.Verification.TaskRevision != task.Revision {
		t.Fatalf("status verification projection=%#v", status.Verification)
	}

	duplicate, err := s.TaskExecutionTestAsync(ctx, TaskExecutionTestInput{
		ProjectID: "example",
		Key:       task.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if duplicate.OperationID != operation.OperationID || duplicate.Status != "completed" || duplicate.Result == nil {
		t.Fatalf("unchanged duplicate must return completed proof: %#v", duplicate)
	}
	if _, err := s.TaskExecutionRework(ctx, TaskExecutionReworkInput{
		ProjectID: "example",
		Key:       task.ID,
		Stage:     "code",
		Comment:   "reopen",
	}); err != nil {
		t.Fatalf("Planner rework on a verified Task must be allowed: %v", err)
	}
	stale, err := s.TaskExecutionStatus(ctx, "example", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stale.Verification != nil {
		t.Fatalf("reworked Task must not project the superseded verification: %#v", stale.Verification)
	}
	reworked, _, _ := db.ReadTaskExecutionState(ctx, "example", task.ID)
	if reworked.Status != model.TaskExecutionChangesRequested || reworked.Stage != "code" {
		t.Fatalf("rework must reopen the requested stage: %#v", reworked)
	}
	reworked.Status = model.TaskExecutionIntegrated
	reworked.ExecutionRevision++
	if err := db.UpdateTaskExecutionState(ctx, reworked, reworked.ExecutionRevision-1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TaskExecutionRework(ctx, TaskExecutionReworkInput{
		ProjectID: "example",
		Key:       task.ID,
		Stage:     "code",
		Comment:   "reopen",
	}); err == nil {
		t.Fatal("rework on an integrated Task must fail")
	}
	if _, err := s.TaskExecutionTestAsync(ctx, TaskExecutionTestInput{
		ProjectID: "example",
		Key:       task.ID,
	}); err == nil {
		t.Fatal("task/test on an integrated Task must fail")
	}
	second := tsk585Task(t, s, "tsk585-e2e-2", "Followup Task")
	if _, err := s.TaskExecutionDispatch(ctx, TaskExecutionDispatchInput{
		ProjectID: "example",
		Key:       second.ID,
	}); err != nil {
		t.Fatalf("integrated Task must release the Agent for the next dispatch: %v", err)
	}
}
func tsk585WriteStaleOperation(t *testing.T, s *Service, key, captured string) string {
	t.Helper()
	input, err := json.Marshal(TaskExecutionTestInput{
		ProjectID: "example",
		Key:       key,
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	operation := durableMutationOperation{
		SchemaVersion: durableMutationSchemaVersion,
		OperationID:   "mutation-" + strings.Repeat("c", 40) + key[strings.LastIndex(key, "TSK")+3:],
		Kind:          "task-execution-test",
		RequestSHA256: strings.Repeat("d", 64),
		ProjectID:     "example",
		Input:         input,
		Status:        "accepted",
		CapturedState: captured,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := s.writeDurableMutation(operation); err != nil {
		t.Fatal(err)
	}
	return operation.OperationID
}
func tsk585CurrentIdentity(t *testing.T, s *Service, key string) string {
	t.Helper()
	ctx := context.Background()
	state, found, err := s.Durability.ReadTaskExecutionState(ctx, "example", key)
	if err != nil || !found {
		t.Fatalf("state found=%v err=%v", found, err)
	}
	admission, err := s.taskExecutionVerificationAdmissionFor(ctx, "example", state)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(admission.identity)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
func tsk585FileCommit(t *testing.T, dir, name, content, message string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, dir, "add", name)
	testutil.Git(t, dir, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-m", message)
}
func TestTSK585RebaseConflictPreservesLane(t *testing.T) {
	s, db := tsk585Setup(t)
	defer db.Close()
	ctx := context.Background()
	task := tsk585Task(t, s, "tsk585-conflict", "Conflict Task")
	tsk585Dispatch(t, s, task.ID)
	lanePath, err := gitx.TaskWorktreePath(s.Config.StateDir, "example", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	tsk585FileCommit(t, lanePath, "conflict.txt", "lane\n", "lane change")
	preRebaseHead := strings.TrimSpace(testutil.Git(t, lanePath, "rev-parse", "HEAD"))
	tsk585DriveToVerification(t, s, task.ID)
	if operation := tsk585VerifyTask(t, s, task.ID); operation.Status != "completed" {
		t.Fatalf("operation status=%q error=%q", operation.Status, operation.Error)
	}
	projectRoot := s.Config.Projects["example"].Root
	testutil.Git(t, projectRoot, "checkout", "main")
	tsk585FileCommit(t, projectRoot, "conflict.txt", "main\n", "main change")
	testutil.Git(t, projectRoot, "push", "origin", "main")
	canonical := strings.TrimSpace(testutil.Git(t, projectRoot, "rev-parse", "main"))

	operation := tsk585VerifyTask(t, s, task.ID)
	t.Logf("conflict op error=%q", operation.Error)
	if operation.Status != "failed" {
		t.Fatalf("conflicting rebase must fail the operation: status=%q error=%q", operation.Status, operation.Error)
	}
	state, _, _ := db.ReadTaskExecutionState(ctx, "example", task.ID)
	if state.Status != model.TaskExecutionChangesRequested || state.Stage != "rebase" || state.BaseHead != canonical || state.Head != preRebaseHead || state.Agent == "" {
		t.Fatalf("conflict must bind the exact canonical target, committed head, and owning Agent: %#v", state)
	}
	phase, found, _ := db.ReadLatestTaskExecutionPhase(ctx, "example", task.ID, "rebase")
	if !found || !strings.Contains(phase.Comment, "conflict") || !strings.Contains(phase.Comment, canonical[:8]) {
		t.Fatalf("durable conflict evidence missing target: %#v", phase)
	}
	porcelain := strings.TrimSpace(testutil.Git(t, lanePath, "status", "--porcelain"))
	onto, err := s.Git.RebaseOnto(ctx, config.ProjectConfig{Root: lanePath})
	if err != nil || onto != canonical {
		t.Fatalf("in-progress rebase must bind the recorded onto target: onto=%q err=%v", onto, err)
	}
	detached := strings.TrimSpace(testutil.Git(t, lanePath, "rev-parse", "--abbrev-ref", "HEAD"))
	branchHead := strings.TrimSpace(testutil.Git(t, lanePath, "rev-parse", "--verify", "refs/heads/"+state.Branch))
	if !strings.Contains(porcelain, "AA conflict.txt") || detached != "HEAD" || branchHead != preRebaseHead {
		t.Fatalf("lane must stay mid-rebase with the branch head preserved: porcelain=%q detached=%q branchHead=%q", porcelain, detached, branchHead)
	}
	read, err := s.CodeRead(ctx, CodeReadInput{
		ProjectID: "example",
		Worktree:  state.Worktree,
		Path:      "conflict.txt",
		Live:      true,
	})
	if err != nil {
		t.Fatalf("code/read live=true must inspect controlled conflict bytes: %v", err)
	}
	if !strings.Contains(read.Content, "<<<<<<<") || !strings.Contains(read.Content, ">>>>>>>") {
		t.Fatalf("conflict markers must be inspectable: %q", read.Content)
	}
	if _, err := s.TaskExecutionSubmitRebase(ctx, "example", task.ID); err == nil {
		t.Fatal("submit-rebase on an unresolved conflict must fail")
	}
	if _, err := s.TaskExecutionTestAsync(ctx, TaskExecutionTestInput{
		ProjectID: "example",
		Key:       task.ID,
	}); err == nil {
		t.Fatal("task/test on an unresolved conflict lane must fail")
	}

	if err := os.WriteFile(filepath.Join(lanePath, "conflict.txt"), []byte("resolved\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, lanePath, "add", "conflict.txt")
	testutil.Git(t, lanePath, "-c", "core.editor=true", "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "rebase", "--continue")
	resolvedHead := strings.TrimSpace(testutil.Git(t, lanePath, "rev-parse", "HEAD"))
	if resolvedHead == preRebaseHead {
		t.Fatal("resolution must create a new head")
	}
	submitted, err := s.TaskExecutionSubmitRebase(ctx, "example", task.ID)
	if err != nil {
		t.Fatalf("submit resolved rebase: %v", err)
	}
	if submitted.Head != resolvedHead[:8] {
		t.Fatalf("submitted head=%q want=%q", submitted.Head, resolvedHead[:8])
	}
	if _, err := s.TaskExecutionReviewDecide(ctx, TaskExecutionReviewDecisionInput{
		ProjectID: "example",
		Key:       task.ID,
		Stage:     "rebase",
		Decision:  "accept",
	}); err != nil {
		t.Fatalf("accept resolved rebase: %v", err)
	}
	operation = tsk585VerifyTask(t, s, task.ID)
	if operation.Status != "completed" {
		t.Fatalf("post-resolution verification status=%q error=%q", operation.Status, operation.Error)
	}
	latest, found, _ := db.ReadLatestTaskExecutionVerification(ctx, "example", task.ID)
	if !found || latest.Outcome != model.TaskExecutionVerificationSucceeded || latest.RebaseReviewID < 1 || latest.CandidateHead != resolvedHead || latest.BaseHead != canonical {
		t.Fatalf("post-resolution receipt=%#v found=%v", latest, found)
	}
}
func TestTSK585FullSuiteArgv(t *testing.T) {
	accepted := [][]string{
		{"go", "test", "./...", "-count=1"},
		{"go", "test", "./...", "-count=1", "-race", "-timeout=2m", "-coverprofile=cover.out"},
		{"go", "test", "./...", "-count=3", "-parallel=4", "-shuffle=on"},
	}
	for _, argv := range accepted {
		if !taskExecutionVerificationFullSuiteArgv(argv) {
			t.Fatalf("full-suite argv %v must be accepted", argv)
		}
	}
	rejected := [][]string{
		{"go", "test", "./internal/service"},
		{"go", "test", "./...", "./internal/service"},
		{"go", "test", "./...", "-run=^$"},
		{"go", "test", "./...", "-run", "^$"},
		{"go", "test", "./..."},
		{"go", "test", "./...", "-count=0"},
		{"go", "test", "./...", "-count=-1"},
		{"go", "test", "./...", "-count"},
		{"go", "test", "./...", "-count", "1"},
		{"go", "test", "./...", "-timeout=0"},
		{"go", "test", "./...", "-parallel=0"},
		{"go", "test", "./...", "-coverprofile"},
		{"go", "test", "./...", "-list=."},
		{"go", "test", "./...", "-short"},
		{"go", "test", "./...", "-skip=x"},
		{"go", "test", "./...", "-bench=."},
		{"go", "test", "./...", "-fuzz=."},
		{"go", "test", "./...", "-exec=wrapper"},
		{"go", "test", "./...", "--", "-test.run=x"},
		{"go", "test", "-run=^$", "./..."},
		{"go", "test"},
		{"go", "build", "./..."},
		{"bash", "-c", "go test ./..."},
	}
	for _, argv := range rejected {
		if taskExecutionVerificationFullSuiteArgv(argv) {
			t.Fatalf("narrowed argv %v must be rejected", argv)
		}
	}
}
func TestTSK585FrozenTaskMutationDuringGates(t *testing.T) {
	s, db := tsk585Setup(t)
	defer db.Close()
	ctx := context.Background()
	gateStarted := make(chan struct{})
	releaseGates := make(chan struct{})
	s.gateExecutorWithProjectCommands = func(ctx context.Context, root string, names []string, _ model.ProjectGateCommands, _ string) ([]model.CompletionGateResult, error) {
		select {
		case <-gateStarted:
		default:
			close(gateStarted)
		}
		select {
		case <-releaseGates:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		tree, err := s.Git.TreeID(ctx, config.ProjectConfig{Root: root})
		if err != nil {
			return nil, err
		}
		out := make([]model.CompletionGateResult, len(names))
		for i, name := range names {
			out[i] = model.CompletionGateResult{ID: name, ExitCode: 0, TreeID: tree}
		}
		return out, nil
	}
	task := tsk585Task(t, s, "tsk585-frozen", "Frozen Task")
	tsk585Dispatch(t, s, task.ID)
	tsk585DriveToVerification(t, s, task.ID)
	receipt, err := s.TaskExecutionTestAsync(ctx, TaskExecutionTestInput{
		ProjectID: "example",
		Key:       task.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-gateStarted:
	case <-time.After(10 * time.Second):
		t.Fatal("verification gates did not start")
	}

	shared, err := db.ReadSharedTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	var stored model.TaskAuthoring
	if err := json.Unmarshal(shared.Payload, &stored); err != nil {
		t.Fatal(err)
	}
	title := "Mutated title while verifying"
	mutated, changed, err := trainv2.UpdateTask(stored, trainv2.AuthoringPatch{Title: &title}, "intruder", time.Now().UTC())
	if err != nil || !changed {
		t.Fatalf("mutation setup: changed=%v err=%v", changed, err)
	}
	payload, err := json.Marshal(mutated)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Shared.Exec(ctx, `UPDATE shared_tasks SET revision=?,payload=?,updated_at=? WHERE id=?`, mutated.Revision, payload, mutated.UpdatedAt.UTC().Format(time.RFC3339Nano), task.ID); err != nil {
		t.Fatal(err)
	}
	close(releaseGates)
	operation := tsk585WaitOperation(t, s, receipt.OperationID)
	if operation.Status == "completed" {
		t.Fatalf("frozen Task mutation during gates must not produce success: %#v", operation)
	}
	state, _, _ := db.ReadTaskExecutionState(ctx, "example", task.ID)
	if state.Status == model.TaskExecutionVerified {
		t.Fatalf("state=%#v must not be verified after Task mutation", state)
	}
	latest, found, _ := db.ReadLatestTaskExecutionVerification(ctx, "example", task.ID)
	if found && latest.Outcome == model.TaskExecutionVerificationSucceeded {
		t.Fatalf("no success receipt may be published: %#v", latest)
	}
}
func TestTSK585GOFLAGSNarrowsEffectiveCoverage(t *testing.T) {
	s, db := tsk585Setup(t)
	defer db.Close()
	ctx := context.Background()
	task := tsk585Task(t, s, "tsk585-goflags", "GOFLAGS Task")
	tsk585Dispatch(t, s, task.ID)
	tsk585DriveToVerification(t, s, task.ID)
	s.effectiveGOFLAGS = func(context.Context) (string, error) { return "-count=2 -v", nil }
	operation := tsk585VerifyTask(t, s, task.ID)
	if operation.Status != "completed" {
		t.Fatalf("supported GOFLAGS verification status=%q error=%q", operation.Status, operation.Error)
	}
	s.effectiveGOFLAGS = func(context.Context) (string, error) { return "-run=^TestNothing$", nil }
	if _, err := s.TaskExecutionTestAsync(ctx, TaskExecutionTestInput{
		ProjectID: "example",
		Key:       task.ID,
	}); err == nil || !strings.Contains(err.Error(), "GOFLAGS") {
		t.Fatalf("narrowing GOFLAGS must fail admission: %v", err)
	}
}
func TestTSK585VerificationNeverReusesReceipt(t *testing.T) {
	s, db := tsk585Setup(t)
	defer db.Close()
	ctx := context.Background()
	task := tsk585Task(t, s, "tsk585-noreuse", "No Reuse Task")
	tsk585Dispatch(t, s, task.ID)
	tsk585DriveToVerification(t, s, task.ID)
	lanePath, err := gitx.TaskWorktreePath(s.Config.StateDir, "example", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	names, err := s.ResolveProjectGates(ctx, "example", "integration")
	if err != nil {
		t.Fatal(err)
	}
	configuration, err := s.ProjectConfigurationRead(ctx, "example")
	if err != nil {
		t.Fatal(err)
	}
	seeded := make([]model.CompletionGateResult, len(names))
	for i, name := range names {
		seeded[i] = model.CompletionGateResult{ID: name, ExitCode: 0}
	}
	if _, _, err := s.writeProjectGatePassReceiptLocked(ctx, "example", lanePath, names, configuration.Workflow.GateCommands, "task", gates.FullTestScope(), seeded); err != nil {
		t.Fatalf("seed matching pass receipt: %v", err)
	}
	executed := 0
	s.gateExecutorWithProjectCommands = func(_ context.Context, _ string, gateNames []string, _ model.ProjectGateCommands, _ string) ([]model.CompletionGateResult, error) {
		executed++
		out := make([]model.CompletionGateResult, len(gateNames))
		for i, name := range gateNames {
			out[i] = model.CompletionGateResult{ID: name, ExitCode: 0}
		}
		return out, nil
	}
	operation := tsk585VerifyTask(t, s, task.ID)
	if operation.Status != "completed" {
		t.Fatalf("verification status=%q error=%q", operation.Status, operation.Error)
	}
	if executed == 0 {
		t.Fatal("a matching pass receipt must not substitute for fresh gate execution")
	}
	latest, found, _ := db.ReadLatestTaskExecutionVerification(ctx, "example", task.ID)
	if !found || latest.Outcome != model.TaskExecutionVerificationSucceeded {
		t.Fatalf("receipt=%#v found=%v", latest, found)
	}
	for _, result := range latest.Gates {
		if result.Execution == "reused" {
			t.Fatalf("verification must not reuse prior gate evidence: %#v", result)
		}
	}
	if _, _, err := s.loadTestPassReceipt("example"); err != nil {
		t.Fatalf("fresh execution must leave pass-receipt evidence on disk: %v", err)
	}
}
func tsk585VerifyTask(t *testing.T, s *Service, key string) durableMutationOperation {
	t.Helper()
	for attempt := 0; attempt < 3; attempt++ {
		receipt, err := s.TaskExecutionTestAsync(context.Background(), TaskExecutionTestInput{
			ProjectID: "example",
			Key:       key,
		})
		if err != nil {
			t.Fatalf("enqueue task/test: %v", err)
		}
		operation := tsk585WaitOperation(t, s, receipt.OperationID)
		if operation.Status != "failed" || !strings.Contains(operation.Error, "timing is not ordered") {
			return operation
		}
		// A transient backward wall-clock step inside the verification window
		// is environmental, not a code defect; re-enqueueing the identical
		// input re-executes the failed operation idempotently.
	}
	t.Fatal("task/test kept failing on wall-clock disorder")
	return durableMutationOperation{}
}
func tsk585DriveToVerified(t *testing.T, s *Service, key string) {
	t.Helper()
	tsk585DriveToVerification(t, s, key)
	if operation := tsk585VerifyTask(t, s, key); operation.Status != "completed" {
		t.Fatalf("task/test status=%q error=%q", operation.Status, operation.Error)
	}
}
func tsk585Integrate(t *testing.T, s *Service, key string) durableMutationOperation {
	t.Helper()
	receipt, err := s.TaskExecutionIntegrateAsync(context.Background(), TaskExecutionIntegrateInput{
		ProjectID: "example",
		Key:       key,
	})
	if err != nil {
		t.Fatalf("enqueue task/integrate: %v", err)
	}
	return tsk585WaitOperation(t, s, receipt.OperationID)
}
func tsk585MainHead(t *testing.T, s *Service) string {
	t.Helper()
	return strings.TrimSpace(string(testutil.Git(t, s.Config.Projects["example"].Root, "rev-parse", "main")))
}
func tsk585CanonicalHead(t *testing.T, s *Service) string {
	t.Helper()
	head, err := s.Git.RefreshDefaultBranch(context.Background(), s.Config.Projects["example"])
	if err != nil {
		t.Fatal(err)
	}
	return head
}
func tsk585IntegrateCapture(t *testing.T, s *Service, operationID string) taskExecutionIntegrationCapture {
	t.Helper()
	operation, err := s.readDurableMutation(operationID)
	if err != nil {
		t.Fatal(err)
	}
	capture, err := readTaskExecutionIntegrationCapture(operation)
	if err != nil {
		t.Fatalf("capture must survive the worker finish write: %v", err)
	}
	if capture.IntegrationHead == "" {
		t.Fatal("durable capture lost the write-ahead integration head")
	}
	return capture
}
func TestTSK585IntegrateLandsPreparedCommit(t *testing.T) {
	s, db := tsk585Setup(t)
	defer db.Close()
	ctx := context.Background()
	task := tsk585Task(t, s, "tsk585-integrate", "Integrate Task")
	tsk585Dispatch(t, s, task.ID)
	tsk585LaneCommit(t, s, task.ID, "candidate work")
	lanePath, err := gitx.TaskWorktreePath(s.Config.StateDir, "example", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	laneTree := strings.TrimSpace(string(testutil.Git(t, lanePath, "rev-parse", "HEAD^{tree}")))
	base := tsk585MainHead(t, s)
	tsk585DriveToVerified(t, s, task.ID)
	operation := tsk585Integrate(t, s, task.ID)
	if operation.Status != "completed" {
		t.Fatalf("integrate status=%q error=%q", operation.Status, operation.Error)
	}
	capture := tsk585IntegrateCapture(t, s, operation.OperationID)
	mainHead := tsk585MainHead(t, s)
	if mainHead != capture.IntegrationHead {
		t.Fatalf("main=%s, prepared=%s", mainHead, capture.IntegrationHead)
	}
	if got := tsk585CanonicalHead(t, s); got != capture.IntegrationHead {
		t.Fatalf("authoritative canonical resolver=%s, prepared=%s", got, capture.IntegrationHead)
	}
	if got := strings.TrimSpace(string(testutil.Git(t, s.Config.Projects["example"].Root, "ls-remote", "origin", "main"))); !strings.HasPrefix(got, capture.IntegrationHead) {
		t.Fatalf("remote main=%q want prepared %s", got, capture.IntegrationHead)
	}
	if got := strings.TrimSpace(string(testutil.Git(t, s.Config.Projects["example"].Root, "rev-parse", "main^"))); got != base {
		t.Fatalf("integration parent=%s want base=%s", got, base)
	}
	if got := strings.TrimSpace(string(testutil.Git(t, s.Config.Projects["example"].Root, "rev-parse", "main^{tree}"))); got != laneTree {
		t.Fatalf("integration tree=%s want candidate tree=%s", got, laneTree)
	}
	phase, phaseFound, phaseErr := db.ReadLatestTaskExecutionPhase(ctx, "example", task.ID, "integration")
	if phaseErr != nil || !phaseFound || phase.Head != capture.IntegrationHead || phase.EventKind != "integration" {
		t.Fatalf("integration phase evidence=%#v found=%v err=%v", phase, phaseFound, phaseErr)
	}
	if status, err := s.Git.WorktreeStatus(ctx, s.Config.Projects["example"]); err != nil || !status.Clean || status.Head != mainHead {
		t.Fatalf("canonical worktree unsynchronized: %#v err=%v", status, err)
	}
	state, found, err := db.ReadTaskExecutionState(ctx, "example", task.ID)
	if err != nil || !found || state.Status != model.TaskExecutionIntegrated {
		t.Fatalf("state=%#v found=%v err=%v", state, found, err)
	}
	shared, err := s.TaskAuthoringRead(ctx, "example", task.ID)
	if err != nil || shared.Status == model.TaskAuthoringDone {
		t.Fatalf("integration must not complete the Task lifecycle: %#v", shared.Status)
	}
	if _, err := os.Stat(lanePath); err != nil {
		t.Fatalf("integration must preserve the Task worktree: %v", err)
	}
	receipt, err := s.TaskExecutionIntegrateAsync(ctx, TaskExecutionIntegrateInput{
		ProjectID: "example",
		Key:       task.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.OperationID != operation.OperationID {
		t.Fatalf("same-input repeat must share the durable operation, got %s", receipt.OperationID)
	}
	status, err := s.TaskExecutionIntegrateOperationStatus(ctx, operation.OperationID)
	if err != nil || status.Status != "completed" || status.Result == nil {
		t.Fatalf("operation/read must surface the completed integration: %#v err=%v", status, err)
	}
	later, err := s.TaskExecutionIntegrateAsync(ctx, TaskExecutionIntegrateInput{
		ProjectID: "example",
		Key:       task.ID,
		Comment:   "again",
	})
	if err != nil {
		t.Fatal(err)
	}
	second := tsk585WaitOperation(t, s, later.OperationID)
	if second.Status != "completed" || tsk585MainHead(t, s) != capture.IntegrationHead {
		t.Fatalf("a completed Task must never gain a second canonical commit: %#v", second)
	}
	next := tsk585Task(t, s, "tsk585-next", "Next Task")
	tsk585Dispatch(t, s, next.ID)
	nextState, found, err := db.ReadTaskExecutionState(ctx, "example", next.ID)
	if err != nil || !found || nextState.BaseHead != capture.IntegrationHead {
		t.Fatalf("next dispatch must start at the integrated head: %#v found=%v err=%v", nextState, found, err)
	}
}
func TestTSK585IntegratePreCASFaultRetriesSameCommit(t *testing.T) {
	s, db := tsk585Setup(t)
	defer db.Close()
	task := tsk585Task(t, s, "tsk585-precas", "PreCAS Task")
	tsk585Dispatch(t, s, task.ID)
	tsk585LaneCommit(t, s, task.ID, "candidate")
	base := tsk585MainHead(t, s)
	tsk585DriveToVerified(t, s, task.ID)
	s.taskIntegrationFaultHook = func(_ context.Context, stage string) error {
		if stage == "prepared" {
			return context.Canceled
		}
		return nil
	}
	first := tsk585Integrate(t, s, task.ID)
	if first.Status != "outcome_unknown" {
		t.Fatalf("first attempt status=%q", first.Status)
	}
	capture := tsk585IntegrateCapture(t, s, first.OperationID)
	if got := tsk585CanonicalHead(t, s); got != base {
		t.Fatalf("canonical moved before CAS fault: %s", got)
	}
	s.taskIntegrationFaultHook = nil
	receipt, err := s.TaskExecutionIntegrateAsync(context.Background(), TaskExecutionIntegrateInput{
		ProjectID: "example",
		Key:       task.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	operation := tsk585WaitOperation(t, s, receipt.OperationID)
	if operation.Status != "completed" {
		t.Fatalf("retry status=%q error=%q", operation.Status, operation.Error)
	}
	if got := tsk585CanonicalHead(t, s); got != capture.IntegrationHead {
		t.Fatalf("retry must land the SAME prepared commit %s, got %s", capture.IntegrationHead, got)
	}
	state, _, _ := db.ReadTaskExecutionState(context.Background(), "example", task.ID)
	if state.Status != model.TaskExecutionIntegrated {
		t.Fatalf("state=%#v", state)
	}
}
func TestTSK585IntegratePersistenceFaultsRecover(t *testing.T) {
	s, db := tsk585Setup(t)
	defer db.Close()
	ctx := context.Background()
	task := tsk585Task(t, s, "tsk585-persist", "Persist Task")
	tsk585Dispatch(t, s, task.ID)
	tsk585LaneCommit(t, s, task.ID, "candidate")
	tsk585DriveToVerified(t, s, task.ID)
	calls := 0
	s.taskIntegrationWriteAhead = func(callCtx context.Context, capture taskExecutionIntegrationCapture) error {
		calls++
		if calls == 1 {
			return fmt.Errorf("injected write-ahead persistence fault")
		}
		return s.saveTaskExecutionIntegrationCapture(callCtx, capture)
	}
	receipt, err := s.TaskExecutionIntegrateAsync(ctx, TaskExecutionIntegrateInput{
		ProjectID: "example",
		Key:       task.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	operation := tsk585WaitOperation(t, s, receipt.OperationID)
	if operation.Status != "failed" {
		t.Fatalf("write-ahead persistence fault status=%q error=%q", operation.Status, operation.Error)
	}
	state, _, _ := db.ReadTaskExecutionState(ctx, "example", task.ID)
	if state.Status != model.TaskExecutionIntegrating {
		t.Fatalf("state=%#v", state)
	}
	if got := tsk585CanonicalHead(t, s); got != tsk585MainHead(t, s) {
		t.Fatal("canonical advanced without write-ahead evidence")
	}
	s.taskIntegrationWriteAhead = nil
	receipt, err = s.TaskExecutionIntegrateAsync(ctx, TaskExecutionIntegrateInput{
		ProjectID: "example",
		Key:       task.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	operation = tsk585WaitOperation(t, s, receipt.OperationID)
	if operation.Status != "completed" {
		t.Fatalf("retry after write-ahead fault status=%q error=%q", operation.Status, operation.Error)
	}
	capture := tsk585IntegrateCapture(t, s, operation.OperationID)
	if got := tsk585CanonicalHead(t, s); got != capture.IntegrationHead {
		t.Fatalf("canonical=%s prepared=%s", got, capture.IntegrationHead)
	}
	state, _, _ = db.ReadTaskExecutionState(ctx, "example", task.ID)
	if state.Status != model.TaskExecutionIntegrated {
		t.Fatalf("state=%#v", state)
	}
}
func tsk585CommitTree(t *testing.T, dir, tree, parent, message string) string {
	t.Helper()
	args := []string{"-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit-tree", tree}
	if parent != "" {
		args = append(args, "-p", parent)
	}
	args = append(args, "-m", message)
	return strings.TrimSpace(string(testutil.Git(t, dir, args...)))
}
func tsk585SetRemoteMain(t *testing.T, s *Service, head string) {
	t.Helper()
	root := s.Config.Projects["example"].Root
	origin := strings.TrimSpace(string(testutil.Git(t, root, "remote", "get-url", "origin")))
	testutil.Git(t, root, "push", "origin", head+":refs/heads/test-fixtures/"+head)
	testutil.Git(t, origin, "update-ref", "refs/heads/main", head)
	if got := tsk585CanonicalHead(t, s); got != head {
		t.Fatalf("remote main=%s want %s", got, head)
	}
}
func tsk585EmptyTree(t *testing.T, s *Service) string {
	t.Helper()
	return strings.TrimSpace(string(testutil.Git(t, s.Config.Projects["example"].Root, "hash-object", "-t", "tree", os.DevNull)))
}
func TestTSK585PushTaskIntegrationCASSuccess(t *testing.T) {
	s, db := tsk585Setup(t)
	defer db.Close()
	ctx := context.Background()
	project := s.Config.Projects["example"]
	base := tsk585CanonicalHead(t, s)
	tree := strings.TrimSpace(string(testutil.Git(t, project.Root, "rev-parse", "main^{tree}")))
	prepared, err := s.Git.PrepareTaskIntegrationCommit(ctx, project, tree, base, "integration")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Git.PushTaskIntegrationCAS(ctx, project, "main", base, tree, prepared); err != nil {
		t.Fatalf("exact expected-old CAS must succeed: %v", err)
	}
	if got := tsk585CanonicalHead(t, s); got != prepared {
		t.Fatalf("reread canonical=%s want prepared %s", got, prepared)
	}
}
func TestTSK585PushTaskIntegrationCASExpectedOldRejections(t *testing.T) {
	s, db := tsk585Setup(t)
	defer db.Close()
	ctx := context.Background()
	project := s.Config.Projects["example"]
	tree := strings.TrimSpace(string(testutil.Git(t, project.Root, "rev-parse", "main^{tree}")))
	parent := tsk585CommitTree(t, project.Root, tree, "", "rewound base")
	base := tsk585CommitTree(t, project.Root, tree, parent, "verified base")
	prepared, err := s.Git.PrepareTaskIntegrationCommit(ctx, project, tree, base, "integration")
	if err != nil {
		t.Fatal(err)
	}

	descendant := tsk585CommitTree(t, project.Root, tree, base, "concurrent advance")
	tsk585SetRemoteMain(t, s, descendant)
	if err := s.Git.PushTaskIntegrationCAS(ctx, project, "main", base, tree, prepared); err == nil {
		t.Fatal("CAS bound to B must reject when the remote advanced to D")
	}
	if got := tsk585CanonicalHead(t, s); got != descendant {
		t.Fatalf("rejected CAS moved the remote: %s want %s", got, descendant)
	}

	tsk585SetRemoteMain(t, s, parent)
	if err := s.Git.PushTaskIntegrationCAS(ctx, project, "main", base, tree, prepared); err == nil {
		t.Fatal("CAS bound to B must reject when the remote rewound to A")
	}
	if got := tsk585CanonicalHead(t, s); got != parent {
		t.Fatalf("rejected CAS moved the remote: %s want %s", got, parent)
	}

	unrelated := tsk585CommitTree(t, project.Root, tsk585EmptyTree(t, s), "", "unrelated")
	tsk585SetRemoteMain(t, s, unrelated)
	if err := s.Git.PushTaskIntegrationCAS(ctx, project, "main", base, tree, prepared); err == nil {
		t.Fatal("CAS bound to B must reject when the remote holds unrelated R")
	}
	if got := tsk585CanonicalHead(t, s); got != unrelated {
		t.Fatalf("rejected CAS moved the remote: %s want %s", got, unrelated)
	}
}
func TestTSK585PushTaskIntegrationCASRejectsBeforePush(t *testing.T) {
	s, db := tsk585Setup(t)
	defer db.Close()
	ctx := context.Background()
	project := s.Config.Projects["example"]
	tree := strings.TrimSpace(string(testutil.Git(t, project.Root, "rev-parse", "main^{tree}")))
	parent := tsk585CommitTree(t, project.Root, tree, "", "unrelated parent")
	base := tsk585CommitTree(t, project.Root, tree, parent, "verified base")
	tsk585SetRemoteMain(t, s, base)

	wrongParent := tsk585CommitTree(t, project.Root, tree, parent, "wrong parent")
	if err := s.Git.PushTaskIntegrationCAS(ctx, project, "main", base, tree, wrongParent); err == nil {
		t.Fatal("a prepared commit parented to A must be rejected before transport")
	}
	if got := tsk585CanonicalHead(t, s); got != base {
		t.Fatalf("pre-transport rejection moved the remote: %s", got)
	}

	orphan := tsk585CommitTree(t, project.Root, tsk585EmptyTree(t, s), "", "orphan")
	if err := s.Git.PushTaskIntegrationCAS(ctx, project, "main", base, tree, orphan); err == nil {
		t.Fatal("a non-descendant prepared commit must be rejected before transport")
	}
	if got := tsk585CanonicalHead(t, s); got != base {
		t.Fatalf("pre-transport rejection moved the remote: %s", got)
	}

	prepared, err := s.Git.PrepareTaskIntegrationCommit(ctx, project, tree, base, "integration")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Git.PushTaskIntegrationCAS(ctx, project, "main", base, tsk585EmptyTree(t, s), prepared); err == nil {
		t.Fatal("a verified-tree mismatch must be rejected before transport")
	}
	if got := tsk585CanonicalHead(t, s); got != base {
		t.Fatalf("pre-transport rejection moved the remote: %s", got)
	}
}
func TestTSK585IntegratePrepublishRaceNoFallback(t *testing.T) {
	s, db := tsk585Setup(t)
	defer db.Close()
	ctx := context.Background()
	task := tsk585Task(t, s, "tsk585-prepub", "Prepublish Race Task")
	tsk585Dispatch(t, s, task.ID)
	tsk585LaneCommit(t, s, task.ID, "candidate")
	base := tsk585MainHead(t, s)
	tsk585DriveToVerified(t, s, task.ID)
	s.taskIntegrationFaultHook = func(_ context.Context, stage string) error {
		if stage == "prepared" {
			return context.Canceled
		}
		return nil
	}
	first := tsk585Integrate(t, s, task.ID)
	if first.Status != "outcome_unknown" {
		t.Fatalf("first attempt status=%q", first.Status)
	}
	capture := tsk585IntegrateCapture(t, s, first.OperationID)
	if capture.BaseHead != base {
		t.Fatalf("capture base=%s want %s", capture.BaseHead, base)
	}
	prepublishCalls := 0
	s.taskIntegrationFaultHook = func(_ context.Context, stage string) error {
		if stage != "prepublish" {
			return nil
		}
		prepublishCalls++
		tsk585AdvanceCanonical(t, s)
		return nil
	}
	receipt, err := s.TaskExecutionIntegrateAsync(ctx, TaskExecutionIntegrateInput{
		ProjectID: "example",
		Key:       task.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	operation := tsk585WaitOperation(t, s, receipt.OperationID)
	if operation.Status != "failed" {
		t.Fatalf("raced integrate status=%q error=%q", operation.Status, operation.Error)
	}
	if !strings.Contains(operation.Error, "expected-old mismatch") {
		t.Fatalf("raced integrate must report the typed expected-old mismatch: %q", operation.Error)
	}
	if prepublishCalls != 1 {
		t.Fatalf("prepublish hook invocations=%d, want exactly 1 (no second publication attempt or fallback base)", prepublishCalls)
	}
	project := s.Config.Projects["example"]
	advanced := tsk585CanonicalHead(t, s)
	if advanced == base || advanced == capture.IntegrationHead {
		t.Fatalf("canonical must remain the exact concurrent advance D: %s", advanced)
	}
	if got := strings.TrimSpace(string(testutil.Git(t, project.Root, "rev-parse", "origin/main^"))); got != base {
		t.Fatalf("concurrent D must stay parented to B: parent=%s want %s", got, base)
	}
	state, _, _ := db.ReadTaskExecutionState(ctx, "example", task.ID)
	if state.Status != model.TaskExecutionVerified {
		t.Fatalf("rejected CAS must release the claim to verified: %#v", state)
	}
	tree, parents, err := s.Git.InspectTaskIntegrationCommit(ctx, project, capture.IntegrationHead)
	if err != nil {
		t.Fatal(err)
	}
	if len(parents) != 1 || parents[0] != base || tree != capture.CandidateTree {
		t.Fatalf("prepared C must remain an exact child of B: tree=%s parents=%v", tree, parents)
	}
	later, err := s.readDurableMutation(receipt.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	latest, err := readTaskExecutionIntegrationCapture(later)
	if err != nil {
		t.Fatal(err)
	}
	if latest.IntegrationHead != capture.IntegrationHead {
		t.Fatalf("a commit parented to D must never be prepared or published: %s", latest.IntegrationHead)
	}
}
func TestTSK585IntegrateConfirmedPublicationThenReplacementHolds(t *testing.T) {
	s, db := tsk585Setup(t)
	defer db.Close()
	ctx := context.Background()
	task := tsk585Task(t, s, "tsk585-replace", "Replacement Task")
	tsk585Dispatch(t, s, task.ID)
	tsk585LaneCommit(t, s, task.ID, "candidate")
	base := tsk585MainHead(t, s)
	tsk585DriveToVerified(t, s, task.ID)
	project := s.Config.Projects["example"]
	publishedCalls := 0
	var replacement string
	s.taskIntegrationFaultHook = func(_ context.Context, stage string) error {
		if stage != "published" {
			return nil
		}
		publishedCalls++
		replacement = tsk585CommitTree(t, project.Root, tsk585EmptyTree(t, s), "", "unrelated replacement")
		tsk585SetRemoteMain(t, s, replacement)
		return nil
	}
	receipt, err := s.TaskExecutionIntegrateAsync(ctx, TaskExecutionIntegrateInput{
		ProjectID: "example",
		Key:       task.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	operation := tsk585WaitOperation(t, s, receipt.OperationID)
	if operation.Status != "failed" {
		t.Fatalf("confirmed publication followed by replacement must fail exactly: status=%q error=%q", operation.Status, operation.Error)
	}
	if publishedCalls != 1 {
		t.Fatalf("published hook invocations=%d, want exactly 1", publishedCalls)
	}
	capture := tsk585IntegrateCapture(t, s, receipt.OperationID)
	if got := tsk585CanonicalHead(t, s); got != replacement {
		t.Fatalf("canonical must remain the exact unrelated replacement: %s want %s", got, replacement)
	}
	if replacement == capture.IntegrationHead || capture.BaseHead != base {
		t.Fatalf("capture must still bind B and C: %#v", capture)
	}
	state, _, _ := db.ReadTaskExecutionState(ctx, "example", task.ID)
	if state.Status != model.TaskExecutionIntegrating {
		t.Fatalf("a confirmed C landing must hold integrating, never reopen verification: %#v", state)
	}
	if _, err := s.TaskExecutionTestAsync(ctx, TaskExecutionTestInput{
		ProjectID: "example",
		Key:       task.ID,
	}); err == nil {
		t.Fatal("task/test must stay blocked while the confirmed publication is unresolved")
	}
	if _, err := s.TaskExecutionRework(ctx, TaskExecutionReworkInput{
		ProjectID: "example",
		Key:       task.ID,
		Stage:     "code",
		Comment:   "reopen",
	}); err == nil {
		t.Fatal("rework must stay blocked while the confirmed publication is unresolved")
	}
	tree, parents, err := s.Git.InspectTaskIntegrationCommit(ctx, project, capture.IntegrationHead)
	if err != nil {
		t.Fatal(err)
	}
	if len(parents) != 1 || parents[0] != base || tree != capture.CandidateTree {
		t.Fatalf("prepared C must remain an exact child of B with the candidate tree: tree=%s parents=%v", tree, parents)
	}
}
func tsk585PlannerSession(t *testing.T, s *Service) string {
	t.Helper()
	rec, err := durableSession.NewStoreWithDurability(s.Durability).Create(durableSession.CreateInput{
		ProjectID: "example", ProjectCode: "EXM", Role: durableSession.RolePlanner, SessionType: durableSession.SessionTypeChatGPT,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Fixture sessions model pre-existing Planner authority. Give the record a
	// settled creation margin so a transient backward wall-clock step between
	// session creation and the Journal write cannot invert the durable
	// ordering the admission check must verify.
	ctx := context.Background()
	row, err := s.Durability.ReadLocalSession(ctx, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	var stored durableSession.Record
	if err := json.Unmarshal(row.Payload, &stored); err != nil {
		t.Fatal(err)
	}
	settled := stored.CreatedAt.Add(-time.Minute)
	stored.CreatedAt = settled
	stored.StartedAt = settled
	payload, err := json.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Durability.UpdateLocalSession(ctx, rec.ID, row.Payload, payload, stored.UpdatedAt.UTC().Format(time.RFC3339Nano), stored.Status); err != nil {
		t.Fatal(err)
	}
	return rec.ID
}
func tsk585JournalEvidence(t *testing.T, s *Service, taskID string, commits, facts []string) model.OperatorJournalEvent {
	t.Helper()
	sessionID := tsk585PlannerSession(t, s)
	event, _, err := s.OperatorRecord(context.Background(), OperatorRecordInput{
		ProjectID:  "example",
		SessionID:  &sessionID,
		Kind:       model.OperatorTaskReview,
		Summary:    "historical integration proof",
		Content:    model.OperatorJournalContent{Facts: facts},
		References: model.OperatorJournalReferences{Tasks: []string{taskID}, Commits: commits},
		Actor:      "owner",
	})
	if err != nil {
		t.Fatal(err)
	}
	return event
}
func tsk585HistoricalIntegrate(t *testing.T, s *Service, in TaskExecutionIntegrateInput) durableMutationOperation {
	t.Helper()
	receipt, err := s.TaskExecutionIntegrateAsync(context.Background(), in)
	if err != nil {
		t.Fatalf("enqueue historical integrate: %v", err)
	}
	return tsk585WaitOperation(t, s, receipt.OperationID)
}

func tsk585HistoricalGatesFact(t *testing.T, s *Service, state model.TaskExecutionState, candidateHead, candidateTree string, gates []model.CompletionGateResult) string {
	t.Helper()
	names, profile, err := s.taskExecutionGateProfile(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	if gates == nil {
		gates = make([]model.CompletionGateResult, len(names))
		for i, name := range names {
			gates[i] = model.CompletionGateResult{ID: name, Execution: "executed", ExitCode: 0, TreeID: candidateTree, ContractDigest: strings.Repeat("c", 64), ReceiptDigest: strings.Repeat("d", 64)}
		}
	}
	raw, err := json.Marshal(taskExecutionHistoricalFullGates{
		SchemaVersion:      1,
		TaskRevisionSHA256: state.TaskRevisionSHA256,
		CandidateHead:      candidateHead,
		CandidateTree:      candidateTree,
		GateProfileSHA256:  profile,
		Gates:              gates,
	})
	if err != nil {
		t.Fatal(err)
	}
	return taskExecutionHistoricalFullGatesPrefix + string(raw)
}
func tsk585GitSnapshot(t *testing.T, s *Service) map[string]string {
	t.Helper()
	root := s.Config.Projects["example"].Root
	return map[string]string{
		"show-ref":  testutil.Git(t, root, "show-ref"),
		"head":      strings.TrimSpace(testutil.Git(t, root, "rev-parse", "HEAD")),
		"porcelain": testutil.Git(t, root, "status", "--porcelain"),
	}
}
func tsk585AssertGitSnapshotUnchanged(t *testing.T, s *Service, before map[string]string) {
	t.Helper()
	after := tsk585GitSnapshot(t, s)
	for k, v := range before {
		if after[k] != v {
			t.Fatalf("git fixture %s changed during historical recognition: %q -> %q", k, v, after[k])
		}
	}
}
func tsk585PhaseEnvelope(t *testing.T, phase sqlitestore.TaskExecutionPhase) taskExecutionHistoricalPhaseEvidence {
	t.Helper()
	if !strings.HasPrefix(phase.Comment, taskExecutionHistoricalPhasePrefix) {
		t.Fatalf("phase comment is not a historical envelope: %q", phase.Comment)
	}
	var envelope taskExecutionHistoricalPhaseEvidence
	if err := decodeStrict([]byte(strings.TrimPrefix(phase.Comment, taskExecutionHistoricalPhasePrefix)), &envelope); err != nil {
		t.Fatalf("historical phase envelope must parse strictly: %v", err)
	}
	return envelope
}
func tsk585LaneTree(t *testing.T, s *Service, taskID string) string {
	t.Helper()
	lanePath, err := gitx.TaskWorktreePath(s.Config.StateDir, "example", taskID)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(testutil.Git(t, lanePath, "rev-parse", "HEAD^{tree}"))
}
func TestTSK585HistoricalLegacyRecognizesLandedCommit(t *testing.T) {
	s, db := tsk585Setup(t)
	defer db.Close()
	ctx := context.Background()
	task := tsk585Task(t, s, "tsk585-hist-legacy", "Historical Legacy")
	tsk585Dispatch(t, s, task.ID)
	tsk585LaneCommit(t, s, task.ID, "candidate")
	tsk585DriveToVerification(t, s, task.ID)
	stateBefore, _, _ := db.ReadTaskExecutionState(ctx, "example", task.ID)
	if stateBefore.Status != model.TaskExecutionReadyForVerification {
		t.Fatalf("state=%#v", stateBefore)
	}
	project := s.Config.Projects["example"]
	lanePath, err := gitx.TaskWorktreePath(s.Config.StateDir, "example", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	laneHead := strings.TrimSpace(testutil.Git(t, lanePath, "rev-parse", "HEAD"))
	laneTree := tsk585LaneTree(t, s, task.ID)
	base := tsk585MainHead(t, s)
	integration := tsk585CommitTree(t, project.Root, laneTree, base, "historical landing")
	tsk585SetRemoteMain(t, s, integration)
	event := tsk585JournalEvidence(t, s, task.ID, []string{integration}, nil)
	snapshot := tsk585GitSnapshot(t, s)

	operation := tsk585HistoricalIntegrate(t, s, TaskExecutionIntegrateInput{
		ProjectID: "example",
		Key:       task.ID,
		Comment:   "recognized",
		Mode:      "historical",
		Historical: &TaskExecutionHistoricalIntegrationInput{
			IntegrationHead: integration,
			Profile:         "legacy",
			Evidence:        event.ID,
		},
	})
	if operation.Status != "completed" {
		t.Fatalf("historical legacy status=%q error=%q", operation.Status, operation.Error)
	}
	state, _, _ := db.ReadTaskExecutionState(ctx, "example", task.ID)
	if state.Status != model.TaskExecutionIntegrated || state.Head != stateBefore.Head || state.BaseHead != stateBefore.BaseHead || state.Branch != stateBefore.Branch || state.Agent != stateBefore.Agent {
		t.Fatalf("historical recognition must preserve the frozen lane identity: %#v", state)
	}
	phases, err := db.ReadTaskExecutionPhases(ctx, "example", task.ID, "integration")
	if err != nil || len(phases) != 1 {
		t.Fatalf("integration phases=%v err=%v", phases, err)
	}
	envelope := tsk585PhaseEnvelope(t, phases[0])
	if envelope.SchemaVersion != 1 || envelope.Mode != "historical" || envelope.Profile != "legacy" || envelope.Evidence != event.ID || envelope.IntegrationHead != integration || envelope.Comment != "recognized" {
		t.Fatalf("phase envelope=%#v", envelope)
	}
	if phases[0].Head != integration || phases[0].EventKind != "integration" || phases[0].Decision != "accept" || phases[0].TaskRevisionSHA256 != state.TaskRevisionSHA256 {
		t.Fatalf("phase=%#v", phases[0])
	}
	if _, found, _ := db.ReadLatestTaskExecutionVerification(ctx, "example", task.ID); found {
		t.Fatal("historical recognition must never create a verification receipt")
	}
	tsk585AssertGitSnapshotUnchanged(t, s, snapshot)
	if got := strings.TrimSpace(testutil.Git(t, lanePath, "rev-parse", "HEAD")); got != laneHead {
		t.Fatalf("task lane moved: %s", got)
	}
	if got := tsk585CanonicalHead(t, s); got != integration {
		t.Fatalf("canonical remote moved: %s", got)
	}
}
func TestTSK585HistoricalBootstrapFullRecognizesFromDispatched(t *testing.T) {
	s, db := tsk585Setup(t)
	defer db.Close()
	ctx := context.Background()
	project := s.Config.Projects["example"]
	task := tsk585Task(t, s, "tsk585-hist-full", "Historical Full")
	tsk585Dispatch(t, s, task.ID)
	state, _, _ := db.ReadTaskExecutionState(ctx, "example", task.ID)
	if state.Status != model.TaskExecutionDispatched || state.Head != state.BaseHead {
		t.Fatalf("dispatched state must carry the stale durable base head: %#v", state)
	}
	lanePath, err := gitx.TaskWorktreePath(s.Config.StateDir, "example", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	tsk585FileCommit(t, lanePath, "candidate.txt", "candidate\n", "candidate")
	candidate := strings.TrimSpace(testutil.Git(t, lanePath, "rev-parse", "HEAD"))
	candidateTree := strings.TrimSpace(testutil.Git(t, lanePath, "rev-parse", "HEAD^{tree}"))
	candidateBranch := strings.TrimSpace(testutil.Git(t, lanePath, "branch", "--show-current"))
	if candidateBranch != state.Branch {
		t.Fatalf("lane branch=%q state.Branch=%q", candidateBranch, state.Branch)
	}
	frozen, _, _ := db.ReadTaskExecutionState(ctx, "example", task.ID)
	if frozen.Status != model.TaskExecutionDispatched || frozen.Head != state.BaseHead {
		t.Fatalf("lane commit must not move durable state: %#v", frozen)
	}
	integration := tsk585CommitTree(t, project.Root, candidateTree, state.BaseHead, "historical landing")
	tsk585SetRemoteMain(t, s, integration)
	fact := tsk585HistoricalGatesFact(t, s, state, candidate, candidateTree, nil)
	event := tsk585JournalEvidence(t, s, task.ID, []string{integration, candidate, state.BaseHead}, []string{fact})
	snapshot := tsk585GitSnapshot(t, s)

	operation := tsk585HistoricalIntegrate(t, s, TaskExecutionIntegrateInput{
		ProjectID: "example",
		Key:       task.ID,
		Mode:      "historical",
		Historical: &TaskExecutionHistoricalIntegrationInput{
			IntegrationHead: integration,
			Profile:         "bootstrap_full",
			Evidence:        event.ID,
			CandidateHead:   candidate,
			MainBase:        state.BaseHead,
		},
	})
	if operation.Status != "completed" {
		t.Fatalf("historical bootstrap_full status=%q error=%q", operation.Status, operation.Error)
	}
	final, _, _ := db.ReadTaskExecutionState(ctx, "example", task.ID)
	if final.Status != model.TaskExecutionIntegrated || final.Head != state.BaseHead {
		t.Fatalf("recognition must preserve the stale durable lane head: %#v", final)
	}
	phases, _ := db.ReadTaskExecutionPhases(ctx, "example", task.ID, "integration")
	if len(phases) != 1 {
		t.Fatalf("phases=%v", phases)
	}
	envelope := tsk585PhaseEnvelope(t, phases[0])
	if envelope.Profile != "bootstrap_full" || envelope.CandidateHead != candidate || envelope.MainBase != state.BaseHead {
		t.Fatalf("envelope=%#v", envelope)
	}
	for _, stage := range []string{"code", "tests", "rebase"} {
		other, _ := db.ReadTaskExecutionPhases(ctx, "example", task.ID, stage)
		if len(other) != 0 {
			t.Fatalf("bootstrap_full must not fabricate %s review phases: %v", stage, other)
		}
	}
	if _, found, _ := db.ReadLatestTaskExecutionVerification(ctx, "example", task.ID); found {
		t.Fatal("historical recognition must never create a verification receipt")
	}
	if got := strings.TrimSpace(testutil.Git(t, lanePath, "rev-parse", "HEAD")); got != candidate {
		t.Fatalf("physical lane moved: %s", got)
	}
	if got := strings.TrimSpace(testutil.Git(t, lanePath, "branch", "--show-current")); got != state.Branch {
		t.Fatalf("lane branch moved: %s", got)
	}
	if got := testutil.Git(t, lanePath, "status", "--porcelain"); got != "" {
		t.Fatalf("lane left dirty: %q", got)
	}
	tsk585AssertGitSnapshotUnchanged(t, s, snapshot)
}
func TestTSK585HistoricalSessionAuthority(t *testing.T) {
	s, db := tsk585Setup(t)
	defer db.Close()
	ctx := context.Background()
	task := tsk585Task(t, s, "tsk585-hist-auth", "Session Authority")
	tsk585Dispatch(t, s, task.ID)
	tsk585LaneCommit(t, s, task.ID, "candidate")
	tsk585DriveToVerification(t, s, task.ID)
	stateBefore, _, _ := db.ReadTaskExecutionState(ctx, "example", task.ID)
	project := s.Config.Projects["example"]
	base := tsk585MainHead(t, s)
	integration := tsk585CommitTree(t, project.Root, tsk585LaneTree(t, s, task.ID), base, "landing")
	tsk585SetRemoteMain(t, s, integration)
	record := func(sessionID *string, actor string) string {
		t.Helper()
		event, _, err := s.OperatorRecord(ctx, OperatorRecordInput{
			ProjectID:  "example",
			SessionID:  sessionID,
			Kind:       model.OperatorTaskReview,
			Summary:    "evidence",
			References: model.OperatorJournalReferences{Tasks: []string{task.ID}, Commits: []string{integration}},
			Actor:      actor,
		})
		if err != nil {
			t.Fatal(err)
		}
		return event.ID
	}
	store := durableSession.NewStoreWithDurability(s.Durability)
	agentRef := "example_master"
	agentSession, err := store.Create(durableSession.CreateInput{ProjectID: "example", ProjectCode: "EXM", Role: durableSession.RoleWorker, SessionType: durableSession.SessionTypeChatGPT, SessionRef: &agentRef})
	if err != nil {
		t.Fatal(err)
	}
	endedSession, err := store.Create(durableSession.CreateInput{ProjectID: "example", ProjectCode: "EXM", Role: durableSession.RolePlanner, SessionType: durableSession.SessionTypeChatGPT})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.End(endedSession.ID); err != nil {
		t.Fatal(err)
	}
	crossProject, err := store.Create(durableSession.CreateInput{ProjectID: "other", ProjectCode: "ZZZ", Role: durableSession.RolePlanner, SessionType: durableSession.SessionTypeChatGPT})
	if err != nil {
		t.Fatal(err)
	}
	missing := record(nil, "planner")
	nonexistentID := "SP-EXM-0000"
	nonexistent := record(&nonexistentID, "planner")
	agent := record(&agentSession.ID, "planner")
	ended := record(&endedSession.ID, "planner")
	cross := record(&crossProject.ID, "planner")
	validSession := tsk585PlannerSession(t, s)
	ownerActorEvent, _, err := s.OperatorRecord(ctx, OperatorRecordInput{
		ProjectID:  "example",
		SessionID:  &validSession,
		Kind:       model.OperatorTaskReview,
		Summary:    "evidence",
		References: model.OperatorJournalReferences{Tasks: []string{task.ID}, Commits: []string{integration}},
		Actor:      "owner",
	})
	if err != nil {
		t.Fatal(err)
	}
	reject := func(name, evidenceID string) {
		t.Helper()
		receipt, err := s.TaskExecutionIntegrateAsync(ctx, TaskExecutionIntegrateInput{
			ProjectID: "example",
			Key:       task.ID,
			Mode:      "historical",
			Historical: &TaskExecutionHistoricalIntegrationInput{
				IntegrationHead: integration,
				Profile:         "legacy",
				Evidence:        evidenceID,
			},
		})
		if err != nil {
			t.Fatalf("%s: enqueue: %v", name, err)
		}
		operation := tsk585WaitOperation(t, s, receipt.OperationID)
		if operation.Status != "failed" || !strings.Contains(operation.Error, "Planner Session") {
			t.Fatalf("%s must fail on Planner Session authority: status=%q error=%q", name, operation.Status, operation.Error)
		}
		state, _, _ := db.ReadTaskExecutionState(ctx, "example", task.ID)
		if state != stateBefore {
			t.Fatalf("%s mutated execution state", name)
		}
	}
	reject("missing session", missing)
	reject("nonexistent session", nonexistent)
	reject("agent session with planner actor", agent)
	reject("ended planner session", ended)
	reject("cross-project planner session", cross)
	postDatedID := "HOM_EXM_P_zzzzz"
	postDatedEvent := record(&postDatedID, "planner")
	time.Sleep(2 * time.Millisecond)
	postStore := durableSession.NewStoreWithGateway(s.Durability, "HOM")
	postStore.TypedIDGenerator = func(string) (string, error) { return postDatedID, nil }
	if _, err := postStore.Create(durableSession.CreateInput{ProjectID: "example", ProjectCode: "EXM", Role: durableSession.RolePlanner, SessionType: durableSession.SessionTypeChatGPT}); err != nil {
		t.Fatal(err)
	}
	reject("post-dated planner session", postDatedEvent)
	operation := tsk585HistoricalIntegrate(t, s, TaskExecutionIntegrateInput{
		ProjectID: "example",
		Key:       task.ID,
		Mode:      "historical",
		Historical: &TaskExecutionHistoricalIntegrationInput{
			IntegrationHead: integration,
			Profile:         "legacy",
			Evidence:        ownerActorEvent.ID,
		},
	})
	if operation.Status != "completed" {
		t.Fatalf("active Planner session with non-planner Actor must succeed: status=%q error=%q", operation.Status, operation.Error)
	}
}
func TestTSK585HistoricalProvenRaceFailsClosed(t *testing.T) {
	s, db := tsk585Setup(t)
	defer db.Close()
	ctx := context.Background()
	task := tsk585Task(t, s, "tsk585-hist-race", "Proven Race")
	tsk585Dispatch(t, s, task.ID)
	tsk585LaneCommit(t, s, task.ID, "candidate")
	tsk585DriveToVerification(t, s, task.ID)
	stateBefore, _, _ := db.ReadTaskExecutionState(ctx, "example", task.ID)
	project := s.Config.Projects["example"]
	base := tsk585MainHead(t, s)
	integration := tsk585CommitTree(t, project.Root, tsk585LaneTree(t, s, task.ID), base, "landing")
	tsk585SetRemoteMain(t, s, integration)
	event := tsk585JournalEvidence(t, s, task.ID, []string{integration}, nil)
	s.taskIntegrationFaultHook = func(_ context.Context, stage string) error {
		if stage == "historical_proven" {
			replacement := tsk585CommitTree(t, project.Root, tsk585LaneTree(t, s, task.ID), integration, "racing canonical move")
			tsk585SetRemoteMain(t, s, replacement)
		}
		return nil
	}
	receipt, err := s.TaskExecutionIntegrateAsync(ctx, TaskExecutionIntegrateInput{
		ProjectID: "example",
		Key:       task.ID,
		Mode:      "historical",
		Historical: &TaskExecutionHistoricalIntegrationInput{
			IntegrationHead: integration,
			Profile:         "legacy",
			Evidence:        event.ID,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	operation := tsk585WaitOperation(t, s, receipt.OperationID)
	s.taskIntegrationFaultHook = nil
	if operation.Status != "failed" || !strings.Contains(operation.Error, "moved before historical") {
		t.Fatalf("post-proof canonical move must fail exactly: status=%q error=%q", operation.Status, operation.Error)
	}
	state, _, _ := db.ReadTaskExecutionState(ctx, "example", task.ID)
	if state != stateBefore {
		t.Fatalf("raced recognition must leave state byte-equal: %#v", state)
	}
	phases, _ := db.ReadTaskExecutionPhases(ctx, "example", task.ID, "integration")
	if len(phases) != 0 {
		t.Fatalf("raced recognition must not write a phase: %v", phases)
	}
}
func tsk585ReviewBases(t *testing.T, s *Service, key string) string {
	t.Helper()
	ctx := context.Background()
	tsk585Dispatch(t, s, key)
	tsk585LaneCommit(t, s, key, "candidate work")
	if _, err := s.TaskExecutionSubmitCode(ctx, "example", key); err != nil {
		t.Fatal(err)
	}
	review, err := s.TaskExecutionReview(ctx, TaskExecutionReviewInput{
		ProjectID: "example",
		Key:       key,
		Stage:     "code",
	})
	if err != nil {
		t.Fatal(err)
	}
	state, _, _ := s.Durability.ReadTaskExecutionState(ctx, "example", key)
	if review.Base != strings.ToLower(state.BaseHead[:8]) || review.Head != strings.ToLower(state.Head[:8]) || review.Stage != "code" {
		t.Fatalf("code review=%#v state=%#v", review, state)
	}
	codeHead := state.Head
	if _, err := s.TaskExecutionReviewDecide(ctx, TaskExecutionReviewDecisionInput{
		ProjectID: "example",
		Key:       key,
		Stage:     "code",
		Decision:  "accept",
	}); err != nil {
		t.Fatal(err)
	}
	tsk585LaneCommit(t, s, key, "tests work")
	if _, err := s.TaskExecutionSubmitTests(ctx, "example", key); err != nil {
		t.Fatal(err)
	}
	review, err = s.TaskExecutionReview(ctx, TaskExecutionReviewInput{
		ProjectID: "example",
		Key:       key,
		Stage:     "tests",
	})
	if err != nil {
		t.Fatal(err)
	}
	if review.Base != strings.ToLower(codeHead[:8]) {
		t.Fatalf("tests review base=%q must equal accepted code head %q", review.Base, codeHead[:8])
	}
	return codeHead
}
func TestTSK585TaskReviewBases(t *testing.T) {
	s, db := tsk585Setup(t)
	defer db.Close()
	ctx := context.Background()
	task := tsk585Task(t, s, "tsk585-review-base", "Review base")
	tsk585ReviewBases(t, s, task.ID)
	if _, err := s.TaskExecutionReviewDecide(ctx, TaskExecutionReviewDecisionInput{
		ProjectID: "example",
		Key:       task.ID,
		Stage:     "tests",
		Decision:  "accept",
	}); err != nil {
		t.Fatal(err)
	}
	operation := tsk585VerifyTask(t, s, task.ID)
	if operation.Status != "completed" {
		t.Fatalf("operation=%q %q", operation.Status, operation.Error)
	}
	tsk585AdvanceCanonical(t, s)
	operation = tsk585VerifyTask(t, s, task.ID)
	if operation.Status != "failed" {
		t.Fatalf("canonical advance op=%q %q", operation.Status, operation.Error)
	}
	state, _, _ := db.ReadTaskExecutionState(ctx, "example", task.ID)
	if state.Stage != "rebase" {
		t.Fatalf("stage=%q", state.Stage)
	}
	acceptedTests, found, err := db.ReadLatestAcceptedTaskExecutionPhase(ctx, "example", task.ID, "tests")
	if err != nil || !found {
		t.Fatal(err)
	}
	if _, err := s.TaskExecutionSubmitRebase(ctx, "example", task.ID); err != nil {
		t.Fatal(err)
	}
	review, err := s.TaskExecutionReview(ctx, TaskExecutionReviewInput{
		ProjectID: "example",
		Key:       task.ID,
		Stage:     "rebase",
	})
	if err != nil {
		t.Fatal(err)
	}
	if review.Base != strings.ToLower(acceptedTests.Head[:8]) {
		t.Fatalf("rebase review base=%q must equal accepted tests head", review.Base)
	}
	state, _, _ = db.ReadTaskExecutionState(ctx, "example", task.ID)
	supplied, err := s.CodeDiff(ctx, CodeDiffInput{
		ProjectID: "example",
		Worktree:  state.Worktree,
		Base:      review.Base,
	})
	if err != nil || supplied.Base != review.Base {
		t.Fatalf("rebase supplied diff=%#v err=%v", supplied, err)
	}
	defaultDiff, err := s.CodeDiff(ctx, CodeDiffInput{
		ProjectID: "example",
		Worktree:  state.Worktree,
	})
	if err != nil || defaultDiff.Base != strings.ToLower(state.BaseHead[:8]) || defaultDiff.Base == review.Base {
		t.Fatalf("rebase default diff=%#v err=%v", defaultDiff, err)
	}
	if supplied.Pagination != nil {
		if _, err := s.CodeDiff(ctx, CodeDiffInput{
			ProjectID: "example",
			Worktree:  state.Worktree,
			Base:      review.Base,
			Cursor:    supplied.Pagination.NextCursor,
		}); err != nil {
			t.Fatalf("supplied rebase continuation: %v", err)
		}
		if _, err := s.CodeDiff(ctx, CodeDiffInput{
			ProjectID: "example",
			Worktree:  state.Worktree,
			Cursor:    supplied.Pagination.NextCursor,
		}); err == nil {
			t.Fatal("rebase cursor must not cross to the omitted base")
		}
	}
}
func tsk585AwaitingCodeReview(t *testing.T, idem string) (*Service, *sqlitestore.Databases, model.TaskAuthoring, model.TaskExecutionState) {
	t.Helper()
	s, db := tsk585Setup(t)
	ctx := context.Background()
	task := tsk585Task(t, s, idem, "Review fixture")
	tsk585Dispatch(t, s, task.ID)
	tsk585LaneCommit(t, s, task.ID, "candidate work")
	if _, err := s.TaskExecutionSubmitCode(ctx, "example", task.ID); err != nil {
		t.Fatal(err)
	}
	state, _, _ := db.ReadTaskExecutionState(ctx, "example", task.ID)
	return s, db, task, state
}
func tsk585ReviewErr(t *testing.T, s *Service, task model.TaskAuthoring, stage string) error {
	t.Helper()
	_, err := s.TaskExecutionReview(context.Background(), TaskExecutionReviewInput{
		ProjectID: "example",
		Key:       task.ID,
		Stage:     stage,
	})
	return err
}
func tsk585LanePath(t *testing.T, s *Service, key string) string {
	t.Helper()
	path, err := gitx.TaskWorktreePath(s.Config.StateDir, "example", key)
	if err != nil {
		t.Fatal(err)
	}
	return path
}
func TestTSK585TaskReviewStaleRejections(t *testing.T) {
	ctx := context.Background()
	t.Run("no submission", func(t *testing.T) {
		s, db := tsk585Setup(t)
		defer db.Close()
		task := tsk585Task(t, s, "tsk585-rs-none", "Review none")
		tsk585Dispatch(t, s, task.ID)
		if err := tsk585ReviewErr(t, s, task, "code"); err == nil {
			t.Fatal("dispatched Task with no submission must fail review")
		}
	})
	t.Run("stage mismatch", func(t *testing.T) {
		s, db, task, _ := tsk585AwaitingCodeReview(t, "tsk585-rs-stage")
		defer db.Close()
		if err := tsk585ReviewErr(t, s, task, "tests"); err == nil {
			t.Fatal("stage mismatch must fail review")
		}
	})
	t.Run("dirty lane", func(t *testing.T) {
		s, db, task, _ := tsk585AwaitingCodeReview(t, "tsk585-rs-dirty")
		defer db.Close()
		path := tsk585LanePath(t, s, task.ID)
		if err := os.WriteFile(filepath.Join(path, "dirty.txt"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := tsk585ReviewErr(t, s, task, "code"); err == nil {
			t.Fatal("dirty lane must fail review")
		}
	})
	t.Run("wrong branch", func(t *testing.T) {
		s, db, task, _ := tsk585AwaitingCodeReview(t, "tsk585-rs-branch")
		defer db.Close()
		testutil.Git(t, tsk585LanePath(t, s, task.ID), "switch", "-c", "other-branch")
		if err := tsk585ReviewErr(t, s, task, "code"); err == nil {
			t.Fatal("wrong branch must fail review")
		}
	})
	t.Run("lane head drift", func(t *testing.T) {
		s, db, task, _ := tsk585AwaitingCodeReview(t, "tsk585-rs-drift")
		defer db.Close()
		tsk585LaneCommit(t, s, task.ID, "lane drift")
		if err := tsk585ReviewErr(t, s, task, "code"); err == nil {
			t.Fatal("lane head drift must fail review")
		}
	})
	t.Run("non descendant", func(t *testing.T) {
		s, db := tsk585Setup(t)
		defer db.Close()
		task := tsk585Task(t, s, "tsk585-rs-orphan", "Review orphan")
		tsk585Dispatch(t, s, task.ID)
		path := tsk585LanePath(t, s, task.ID)
		tree := strings.TrimSpace(testutil.Git(t, path, "write-tree"))
		unrelated := strings.TrimSpace(testutil.Git(t, path, "-c", "user.name=T", "-c", "user.email=t@x", "commit-tree", tree, "-m", "unrelated"))
		testutil.Git(t, path, "reset", "--hard", unrelated)
		if _, err := s.TaskExecutionSubmitCode(ctx, "example", task.ID); err != nil {
			t.Fatal(err)
		}
		if err := tsk585ReviewErr(t, s, task, "code"); err == nil {
			t.Fatal("candidate not descended from base must fail review")
		}
	})
	t.Run("task revision drift", func(t *testing.T) {
		s, db, task, state := tsk585AwaitingCodeReview(t, "tsk585-rs-rev")
		defer db.Close()
		corrupt := "f"
		if state.TaskRevisionSHA256[:1] == "f" {
			corrupt = "e"
		}
		if _, err := db.Shared.Exec(ctx, `UPDATE shared_task_execution_states SET task_revision_sha256=? WHERE task_id=?`, corrupt+state.TaskRevisionSHA256[1:], task.ID); err != nil {
			t.Fatal(err)
		}
		if err := tsk585ReviewErr(t, s, task, "code"); err == nil {
			t.Fatal("task revision drift must fail review")
		}
	})
}
func tsk585AwaitingTestsReview(t *testing.T, idem string) (*Service, *sqlitestore.Databases, model.TaskAuthoring, model.TaskExecutionState) {
	t.Helper()
	s, db, task, _ := tsk585AwaitingCodeReview(t, idem)
	ctx := context.Background()
	if _, err := s.TaskExecutionReviewDecide(ctx, TaskExecutionReviewDecisionInput{
		ProjectID: "example",
		Key:       task.ID,
		Stage:     "code",
		Decision:  "accept",
	}); err != nil {
		t.Fatal(err)
	}
	tsk585LaneCommit(t, s, task.ID, "tests work")
	if _, err := s.TaskExecutionSubmitTests(ctx, "example", task.ID); err != nil {
		t.Fatal(err)
	}
	state, _, _ := db.ReadTaskExecutionState(ctx, "example", task.ID)
	return s, db, task, state
}
func TestTSK585TaskReviewAcceptedArtifactRejections(t *testing.T) {
	ctx := context.Background()
	t.Run("missing accepted code", func(t *testing.T) {
		s, db, task, _ := tsk585AwaitingTestsReview(t, "tsk585-ra-missing")
		defer db.Close()
		if _, err := db.Shared.Exec(ctx, `DELETE FROM shared_task_execution_phases WHERE task_id=? AND stage='code' AND decision='accept'`, task.ID); err != nil {
			t.Fatal(err)
		}
		if err := tsk585ReviewErr(t, s, task, "tests"); err == nil {
			t.Fatal("missing accepted code phase must fail tests review")
		}
	})
	t.Run("accepted wrong status", func(t *testing.T) {
		s, db, task, _ := tsk585AwaitingTestsReview(t, "tsk585-ra-status")
		defer db.Close()
		if _, err := db.Shared.Exec(ctx, `UPDATE shared_task_execution_phases SET status='awaiting_review' WHERE task_id=? AND stage='code' AND decision='accept'`, task.ID); err != nil {
			t.Fatal(err)
		}
		if err := tsk585ReviewErr(t, s, task, "tests"); err == nil {
			t.Fatal("accepted code review with wrong status must fail tests review")
		}
	})
	t.Run("accepted future revision", func(t *testing.T) {
		s, db, task, _ := tsk585AwaitingTestsReview(t, "tsk585-ra-rev")
		defer db.Close()
		if _, err := db.Shared.Exec(ctx, `UPDATE shared_task_execution_phases SET execution_revision=999 WHERE task_id=? AND stage='code' AND decision='accept'`, task.ID); err != nil {
			t.Fatal(err)
		}
		if err := tsk585ReviewErr(t, s, task, "tests"); err == nil {
			t.Fatal("accepted code review with future revision must fail tests review")
		}
	})
	t.Run("rebase not descended", func(t *testing.T) {
		s, db, task, _ := tsk585AwaitingTestsReview(t, "tsk585-ra-rebase")
		defer db.Close()
		if _, err := s.TaskExecutionReviewDecide(ctx, TaskExecutionReviewDecisionInput{
			ProjectID: "example",
			Key:       task.ID,
			Stage:     "tests",
			Decision:  "accept",
		}); err != nil {
			t.Fatal(err)
		}
		operation := tsk585VerifyTask(t, s, task.ID)
		if operation.Status != "completed" {
			t.Fatalf("operation=%q %q", operation.Status, operation.Error)
		}
		tsk585AdvanceCanonical(t, s)
		operation = tsk585VerifyTask(t, s, task.ID)
		if operation.Status != "failed" {
			t.Fatalf("canonical advance op=%q %q", operation.Status, operation.Error)
		}
		state, _, _ := db.ReadTaskExecutionState(ctx, "example", task.ID)
		if state.Stage != "rebase" {
			t.Fatalf("stage=%q", state.Stage)
		}
		path := tsk585LanePath(t, s, task.ID)
		tree := strings.TrimSpace(testutil.Git(t, path, "write-tree"))
		unrelated := strings.TrimSpace(testutil.Git(t, path, "-c", "user.name=T", "-c", "user.email=t@x", "commit-tree", tree, "-m", "unrelated"))
		if _, err := s.TaskExecutionSubmitRebase(ctx, "example", task.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Shared.Exec(ctx, `UPDATE shared_task_execution_states SET base_head_sha=? WHERE task_id=?`, unrelated, task.ID); err != nil {
			t.Fatal(err)
		}
		if err := tsk585ReviewErr(t, s, task, "rebase"); err == nil {
			t.Fatal("rebase candidate not descended from refreshed base must fail review")
		}
	})
}

func tsk585LaneWrite(t *testing.T, s *Service, key, name, content string) {
	t.Helper()
	path := tsk585LanePath(t, s, key)
	if err := os.WriteFile(filepath.Join(path, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, path, "add", name)
	testutil.Git(t, path, "-c", "user.name=T", "-c", "user.email=t@x", "commit", "-m", "lane change")
}

func TestTSK585CodeDiffAuthorizedBase(t *testing.T) {
	ctx := context.Background()
	s, db := tsk585Setup(t)
	defer db.Close()
	task := tsk585Task(t, s, "tsk585-diff", "Diff fixture")
	tsk585Dispatch(t, s, task.ID)
	lines := make([]string, 400)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %04d of a long generated diff body to exceed the page token budget limit", i)
	}
	tsk585LaneWrite(t, s, task.ID, "big.txt", strings.Join(lines, "\n"))
	if _, err := s.TaskExecutionSubmitCode(ctx, "example", task.ID); err != nil {
		t.Fatal(err)
	}
	state, _, _ := db.ReadTaskExecutionState(ctx, "example", task.ID)
	review, err := s.TaskExecutionReview(ctx, TaskExecutionReviewInput{
		ProjectID: "example",
		Key:       task.ID,
		Stage:     "code",
	})
	if err != nil {
		t.Fatal(err)
	}
	if review.Base != strings.ToLower(state.BaseHead[:8]) {
		t.Fatalf("code review base=%q want %q", review.Base, state.BaseHead[:8])
	}
	first, err := s.CodeDiff(ctx, CodeDiffInput{
		ProjectID: "example",
		Worktree:  state.Worktree,
		Base:      review.Base,
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.Base != review.Base || first.Pagination == nil || first.Pagination.NextCursor == "" {
		t.Fatalf("supplied base diff=%#v", first)
	}
	next, err := s.CodeDiff(ctx, CodeDiffInput{
		ProjectID: "example",
		Worktree:  state.Worktree,
		Base:      review.Base,
		Cursor:    first.Pagination.NextCursor,
	})
	if err != nil || next.Base != review.Base {
		t.Fatalf("continuation diff=%#v err=%v", next, err)
	}
	if _, err := s.CodeDiff(ctx, CodeDiffInput{
		ProjectID: "example",
		Worktree:  state.Worktree,
		Base:      "00000000",
		Cursor:    first.Pagination.NextCursor,
	}); err == nil {
		t.Fatal("cursor must not survive a different supplied base")
	}
	if _, err := s.CodeDiff(ctx, CodeDiffInput{
		ProjectID: "example",
		Worktree:  state.Worktree,
		Base:      "00000000",
	}); err == nil {
		t.Fatal("unauthorized base must fail")
	}
	if _, err := s.CodeDiff(ctx, CodeDiffInput{
		ProjectID: "example",
		Worktree:  state.Worktree,
		Base:      "ABCDEF00",
	}); err == nil {
		t.Fatal("non-lowercase base must fail")
	}
	if _, err := s.CodeDiff(ctx, CodeDiffInput{
		ProjectID: "example",
		Worktree:  state.Worktree,
		Base:      state.BaseHead[:9],
	}); err == nil {
		t.Fatal("non-sha8 base must fail")
	}
	mainSelector := "WT-MAIN-" + strings.ToLower(tsk585MainHead(t, s)[:8])
	if _, err := s.CodeDiff(ctx, CodeDiffInput{
		ProjectID: "example",
		Worktree:  mainSelector,
		Base:      review.Base,
	}); err == nil {
		t.Fatal("base on a non-Task selector must fail")
	}
	empty, err := s.CodeDiff(ctx, CodeDiffInput{
		ProjectID: "example",
		Worktree:  state.Worktree,
		Paths:     []string{"zzz-missing.txt"},
	})
	if err != nil || empty.Base != strings.ToLower(state.BaseHead[:8]) || empty.Diff != "" {
		t.Fatalf("empty diff=%#v err=%v", empty, err)
	}
	if _, err := s.TaskExecutionReviewDecide(ctx, TaskExecutionReviewDecisionInput{
		ProjectID: "example",
		Key:       task.ID,
		Stage:     "code",
		Decision:  "accept",
	}); err != nil {
		t.Fatal(err)
	}
	tsk585LaneWrite(t, s, task.ID, "tests.txt", strings.Join(append([]string{"tests"}, lines[80:]...), "\n"))
	if _, err := s.TaskExecutionSubmitTests(ctx, "example", task.ID); err != nil {
		t.Fatal(err)
	}
	testsReview, err := s.TaskExecutionReview(ctx, TaskExecutionReviewInput{
		ProjectID: "example",
		Key:       task.ID,
		Stage:     "tests",
	})
	if err != nil {
		t.Fatal(err)
	}
	if testsReview.Base == strings.ToLower(state.BaseHead[:8]) {
		t.Fatal("tests base must differ from dispatch base")
	}
	state, _, _ = db.ReadTaskExecutionState(ctx, "example", task.ID)
	supplied, err := s.CodeDiff(ctx, CodeDiffInput{
		ProjectID: "example",
		Worktree:  state.Worktree,
		Base:      testsReview.Base,
	})
	if err != nil || supplied.Base != testsReview.Base || supplied.Pagination == nil || supplied.Pagination.NextCursor == "" {
		t.Fatalf("supplied tests diff=%#v err=%v", supplied, err)
	}
	if _, err := s.CodeDiff(ctx, CodeDiffInput{
		ProjectID: "example",
		Worktree:  state.Worktree,
		Cursor:    supplied.Pagination.NextCursor,
	}); err == nil {
		t.Fatal("cursor must not cross from supplied tests base to default base")
	}
	stale, err := s.CodeDiff(ctx, CodeDiffInput{
		ProjectID: "example",
		Worktree:  state.Worktree,
	})
	if err != nil || stale.Base != strings.ToLower(state.BaseHead[:8]) {
		t.Fatalf("stale default diff=%#v err=%v", stale, err)
	}
	if _, err := s.CodeDiff(ctx, CodeDiffInput{
		ProjectID: "example",
		Worktree:  state.Worktree,
		Base:      strings.ToLower(state.BaseHead[:8]),
	}); err == nil {
		t.Fatal("stale dispatch base must fail during tests review")
	}
	if stale.Pagination != nil {
		if _, err := s.CodeDiff(ctx, CodeDiffInput{
			ProjectID: "example",
			Worktree:  state.Worktree,
			Base:      testsReview.Base,
			Cursor:    stale.Pagination.NextCursor,
		}); err == nil {
			t.Fatal("cursor must not cross from default base to supplied base")
		}
	}
}
