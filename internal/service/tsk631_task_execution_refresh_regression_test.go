package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

type tsk631WorkerBinding struct {
	sessionID  string
	agentID    string
	sessionKey string
	count      int
}

func tsk631WorkerBindingSnapshot(t *testing.T, s *Service) tsk631WorkerBinding {
	t.Helper()
	worker, err := s.ResolveProjectWorker(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	records, err := durableSession.NewStoreWithDurability(s.Durability).List()
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, record := range records {
		if record.Role == durableSession.RoleWorker && record.Status == durableSession.StatusActive {
			count++
		}
	}
	return tsk631WorkerBinding{
		sessionID:  worker.Session.ID,
		agentID:    worker.Agent.AgentID,
		sessionKey: worker.Binding.SessionKey,
		count:      count,
	}
}

func tsk631AssertWorkerBinding(t *testing.T, s *Service, want tsk631WorkerBinding) {
	t.Helper()
	got := tsk631WorkerBindingSnapshot(t, s)
	if got != want {
		t.Fatalf("Worker binding changed: before=%#v after=%#v", want, got)
	}
}

func tsk631AssertCleanLane(t *testing.T, s *Service, state model.TaskExecutionState) {
	t.Helper()
	lane, err := s.taskExecutionLane("example", state.TaskID, state)
	if err != nil {
		t.Fatal(err)
	}
	head, branch, clean, err := s.Git.CurrentHead(context.Background(), lane)
	if err != nil || !clean || branch != state.Branch || head != state.Head {
		t.Fatalf("lane identity=%#v head=%s branch=%s clean=%v err=%v", state, head, branch, clean, err)
	}
}

func tsk631AssertPhysicalLane(t *testing.T, s *Service, key, wantHead, wantBranch string) {
	t.Helper()
	path := tsk585LanePath(t, s, key)
	head, branch, clean, err := s.Git.CurrentHead(context.Background(), config.ProjectConfig{Root: path})
	if err != nil || !clean || head != wantHead || branch != wantBranch {
		t.Fatalf("physical lane key=%s head=%s branch=%s clean=%v wantHead=%s wantBranch=%s err=%v", key, head, branch, clean, wantHead, wantBranch, err)
	}
}

func tsk631AdvanceCanonical(t *testing.T, s *Service, parent, content string, unrelated bool) string {
	t.Helper()
	project := s.Config.Projects["example"]
	if unrelated {
		tree := strings.TrimSpace(string(testutil.Git(t, project.Root, "rev-parse", "main^{tree}")))
		head := tsk585CommitTree(t, project.Root, tree, "", "unrelated canonical head")
		tsk585SetRemoteMain(t, s, head)
		return head
	}
	if got := tsk585MainHead(t, s); got != parent {
		t.Fatalf("canonical parent=%s want=%s", got, parent)
	}
	contentPath := filepath.Join(project.Root, "tsk631-bootstrap-content.txt")
	if err := os.WriteFile(contentPath, []byte(content+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, project.Root, "add", "tsk631-bootstrap-content.txt")
	testutil.Git(t, project.Root, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-m", "accepted bootstrap publication")
	head := tsk585MainHead(t, s)
	tsk585SetRemoteMain(t, s, head)
	return head
}

func TestTSK631RefreshesBlockedPreworkAndPreservesIdentity(t *testing.T) {
	s, db := tsk585Setup(t)
	defer db.Close()
	ctx := context.Background()
	task := tsk585Task(t, s, "tsk631-refresh-success", "TSK631 refresh")
	tsk585Dispatch(t, s, task.ID)
	before, found, err := db.ReadTaskExecutionState(ctx, "example", task.ID)
	if err != nil || !found {
		t.Fatal(err)
	}
	binding := tsk631WorkerBindingSnapshot(t, s)
	if _, err := s.TaskExecutionBlock(ctx, TaskExecutionBlockInput{
		ProjectID: "example",
		Key:       task.ID,
		Reason:    "canonical publication pending",
	}); err != nil {
		t.Fatal(err)
	}
	blocked, found, err := db.ReadTaskExecutionState(ctx, "example", task.ID)
	if err != nil || !found || blocked.Status != model.TaskExecutionBlocked {
		t.Fatalf("blocked state=%#v found=%v err=%v", blocked, found, err)
	}
	phasesBefore, err := db.ReadTaskExecutionPhases(ctx, "example", task.ID, "code")
	if err != nil {
		t.Fatal(err)
	}
	newHead := tsk631AdvanceCanonical(t, s, before.BaseHead, "TSK631 bootstrap content v1", false)
	refreshed, err := s.TaskExecutionRefresh(ctx, TaskExecutionRefreshInput{
		ProjectID: "example",
		Key:       task.ID,
		Reason:    "accepted bootstrap is now canonical",
	})
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.Status != model.TaskExecutionBlocked || refreshed.Head != newHead[:8] || refreshed.ExecutionRevision != blocked.ExecutionRevision+1 || refreshed.Reason != "accepted bootstrap is now canonical" {
		t.Fatalf("refresh output=%#v", refreshed)
	}
	updated, found, err := db.ReadTaskExecutionState(ctx, "example", task.ID)
	if err != nil || !found || updated.BaseHead != newHead || updated.Head != newHead || updated.Worktree == blocked.Worktree || updated.Branch != before.Branch || updated.Agent != before.Agent || updated.TaskRevision != before.TaskRevision || updated.TaskRevisionSHA256 != before.TaskRevisionSHA256 || updated.ExecutionRevision != blocked.ExecutionRevision+1 {
		t.Fatalf("refreshed state=%#v found=%v err=%v", updated, found, err)
	}
	tsk631AssertCleanLane(t, s, updated)
	content, err := os.ReadFile(filepath.Join(tsk585LanePath(t, s, task.ID), "tsk631-bootstrap-content.txt"))
	if err != nil || string(content) != "TSK631 bootstrap content v1\n" {
		t.Fatalf("refreshed lane content=%q err=%v", content, err)
	}
	tsk631AssertWorkerBinding(t, s, binding)
	phasesAfterRefresh, err := db.ReadTaskExecutionPhases(ctx, "example", task.ID, "code")
	if err != nil || len(phasesAfterRefresh) != len(phasesBefore)+1 || !reflect.DeepEqual(phasesBefore, phasesAfterRefresh[:len(phasesBefore)]) || phasesAfterRefresh[len(phasesBefore)].EventKind != "refresh" {
		t.Fatalf("refresh history before=%#v after=%#v err=%v", phasesBefore, phasesAfterRefresh, err)
	}
	var evidence struct {
		OldBase            string `json:"old_base"`
		NewBase            string `json:"new_base"`
		OldHead            string `json:"old_head"`
		NewHead            string `json:"new_head"`
		Reason             string `json:"reason"`
		ExecutionRevision  int    `json:"execution_revision"`
		Stage              string `json:"stage"`
		Status             string `json:"status"`
		Branch             string `json:"branch"`
		TaskRevisionSHA256 string `json:"task_revision_sha256"`
	}
	refreshPhase := phasesAfterRefresh[len(phasesBefore)]
	if err := json.Unmarshal([]byte(refreshPhase.Comment), &evidence); err != nil || evidence.OldBase != before.BaseHead || evidence.NewBase != newHead || evidence.OldHead != before.Head || evidence.NewHead != newHead || evidence.Reason != "accepted bootstrap is now canonical" || evidence.ExecutionRevision != refreshPhase.ExecutionRevision || evidence.Stage != refreshPhase.Stage || evidence.Status != refreshPhase.Status || evidence.Branch != refreshPhase.Branch || evidence.TaskRevisionSHA256 != refreshPhase.TaskRevisionSHA256 {
		t.Fatalf("refresh evidence=%#v err=%v", evidence, err)
	}

	restarted := NewWithDurabilityDeferredWorkers(s.Config, db)
	status, err := restarted.TaskExecutionStatus(ctx, "example", task.ID)
	if err != nil || status.Status != model.TaskExecutionBlocked {
		t.Fatalf("restart status=%#v err=%v", status, err)
	}
	tsk631AssertWorkerBinding(t, restarted, binding)
	resumed, err := restarted.TaskExecutionResume(ctx, TaskExecutionResumeInput{
		ProjectID: "example",
		Key:       task.ID,
		Reason:    "canonical publication complete",
	})
	if err != nil || resumed.Status != before.Status || resumed.Head != newHead[:8] || resumed.Worktree != updated.Worktree || resumed.Agent != before.Agent {
		t.Fatalf("resume output=%#v err=%v", resumed, err)
	}
	afterResume, found, err := db.ReadTaskExecutionState(ctx, "example", task.ID)
	if err != nil || !found || afterResume.BaseHead != newHead || afterResume.Head != newHead || afterResume.Branch != before.Branch || afterResume.Agent != before.Agent || afterResume.ExecutionRevision != updated.ExecutionRevision+1 {
		t.Fatalf("resume state=%#v found=%v err=%v", afterResume, found, err)
	}
	tsk631AssertWorkerBinding(t, restarted, binding)
	phasesAfterResume, err := db.ReadTaskExecutionPhases(ctx, "example", task.ID, "code")
	if err != nil || len(phasesAfterResume) != len(phasesAfterRefresh)+1 || !reflect.DeepEqual(phasesAfterRefresh, phasesAfterResume[:len(phasesAfterRefresh)]) || phasesAfterResume[len(phasesAfterRefresh)].EventKind != "resume" {
		t.Fatalf("resume history before=%#v after=%#v err=%v", phasesAfterRefresh, phasesAfterResume, err)
	}
	tsk585LaneCommit(t, restarted, task.ID, "worker proceeds after refresh")
	if submitted, err := restarted.TaskExecutionSubmitCode(WithAgentSessionID(ctx, binding.sessionID), "example", task.ID); err != nil || submitted.Status != model.TaskExecutionAwaitingReview {
		t.Fatalf("Worker execution after refresh output=%#v err=%v", submitted, err)
	}
	next := tsk585Task(t, restarted, "tsk631-refresh-dispatch", "dispatch after publication")
	tsk585Dispatch(t, restarted, next.ID)
	nextState, found, err := db.ReadTaskExecutionState(ctx, "example", next.ID)
	if err != nil || !found || nextState.BaseHead != newHead {
		t.Fatalf("post-publication dispatch state=%#v found=%v err=%v", nextState, found, err)
	}
}

func TestTSK631RefreshFinalizesAfterLaneMoveCrash(t *testing.T) {
	s, db := tsk585Setup(t)
	defer db.Close()
	ctx := context.Background()
	task := tsk585Task(t, s, "tsk631-refresh-crash", "TSK631 refresh crash recovery")
	tsk585Dispatch(t, s, task.ID)
	before, found, err := db.ReadTaskExecutionState(ctx, "example", task.ID)
	if err != nil || !found {
		t.Fatal(err)
	}
	binding := tsk631WorkerBindingSnapshot(t, s)
	if _, err := s.TaskExecutionBlock(ctx, TaskExecutionBlockInput{
		ProjectID: "example",
		Key:       task.ID,
		Reason:    "crash recovery boundary",
	}); err != nil {
		t.Fatal(err)
	}
	blocked, _, _ := db.ReadTaskExecutionState(ctx, "example", task.ID)
	phasesBefore, err := db.ReadTaskExecutionPhases(ctx, "example", task.ID, "code")
	if err != nil {
		t.Fatal(err)
	}
	newHead := tsk631AdvanceCanonical(t, s, before.BaseHead, "TSK631 crash content", false)
	s.taskExecutionRefreshFaultHook = func(_ context.Context, stage string) error {
		if stage == "after_lane_reconcile" {
			return errors.New("injected refresh crash after lane move")
		}
		return nil
	}
	if _, err := s.TaskExecutionRefresh(ctx, TaskExecutionRefreshInput{
		ProjectID: "example",
		Key:       task.ID,
		Reason:    "retry after crash",
	}); err == nil {
		t.Fatal("refresh crash injection was not observed")
	}
	s.taskExecutionRefreshFaultHook = nil
	durableAfterFailure, found, err := db.ReadTaskExecutionState(ctx, "example", task.ID)
	if err != nil || !found || durableAfterFailure != blocked {
		t.Fatalf("crash changed durable state: before=%#v after=%#v found=%v err=%v", blocked, durableAfterFailure, found, err)
	}
	phasesAfterFailure, err := db.ReadTaskExecutionPhases(ctx, "example", task.ID, "code")
	if err != nil || !reflect.DeepEqual(phasesBefore, phasesAfterFailure) {
		t.Fatalf("crash changed durable history: before=%#v after=%#v err=%v", phasesBefore, phasesAfterFailure, err)
	}
	tsk631AssertPhysicalLane(t, s, task.ID, newHead, before.Branch)

	restarted := NewWithDurabilityDeferredWorkers(s.Config, db)
	recovered, err := restarted.TaskExecutionRefresh(ctx, TaskExecutionRefreshInput{
		ProjectID: "example",
		Key:       task.ID,
		Reason:    "finalize recovered refresh",
	})
	if err != nil || recovered.Status != model.TaskExecutionBlocked || recovered.Head != newHead[:8] || recovered.ExecutionRevision != blocked.ExecutionRevision+1 {
		t.Fatalf("recovered refresh=%#v err=%v", recovered, err)
	}
	state, found, err := db.ReadTaskExecutionState(ctx, "example", task.ID)
	if err != nil || !found || state.BaseHead != newHead || state.Head != newHead || state.Branch != before.Branch || state.Agent != before.Agent || state.TaskRevision != before.TaskRevision || state.TaskRevisionSHA256 != before.TaskRevisionSHA256 {
		t.Fatalf("recovered state=%#v found=%v err=%v", state, found, err)
	}
	tsk631AssertCleanLane(t, restarted, state)
	tsk631AssertWorkerBinding(t, restarted, binding)
	phasesAfterRecovery, err := db.ReadTaskExecutionPhases(ctx, "example", task.ID, "code")
	if err != nil || len(phasesAfterRecovery) != len(phasesBefore)+1 || !reflect.DeepEqual(phasesBefore, phasesAfterRecovery[:len(phasesBefore)]) || phasesAfterRecovery[len(phasesBefore)].EventKind != "refresh" {
		t.Fatalf("recovery history=%#v err=%v", phasesAfterRecovery, err)
	}
	content, err := os.ReadFile(filepath.Join(tsk585LanePath(t, restarted, task.ID), "tsk631-bootstrap-content.txt"))
	if err != nil || string(content) != "TSK631 crash content\n" {
		t.Fatalf("recovered lane content=%q err=%v", content, err)
	}
	if _, err := restarted.TaskExecutionRefresh(ctx, TaskExecutionRefreshInput{
		ProjectID: "example",
		Key:       task.ID,
		Reason:    "idempotent retry",
	}); err != nil {
		t.Fatal(err)
	}
	phasesAfterRetry, err := db.ReadTaskExecutionPhases(ctx, "example", task.ID, "code")
	if err != nil || !reflect.DeepEqual(phasesAfterRecovery, phasesAfterRetry) {
		t.Fatalf("retry duplicated refresh history: recovered=%#v retry=%#v err=%v", phasesAfterRecovery, phasesAfterRetry, err)
	}
	refreshCount := 0
	for _, phase := range phasesAfterRetry {
		if phase.EventKind == "refresh" {
			refreshCount++
		}
	}
	if refreshCount != 1 {
		t.Fatalf("refresh phase count=%d want exactly one", refreshCount)
	}
}

func TestTSK631RefreshChainRejectsCorruptIntermediateEvidence(t *testing.T) {
	s, db := tsk585Setup(t)
	defer db.Close()
	ctx := context.Background()
	task := tsk585Task(t, s, "tsk631-refresh-corrupt-chain", "TSK631 refresh chain corruption")
	tsk585Dispatch(t, s, task.ID)
	before, _, _ := db.ReadTaskExecutionState(ctx, "example", task.ID)
	if _, err := s.TaskExecutionBlock(ctx, TaskExecutionBlockInput{
		ProjectID: "example",
		Key:       task.ID,
		Reason:    "chain corruption boundary",
	}); err != nil {
		t.Fatal(err)
	}
	firstHead := tsk631AdvanceCanonical(t, s, before.BaseHead, "TSK631 chain content v1", false)
	if _, err := s.TaskExecutionRefresh(ctx, TaskExecutionRefreshInput{
		ProjectID: "example",
		Key:       task.ID,
		Reason:    "first canonical refresh",
	}); err != nil {
		t.Fatal(err)
	}
	secondHead := tsk631AdvanceCanonical(t, s, firstHead, "TSK631 chain content v2", false)
	if _, err := s.TaskExecutionRefresh(ctx, TaskExecutionRefreshInput{
		ProjectID: "example",
		Key:       task.ID,
		Reason:    "second canonical refresh",
	}); err != nil {
		t.Fatal(err)
	}
	stateBefore, found, err := db.ReadTaskExecutionState(ctx, "example", task.ID)
	if err != nil || !found || stateBefore.BaseHead != secondHead {
		t.Fatalf("second refresh state=%#v found=%v err=%v", stateBefore, found, err)
	}
	phasesBefore, err := db.ReadTaskExecutionPhases(ctx, "example", task.ID, "code")
	if err != nil {
		t.Fatal(err)
	}
	refreshIndex := -1
	for i, phase := range phasesBefore {
		if phase.EventKind == "refresh" {
			refreshIndex = i
			break
		}
	}
	if refreshIndex < 0 {
		t.Fatal("missing refresh phase to corrupt")
	}
	var corrupted map[string]any
	if err := json.Unmarshal([]byte(phasesBefore[refreshIndex].Comment), &corrupted); err != nil {
		t.Fatal(err)
	}
	corrupted["new_base"] = corrupted["old_base"]
	comment, err := json.Marshal(corrupted)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Local.Exec(ctx, `UPDATE local_task_execution_phases SET comment=? WHERE id=?`, string(comment), phasesBefore[refreshIndex].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TaskExecutionStatus(ctx, "example", task.ID); err == nil {
		t.Fatal("corrupt refresh chain was exposed as valid status")
	}
	if _, err := s.TaskExecutionRefresh(ctx, TaskExecutionRefreshInput{
		ProjectID: "example",
		Key:       task.ID,
		Reason:    "corrupt chain must reject",
	}); err == nil {
		t.Fatal("corrupt refresh chain was accepted")
	}
	after, found, err := db.ReadTaskExecutionState(ctx, "example", task.ID)
	if err != nil || !found || after != stateBefore {
		t.Fatalf("corrupt-chain rejection mutated state: before=%#v after=%#v found=%v err=%v", stateBefore, after, found, err)
	}
	phasesAfter, err := db.ReadTaskExecutionPhases(ctx, "example", task.ID, "code")
	if err != nil || len(phasesAfter) != len(phasesBefore) {
		t.Fatalf("corrupt-chain rejection changed history: before=%#v after=%#v err=%v", phasesBefore, phasesAfter, err)
	}
}

func TestTSK631RefreshRejectsUnsafeStatesWithoutMutation(t *testing.T) {
	cases := []struct {
		name      string
		prepare   func(*testing.T, *Service, *sqlitestore.Databases, model.TaskAuthoring)
		unrelated bool
	}{
		{name: "dirty-lane", prepare: func(t *testing.T, s *Service, _ *sqlitestore.Databases, task model.TaskAuthoring) {
			path := tsk585LanePath(t, s, task.ID)
			if err := os.WriteFile(filepath.Join(path, "dirty.txt"), []byte("dirty\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "immutable-submission", prepare: func(t *testing.T, s *Service, _ *sqlitestore.Databases, task model.TaskAuthoring) {
			tsk585LaneCommit(t, s, task.ID, "immutable candidate")
			if _, err := s.TaskExecutionSubmitCode(context.Background(), "example", task.ID); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "in-flight-worker-turn", prepare: func(t *testing.T, s *Service, db *sqlitestore.Databases, task model.TaskAuthoring) {
			tsk625SeedAgentPrompt(t, s, db, task.ID, "accepted", strings.Repeat("e", 64))
		}},
		{name: "outcome-unknown-worker-turn", prepare: func(t *testing.T, s *Service, db *sqlitestore.Databases, task model.TaskAuthoring) {
			tsk625SeedAgentPrompt(t, s, db, task.ID, "outcome_unknown", strings.Repeat("f", 64))
		}},
		{name: "changed-task-revision", prepare: func(t *testing.T, _ *Service, db *sqlitestore.Databases, task model.TaskAuthoring) {
			shared, err := db.ReadSharedTask(context.Background(), task.ID)
			if err != nil {
				t.Fatal(err)
			}
			var changed model.TaskAuthoring
			if err := json.Unmarshal(shared.Payload, &changed); err != nil {
				t.Fatal(err)
			}
			changed.Title = "changed before refresh"
			changed.Revision++
			changed.RevisionSHA256, err = model.HashTaskAuthoring(changed)
			if err != nil {
				t.Fatal(err)
			}
			payload, err := json.Marshal(changed)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Shared.Exec(context.Background(), `UPDATE shared_tasks SET revision=?,payload=? WHERE id=?`, changed.Revision, payload, task.ID); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "diverged-canonical-main", unrelated: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, db := tsk585Setup(t)
			defer db.Close()
			ctx := context.Background()
			task := tsk585Task(t, s, "tsk631-reject-"+tc.name, "TSK631 rejection")
			tsk585Dispatch(t, s, task.ID)
			before, found, err := db.ReadTaskExecutionState(ctx, "example", task.ID)
			if err != nil || !found {
				t.Fatal(err)
			}
			if tc.prepare != nil {
				tc.prepare(t, s, db, task)
			}
			stateBeforeRefresh, found, err := db.ReadTaskExecutionState(ctx, "example", task.ID)
			if err != nil || !found {
				t.Fatal(err)
			}
			phasesBefore, err := db.ReadTaskExecutionPhases(ctx, "example", task.ID, stateBeforeRefresh.Stage)
			if err != nil {
				t.Fatal(err)
			}
			tsk631AdvanceCanonical(t, s, before.BaseHead, "TSK631 unsafe content", tc.unrelated)
			if _, err := s.TaskExecutionRefresh(ctx, TaskExecutionRefreshInput{
				ProjectID: "example",
				Key:       task.ID,
				Reason:    "unsafe refresh must reject",
			}); err == nil {
				t.Fatal("unsafe refresh was accepted")
			}
			after, found, err := db.ReadTaskExecutionState(ctx, "example", task.ID)
			if err != nil || !found || after != stateBeforeRefresh {
				t.Fatalf("rejected refresh mutated state: before=%#v after=%#v found=%v err=%v", stateBeforeRefresh, after, found, err)
			}
			phasesAfter, err := db.ReadTaskExecutionPhases(ctx, "example", task.ID, stateBeforeRefresh.Stage)
			if err != nil || !reflect.DeepEqual(phasesBefore, phasesAfter) {
				t.Fatalf("rejected refresh mutated history: before=%#v after=%#v err=%v", phasesBefore, phasesAfter, err)
			}
		})
	}
}
