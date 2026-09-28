package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/gitx"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func tsk670ResetFixture(t *testing.T, idem, title string) (*Service, *sqlitestore.Databases, model.TaskAuthoring, model.TaskExecutionState) {
	t.Helper()
	s, db := tsk585Setup(t)
	task := tsk585Task(t, s, idem, title)
	if _, err := s.TaskExecutionDispatch(context.Background(), TaskExecutionDispatchInput{
		ProjectID: task.ProjectID,
		Key:       task.ID,
	}); err != nil {
		db.Close()
		t.Fatal(err)
	}
	state, found, err := db.ReadTaskExecutionState(context.Background(), task.ProjectID, task.ID)
	if err != nil || !found {
		db.Close()
		t.Fatalf("dispatched Task execution found=%v err=%v", found, err)
	}
	return s, db, task, state
}

func tsk670AssertResetRejected(t *testing.T, s *Service, db *sqlitestore.Databases, task model.TaskAuthoring) {
	t.Helper()
	before, found, err := db.ReadTaskExecutionState(context.Background(), task.ProjectID, task.ID)
	if err != nil || !found {
		t.Fatalf("read pre-reset state found=%v err=%v", found, err)
	}
	if _, err := s.TaskExecutionReset(authority.WithPlanner(context.Background()), TaskExecutionResetInput{
		ProjectID: task.ProjectID,
		Key:       task.ID,
		Reason:    "unsafe reset test",
	}); err == nil {
		t.Fatal("unsafe Task reset succeeded")
	}
	after, found, err := db.ReadTaskExecutionState(context.Background(), task.ProjectID, task.ID)
	if err != nil || !found || after.Status != before.Status || after.ExecutionRevision != before.ExecutionRevision {
		t.Fatalf("rejected reset changed state: before=%#v after=%#v found=%v err=%v", before, after, found, err)
	}
}

func tsk670AppendUnsafePhase(t *testing.T, db *sqlitestore.Databases, state model.TaskExecutionState, stage, status, kind, decision string) {
	t.Helper()
	if err := db.AppendTaskExecutionPhase(context.Background(), sqlitestore.TaskExecutionPhase{
		TaskID:             state.TaskID,
		ProjectID:          state.ProjectID,
		ExecutionRevision:  state.ExecutionRevision + 1,
		Stage:              stage,
		Status:             status,
		Head:               state.Head,
		Branch:             state.Branch,
		TaskRevisionSHA256: state.TaskRevisionSHA256,
		EventKind:          kind,
		Decision:           decision,
		Comment:            "immutable lifecycle evidence",
		CreatedAt:          time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
}

func tsk670SeedMutationOperation(t *testing.T, s *Service, db *sqlitestore.Databases, kind string, input any, status string) string {
	t.Helper()
	ctx := context.Background()
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	digest := durableMutationDigest(kind, "", raw)
	now := time.Now().UTC()
	allocated, err := db.AllocateLocalOperation(ctx, "example", "EXM", digest, kind, now)
	if err != nil {
		t.Fatal(err)
	}
	operation := durableMutationOperation{
		SchemaVersion: durableMutationSchemaVersion,
		OperationID:   allocated.OperationID,
		MutationID:    digest,
		Kind:          kind,
		RequestSHA256: digest,
		ProjectID:     "example",
		Input:         raw,
		Status:        status,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if status == "outcome_unknown" {
		operation.Error = "seeded uncertain operation"
	}
	if err := s.writeDurableMutation(operation); err != nil {
		t.Fatal(err)
	}
	return operation.OperationID
}

func TestTSK670TaskExecutionResetThenUpdateAndRedispatch(t *testing.T) {
	s, db, task, state := tsk670ResetFixture(t, "tsk670-reset-happy", "Reset execution")
	defer db.Close()
	ctx := context.Background()
	planner := authority.WithPlanner(ctx)
	originalHistory, err := s.TaskLifecycleRead(ctx, task.ProjectID, task.ID, task.Revision)
	if err != nil {
		t.Fatal(err)
	}
	lifecycleBefore, err := db.ListTaskLifecycleEvents(ctx, task.ProjectID, task.ID, 32)
	if err != nil {
		t.Fatal(err)
	}
	oldBranch := state.Branch

	prematureTitle := "Premature update"
	if _, _, err := s.TaskLifecycleUpdate(planner, TaskAuthoringUpdateInput{
		ProjectID:              task.ProjectID,
		TaskID:                 task.ID,
		ExpectedRevision:       task.Revision,
		ExpectedRevisionSHA256: task.RevisionSHA256,
		Title:                  &prematureTitle,
		UpdatedBy:              "planner",
		Reason:                 "must remain blocked",
	}); err == nil {
		t.Fatal("Task authoring update succeeded while execution was nonterminal")
	}
	blocked, err := s.TaskExecutionBlock(ctx, TaskExecutionBlockInput{
		ProjectID: task.ProjectID,
		Key:       task.ID,
		Reason:    "awaiting Planner reset",
	})
	if err != nil || blocked.Status != model.TaskExecutionBlocked {
		t.Fatalf("block before reset=%#v err=%v", blocked, err)
	}
	found := false
	state, found, err = db.ReadTaskExecutionState(ctx, task.ProjectID, task.ID)
	if err != nil || !found || state.Status != model.TaskExecutionBlocked {
		t.Fatalf("blocked state=%#v found=%v err=%v", state, found, err)
	}

	reset, err := s.TaskExecutionReset(planner, TaskExecutionResetInput{
		ProjectID: task.ProjectID,
		Key:       task.ID,
		Reason:    "retire stale execution",
	})
	if err != nil || reset.Key != task.ID || reset.Status != model.TaskExecutionPlanned || reset.ExecutionRevision != state.ExecutionRevision+2 {
		t.Fatalf("reset=%#v err=%v state=%#v", reset, err, state)
	}
	status, err := s.TaskExecutionStatus(ctx, task.ProjectID, task.ID)
	if err != nil || status.Status != model.TaskExecutionPlanned || status.Key != task.ID {
		t.Fatalf("status after reset=%#v err=%v", status, err)
	}
	retired, found, err := db.ReadTaskExecutionState(ctx, task.ProjectID, task.ID)
	if err != nil || !found || retired.Status != model.TaskExecutionAbandoned || retired.ExecutionRevision != reset.ExecutionRevision {
		t.Fatalf("retired execution=%#v found=%v err=%v", retired, found, err)
	}
	lanePath, err := gitx.TaskWorktreePath(s.Config.StateDir, task.ProjectID, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(lanePath); !os.IsNotExist(err) {
		t.Fatalf("reset lane still exists: %s err=%v", lanePath, err)
	}
	project, err := s.EffectiveProjectConfig(task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if _, found, err := s.Git.ManagedBranchHead(ctx, project, oldBranch); err != nil || found {
		t.Fatalf("retired branch remains: found=%v err=%v", found, err)
	}
	phasesBeforeReset, err := db.ReadTaskExecutionPhases(ctx, task.ProjectID, task.ID, "code")
	if err != nil {
		t.Fatal(err)
	}
	if len(phasesBeforeReset) != 3 || phasesBeforeReset[0].EventKind != "block" || phasesBeforeReset[1].EventKind != "reset_start" || phasesBeforeReset[2].EventKind != "reset" {
		t.Fatalf("append-only retirement phase history=%#v", phasesBeforeReset)
	}
	originalAfterReset, err := s.TaskLifecycleRead(ctx, task.ProjectID, task.ID, task.Revision)
	if err != nil || !reflect.DeepEqual(originalAfterReset, originalHistory) {
		t.Fatalf("reset changed immutable Task revision: before=%#v after=%#v err=%v", originalHistory, originalAfterReset, err)
	}
	lifecycleAfterReset, err := db.ListTaskLifecycleEvents(ctx, task.ProjectID, task.ID, 32)
	if err != nil || !reflect.DeepEqual(lifecycleAfterReset, lifecycleBefore) {
		t.Fatalf("reset changed Shared Task lifecycle history: before=%#v after=%#v err=%v", lifecycleBefore, lifecycleAfterReset, err)
	}

	repeated, err := s.TaskExecutionReset(planner, TaskExecutionResetInput{
		ProjectID: task.ProjectID,
		Key:       task.ID,
		Reason:    "retire stale execution",
	})
	if err != nil || repeated != reset {
		t.Fatalf("idempotent reset=%#v first=%#v err=%v", repeated, reset, err)
	}
	if _, err := s.TaskExecutionReset(planner, TaskExecutionResetInput{
		ProjectID: task.ProjectID,
		Key:       task.ID,
		Reason:    "different reason",
	}); err == nil {
		t.Fatal("completed reset accepted a conflicting reason")
	}
	phasesAfterRepeat, err := db.ReadTaskExecutionPhases(ctx, task.ProjectID, task.ID, "code")
	if err != nil || !reflect.DeepEqual(phasesAfterRepeat, phasesBeforeReset) {
		t.Fatalf("idempotent reset duplicated or changed phase evidence: before=%#v after=%#v err=%v", phasesBeforeReset, phasesAfterRepeat, err)
	}

	updatedSummary := "Reset execution summary revised"
	updated, _, err := s.TaskLifecycleUpdate(planner, TaskAuthoringUpdateInput{
		ProjectID:              task.ProjectID,
		TaskID:                 task.ID,
		ExpectedRevision:       task.Revision,
		ExpectedRevisionSHA256: task.RevisionSHA256,
		Summary:                &updatedSummary,
		UpdatedBy:              "planner",
		Reason:                 "revise after reset",
	})
	if err != nil || updated.ID != task.ID || updated.Revision != task.Revision+1 || updated.RevisionSHA256 == task.RevisionSHA256 || updated.Title != task.Title || updated.Summary != updatedSummary {
		t.Fatalf("Task update after reset=%#v err=%v", updated, err)
	}
	historicalAfterUpdate, err := s.TaskLifecycleRead(ctx, task.ProjectID, task.ID, task.Revision)
	if err != nil || !reflect.DeepEqual(historicalAfterUpdate, originalHistory) {
		t.Fatalf("immutable Task history changed after update: before=%#v after=%#v err=%v", originalHistory, historicalAfterUpdate, err)
	}
	redispatched, err := s.TaskExecutionDispatch(planner, TaskExecutionDispatchInput{
		ProjectID: task.ProjectID,
		Key:       task.ID,
	})
	if err != nil || redispatched.Status != model.TaskExecutionDispatched || redispatched.ExecutionRevision != reset.ExecutionRevision+1 {
		t.Fatalf("redispatch after reset=%#v err=%v", redispatched, err)
	}
	current, found, err := db.ReadTaskExecutionState(ctx, task.ProjectID, task.ID)
	if err != nil || !found || current.ExecutionRevision != redispatched.ExecutionRevision || current.TaskRevision != updated.Revision || current.Status != model.TaskExecutionDispatched {
		t.Fatalf("new execution revision=%#v found=%v err=%v", current, found, err)
	}
	lane := s.Config.Projects[task.ProjectID]
	lane.Root = lanePath
	head, branch, clean, err := s.Git.CurrentHead(ctx, lane)
	if err != nil || !clean || head != current.BaseHead || head != current.Head || branch != current.Branch || current.Branch == oldBranch {
		t.Fatalf("redispatched lane is not fresh and canonical: head=%s branch=%s clean=%v state=%#v err=%v", head, branch, clean, current, err)
	}
	if _, found, err := s.Git.ManagedBranchHead(ctx, project, oldBranch); err != nil || found {
		t.Fatalf("old execution branch was reused: found=%v err=%v", found, err)
	}
}

func TestTSK670TaskExecutionResetRequiresPlannerAndValidInput(t *testing.T) {
	s, db, task, before := tsk670ResetFixture(t, "tsk670-reset-authority", "Reset authority")
	defer db.Close()
	input := TaskExecutionResetInput{
		ProjectID: task.ProjectID,
		Key:       task.ID,
		Reason:    "planner reset",
	}
	for name, ctx := range map[string]context.Context{"missing role": context.Background(), "lead role": authority.WithLead(context.Background())} {
		t.Run(name, func(t *testing.T) {
			if _, err := s.TaskExecutionReset(ctx, input); err == nil {
				t.Fatalf("%s was authorized to reset", name)
			}
		})
	}
	for name, invalid := range map[string]TaskExecutionResetInput{
		"wrong project":    {ProjectID: "other", Key: task.ID, Reason: "planner reset"},
		"empty reason":     {ProjectID: task.ProjectID, Key: task.ID, Reason: "  "},
		"nul reason":       {ProjectID: task.ProjectID, Key: task.ID, Reason: "invalid\x00reason"},
		"oversized reason": {ProjectID: task.ProjectID, Key: task.ID, Reason: strings.Repeat("x", taskExecutionBlockReasonLimit+1)},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := s.TaskExecutionReset(authority.WithPlanner(context.Background()), invalid); err == nil {
				t.Fatalf("invalid reset input %q was accepted", name)
			}
		})
	}
	after, found, err := db.ReadTaskExecutionState(context.Background(), task.ProjectID, task.ID)
	if err != nil || !found || after.Status != before.Status || after.ExecutionRevision != before.ExecutionRevision {
		t.Fatalf("unauthorized/invalid reset changed state: before=%#v after=%#v found=%v err=%v", before, after, found, err)
	}
}

func TestTSK670TaskExecutionResetFailsClosedOnUnsafeEvidence(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(*testing.T, *Service, *sqlitestore.Databases, model.TaskAuthoring, model.TaskExecutionState)
	}{
		{name: "busy Worker", prepare: func(t *testing.T, s *Service, _ *sqlitestore.Databases, task model.TaskAuthoring, _ model.TaskExecutionState) {
			installServiceExecutionSessionFixtureState(t, s, filepath.Join(t.TempDir(), "prompts.log"), "busy")
			worker, err := s.ResolveProjectWorker(context.Background(), task.ProjectID)
			if err != nil || strings.EqualFold(worker.RuntimeState, "idle") {
				t.Fatalf("busy Worker fixture state=%#v err=%v", worker, err)
			}
		}},
		{name: "accepted Agent prompt", prepare: func(t *testing.T, s *Service, _ *sqlitestore.Databases, task model.TaskAuthoring, state model.TaskExecutionState) {
			seedAgentPromptTurn(t, s, AgentPromptInput{
				ProjectID: task.ProjectID,
				AgentID:   state.Agent,
				Message:   "continue current task",
			}, "accepted")
		}},
		{name: "unknown Agent interrupt", prepare: func(t *testing.T, s *Service, db *sqlitestore.Databases, task model.TaskAuthoring, state model.TaskExecutionState) {
			tsk670SeedMutationOperation(t, s, db, "agent-interrupt", AgentInterruptInput{
				ProjectID: task.ProjectID,
				AgentID:   state.Agent,
			}, "outcome_unknown")
		}},
		{name: "accepted Task submission operation", prepare: func(t *testing.T, s *Service, db *sqlitestore.Databases, task model.TaskAuthoring, state model.TaskExecutionState) {
			tsk670SeedMutationOperation(t, s, db, taskExecutionSubmitKind, taskExecutionSubmitInput{
				Stage:             "code",
				TaskID:            task.ID,
				ExecutionRevision: state.ExecutionRevision,
			}, "accepted")
		}},
		{name: "accepted Task verification operation", prepare: func(t *testing.T, s *Service, db *sqlitestore.Databases, task model.TaskAuthoring, _ model.TaskExecutionState) {
			tsk670SeedMutationOperation(t, s, db, "task-execution-test", TaskExecutionTestInput{
				ProjectID: task.ProjectID,
				Key:       task.ID,
			}, "accepted")
		}},
		{name: "unknown Task integration operation", prepare: func(t *testing.T, s *Service, db *sqlitestore.Databases, task model.TaskAuthoring, _ model.TaskExecutionState) {
			tsk670SeedMutationOperation(t, s, db, "task-execution-integrate", TaskExecutionIntegrateInput{
				ProjectID: task.ProjectID,
				Key:       task.ID,
			}, "outcome_unknown")
		}},
		{name: "immutable submission", prepare: func(t *testing.T, _ *Service, db *sqlitestore.Databases, task model.TaskAuthoring, state model.TaskExecutionState) {
			tsk670AppendUnsafePhase(t, db, state, "code", model.TaskExecutionAwaitingReview, "submission", "")
		}},
		{name: "accepted immutable review", prepare: func(t *testing.T, _ *Service, db *sqlitestore.Databases, task model.TaskAuthoring, state model.TaskExecutionState) {
			tsk670AppendUnsafePhase(t, db, state, "code", model.TaskExecutionReadyForVerification, "review", "accept")
		}},
		{name: "integration evidence", prepare: func(t *testing.T, _ *Service, db *sqlitestore.Databases, task model.TaskAuthoring, state model.TaskExecutionState) {
			tsk670AppendUnsafePhase(t, db, state, "integration", model.TaskExecutionIntegrated, "integration", "accept")
		}},
		{name: "dirty lane", prepare: func(t *testing.T, s *Service, _ *sqlitestore.Databases, task model.TaskAuthoring, _ model.TaskExecutionState) {
			tsk585LaneWrite(t, s, task.ID, "reset-dirty.txt", "uncommitted")
		}},
		{name: "candidate head", prepare: func(t *testing.T, s *Service, _ *sqlitestore.Databases, task model.TaskAuthoring, _ model.TaskExecutionState) {
			tsk585LaneCommit(t, s, task.ID, "unsubmitted candidate")
		}},
		{name: "rebase conflict state", prepare: func(t *testing.T, s *Service, _ *sqlitestore.Databases, task model.TaskAuthoring, _ model.TaskExecutionState) {
			lanePath := tsk585LanePath(t, s, task.ID)
			rebasePath := strings.TrimSpace(testutil.Git(t, lanePath, "rev-parse", "--git-path", "rebase-merge"))
			if !filepath.IsAbs(rebasePath) {
				rebasePath = filepath.Join(lanePath, rebasePath)
			}
			if err := os.MkdirAll(rebasePath, 0o700); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "missing canonical lane", prepare: func(t *testing.T, s *Service, _ *sqlitestore.Databases, task model.TaskAuthoring, _ model.TaskExecutionState) {
			if err := os.RemoveAll(tsk585LanePath(t, s, task.ID)); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "symlinked canonical lane", prepare: func(t *testing.T, s *Service, _ *sqlitestore.Databases, task model.TaskAuthoring, _ model.TaskExecutionState) {
			lanePath := tsk585LanePath(t, s, task.ID)
			if err := os.RemoveAll(lanePath); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(t.TempDir(), "outside")
			if err := os.MkdirAll(target, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, lanePath); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "cleanup branch mismatch", prepare: func(t *testing.T, _ *Service, db *sqlitestore.Databases, task model.TaskAuthoring, state model.TaskExecutionState) {
			state.Branch = "task/" + task.ID + "-wrong"
			state.ExecutionRevision++
			state.UpdatedAt = time.Now().UTC()
			if err := db.UpdateTaskExecutionState(context.Background(), state, state.ExecutionRevision-1); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "missing blocked phase history", prepare: func(t *testing.T, s *Service, db *sqlitestore.Databases, task model.TaskAuthoring, _ model.TaskExecutionState) {
			if _, err := s.TaskExecutionBlock(context.Background(), TaskExecutionBlockInput{
				ProjectID: task.ProjectID,
				Key:       task.ID,
				Reason:    "block before corrupt history",
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Local.Exec(context.Background(), `DELETE FROM local_task_execution_phases WHERE project_id=? AND task_id=? AND event_kind='block'`, task.ProjectID, task.ID); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "unsupported execution stage", prepare: func(t *testing.T, _ *Service, db *sqlitestore.Databases, task model.TaskAuthoring, state model.TaskExecutionState) {
			state.Stage = "tests"
			state.Status = model.TaskExecutionVerified
			state.ExecutionRevision++
			state.UpdatedAt = time.Now().UTC()
			if err := db.UpdateTaskExecutionState(context.Background(), state, state.ExecutionRevision-1); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			s, db, task, state := tsk670ResetFixture(t, "tsk670-reset-guard-"+strings.ToLower(strings.ReplaceAll(test.name, " ", "-")), "Reset guard "+test.name)
			defer db.Close()
			test.prepare(t, s, db, task, state)
			tsk670AssertResetRejected(t, s, db, task)
		})
	}
}

func tsk670SeedVerificationEvidence(t *testing.T, s *Service, db *sqlitestore.Databases, task model.TaskAuthoring) {
	t.Helper()
	ctx := context.Background()
	state, found, err := db.ReadTaskExecutionState(ctx, task.ProjectID, task.ID)
	if err != nil || !found {
		t.Fatalf("verification seed state found=%v err=%v", found, err)
	}
	state.Stage = "tests"
	state.Status = model.TaskExecutionVerifying
	state.ExecutionRevision++
	state.UpdatedAt = time.Now().UTC()
	if err := db.UpdateTaskExecutionState(ctx, state, state.ExecutionRevision-1); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	receipt := model.TaskExecutionVerification{
		ProjectID: task.ProjectID, TaskID: task.ID, OperationID: "EXM-OPR1", TaskRevisionSHA256: state.TaskRevisionSHA256,
		BaseHead: state.BaseHead, CandidateHead: state.Head, CandidateTree: state.Head, Branch: state.Branch,
		GateProfileSHA256: strings.Repeat("a", 64), Outcome: model.TaskExecutionVerificationSucceeded,
		TaskRevision: state.TaskRevision, AttemptRevision: state.ExecutionRevision, CodeReviewID: 1,
		Gates: []model.CompletionGateResult{{
			ID: "unit", Execution: "executed", ExitCode: 0, TreeID: state.Head,
			ContractDigest: strings.Repeat("a", 64), ReceiptDigest: strings.Repeat("b", 64), DurationMS: 1,
		}}, StartedAt: now, CompletedAt: now.Add(time.Second),
	}
	next := state
	next.Status = model.TaskExecutionVerified
	next.ExecutionRevision++
	next.UpdatedAt = now.Add(time.Second)
	if err := db.FinishTaskExecutionVerification(ctx, next, state.ExecutionRevision, receipt); err != nil {
		t.Fatal(err)
	}
	next.Status = model.TaskExecutionDispatched
	next.Stage = "code"
	next.ExecutionRevision++
	next.UpdatedAt = now.Add(2 * time.Second)
	if err := db.UpdateTaskExecutionState(ctx, next, next.ExecutionRevision-1); err != nil {
		t.Fatal(err)
	}
	_ = s
}

func TestTSK670TaskExecutionResetRejectsVerificationEvidence(t *testing.T) {
	s, db, task, _ := tsk670ResetFixture(t, "tsk670-reset-verification", "Reset verification evidence")
	defer db.Close()
	tsk670SeedVerificationEvidence(t, s, db, task)
	tsk670AssertResetRejected(t, s, db, task)
}

func TestTSK670TaskExecutionResetRejectsCompletionEvidence(t *testing.T) {
	s, db := tsk585Setup(t)
	defer db.Close()
	ctx := context.Background()
	task := tsk585CompleteTask(t, s, "tsk670-reset-completion", "Reset completion evidence", "completion criterion")
	sessionID := tsk585PlannerSession(t, s)
	review := tsk585CompleteEvidence(t, s, task, &sessionID, model.OperatorTaskReview, []string{tsk585ReviewRationale(t, task, 1, "non_code", "", "non_code")}, nil)
	if _, err := s.TaskComplete(ctx, tsk585CompletionInput(task, "non_code", "completion is immutable", review.ID), "planner"); err != nil {
		t.Fatal(err)
	}
	if _, found, err := db.ReadTaskCompletionEvent(ctx, task.ProjectID, task.ID); err != nil || !found {
		t.Fatalf("completion evidence found=%v err=%v", found, err)
	}
	base, _, _, err := s.Git.CurrentHead(ctx, s.Config.Projects[task.ProjectID])
	if err != nil {
		t.Fatal(err)
	}
	state := model.TaskExecutionState{
		TaskID: task.ID, ProjectID: task.ProjectID, TaskRevision: task.Revision, TaskRevisionSHA256: task.RevisionSHA256,
		Status: model.TaskExecutionDispatched, Stage: "code", Worktree: taskExecutionWorktree(task.ID, base[:8]),
		BaseHead: base, Head: base, Branch: "task/" + task.ID + "-completion", Agent: "coder-example",
		ExecutionRevision: 1, UpdatedAt: time.Now().UTC(),
	}
	if err := db.CreateTaskExecutionState(ctx, state); err != nil {
		t.Fatal(err)
	}
	evidence := taskExecutionResetEvidenceFromState(state, "must reject completion")
	if err := s.ensureTaskExecutionResetSafe(ctx, state, evidence, false); err == nil || !strings.Contains(err.Error(), "completion evidence") {
		t.Fatalf("completion evidence was not the reset guard: %v", err)
	}
	if _, err := s.TaskExecutionReset(authority.WithPlanner(ctx), TaskExecutionResetInput{
		ProjectID: task.ProjectID,
		Key:       task.ID,
		Reason:    "must reject completion",
	}); err == nil {
		t.Fatal("task/reset accepted immutable completion evidence")
	}
	current, found, err := db.ReadTaskExecutionState(ctx, task.ProjectID, task.ID)
	if err != nil || !found || current != state {
		t.Fatalf("rejected completion reset changed execution state: state=%#v found=%v err=%v", current, found, err)
	}
}

func TestTSK670TaskExecutionResetPersistenceFailuresRemainFailClosed(t *testing.T) {
	t.Run("start transaction failure leaves execution and lane untouched", func(t *testing.T) {
		s, db, task, before := tsk670ResetFixture(t, "tsk670-reset-start-failure", "Reset start failure")
		defer db.Close()
		if _, err := db.Local.Exec(context.Background(), `CREATE TRIGGER tsk670_reject_reset_start BEFORE INSERT ON local_task_execution_phases WHEN NEW.event_kind='reset_start' BEGIN SELECT RAISE(ABORT,'injected reset start failure'); END`); err != nil {
			t.Fatal(err)
		}
		if _, err := s.TaskExecutionReset(authority.WithPlanner(context.Background()), TaskExecutionResetInput{
			ProjectID: task.ProjectID,
			Key:       task.ID,
			Reason:    "start persistence failure",
		}); err == nil {
			t.Fatal("reset succeeded despite failed start transaction")
		}
		after, found, err := db.ReadTaskExecutionState(context.Background(), task.ProjectID, task.ID)
		if err != nil || !found || after.Status != before.Status || after.ExecutionRevision != before.ExecutionRevision {
			t.Fatalf("failed start transaction changed state: before=%#v after=%#v found=%v err=%v", before, after, found, err)
		}
		if _, err := os.Lstat(tsk585LanePath(t, s, task.ID)); err != nil {
			t.Fatalf("failed start transaction cleaned the lane: %v", err)
		}
	})
	t.Run("terminal transaction failure leaves a recoverable resetting state", func(t *testing.T) {
		s, db, task, _ := tsk670ResetFixture(t, "tsk670-reset-final-failure", "Reset final failure")
		defer db.Close()
		if _, err := db.Local.Exec(context.Background(), `CREATE TRIGGER tsk670_reject_reset_terminal BEFORE INSERT ON local_task_execution_phases WHEN NEW.event_kind='reset' BEGIN SELECT RAISE(ABORT,'injected reset terminal failure'); END`); err != nil {
			t.Fatal(err)
		}
		if _, err := s.TaskExecutionReset(authority.WithPlanner(context.Background()), TaskExecutionResetInput{
			ProjectID: task.ProjectID,
			Key:       task.ID,
			Reason:    "terminal persistence failure",
		}); err == nil {
			t.Fatal("reset succeeded despite failed terminal transaction")
		}
		state, found, err := db.ReadTaskExecutionState(context.Background(), task.ProjectID, task.ID)
		if err != nil || !found || state.Status != model.TaskExecutionResetting {
			t.Fatalf("failed terminal transaction state=%#v found=%v err=%v", state, found, err)
		}
		status, err := s.TaskExecutionStatus(context.Background(), task.ProjectID, task.ID)
		if err != nil || status.Status != model.TaskExecutionResetting {
			t.Fatalf("pending reset status=%#v err=%v", status, err)
		}
		if _, _, err := s.TaskLifecycleUpdate(authority.WithPlanner(context.Background()), TaskAuthoringUpdateInput{
			ProjectID:        task.ProjectID,
			TaskID:           task.ID,
			ExpectedRevision: task.Revision,
			Title:            planString("Unsafe update"),
			UpdatedBy:        "planner",
			Reason:           "must remain blocked",
		}); err == nil {
			t.Fatal("Task authoring update succeeded while reset was incomplete")
		}
		if _, err := os.Lstat(tsk585LanePath(t, s, task.ID)); !os.IsNotExist(err) {
			t.Fatalf("terminal failure did not finish physical cleanup before durable retry: %v", err)
		}
		if _, err := db.Local.Exec(context.Background(), `DROP TRIGGER tsk670_reject_reset_terminal`); err != nil {
			t.Fatal(err)
		}
		recovered, err := s.TaskExecutionReset(authority.WithPlanner(context.Background()), TaskExecutionResetInput{
			ProjectID: task.ProjectID,
			Key:       task.ID,
			Reason:    "terminal persistence failure",
		})
		if err != nil || recovered.Status != model.TaskExecutionPlanned || recovered.ExecutionRevision != state.ExecutionRevision+1 {
			t.Fatalf("reset did not recover idempotently after the terminal store recovered: result=%#v err=%v", recovered, err)
		}
	})
}

func TestTSK670TaskExecutionResetRejectsMissingExecution(t *testing.T) {
	s, db := tsk585Setup(t)
	defer db.Close()
	task := tsk585Task(t, s, "tsk670-reset-no-execution", "Reset without execution")
	planner := authority.WithPlanner(context.Background())
	input := TaskExecutionResetInput{
		ProjectID: task.ProjectID,
		Key:       task.ID,
		Reason:    "already planned",
	}
	if _, err := s.TaskExecutionReset(planner, input); err == nil {
		t.Fatal("task/reset accepted a Task without an execution")
	}
	status, err := s.TaskExecutionStatus(context.Background(), task.ProjectID, task.ID)
	if err != nil || status.Status != model.TaskExecutionPlanned {
		t.Fatalf("Task status without execution=%#v err=%v", status, err)
	}
	if _, found, err := db.ReadTaskExecutionState(context.Background(), task.ProjectID, task.ID); err != nil || found {
		t.Fatalf("rejected reset created an execution state: found=%v err=%v", found, err)
	}
}
