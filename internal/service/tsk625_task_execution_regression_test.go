package service

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

func tsk625SeedAgentPrompt(t *testing.T, s *Service, db *sqlitestore.Databases, taskID, status, mutationID string) {
	t.Helper()
	ctx := context.Background()
	state, found, err := db.ReadTaskExecutionState(ctx, "example", taskID)
	if err != nil || !found {
		t.Fatalf("read Task state found=%v err=%v", found, err)
	}
	input, err := json.Marshal(AgentPromptInput{
		ProjectID: "example",
		AgentID:   state.Agent,
		Message:   "TSK625 Worker turn",
	})
	if err != nil {
		t.Fatal(err)
	}
	sessionID := tsk622AssertWorkerSessionCount(t, db)
	now := time.Now().UTC()
	allocated, err := db.AllocateLocalOperation(ctx, "example", "EXM", mutationID, "agent-prompt", now)
	if err != nil {
		t.Fatal(err)
	}
	operation := durableMutationOperation{
		SchemaVersion: durableMutationSchemaVersion,
		OperationID:   allocated.OperationID,
		MutationID:    allocated.MutationID,
		Kind:          "agent-prompt",
		RequestSHA256: allocated.MutationID,
		SessionID:     sessionID,
		ProjectID:     "example",
		Input:         input,
		Status:        status,
		Error:         "seeded TSK625 test operation",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := s.writeDurableMutation(operation); err != nil {
		t.Fatal(err)
	}
}

func tsk625RemoveAdmissionColumns(t *testing.T, db *sqlitestore.Databases) {
	t.Helper()
	ctx := context.Background()
	if _, err := db.Local.Exec(ctx, `DROP INDEX IF EXISTS local_operations_admission_idx`); err != nil {
		t.Fatal(err)
	}
	for _, column := range []string{"admission_session_id", "admission_input_sha256"} {
		if _, err := db.Local.Exec(ctx, `ALTER TABLE local_operations DROP COLUMN `+column); err != nil {
			t.Fatalf("drop legacy-schema column %s: %v", column, err)
		}
	}
}

func TestTSK625BlockResumePreservesDurableWorkerLane(t *testing.T) {
	s, db := tsk585Setup(t)
	defer db.Close()
	ctx := context.Background()
	task := tsk585Task(t, s, "tsk625-block-resume", "TSK625 block and resume")
	tsk585Dispatch(t, s, task.ID)
	before, found, err := db.ReadTaskExecutionState(ctx, "example", task.ID)
	if err != nil || !found {
		t.Fatalf("read dispatched state found=%v err=%v", found, err)
	}
	workerSession := tsk622AssertWorkerSessionCount(t, db)
	workerCtx := WithAgentSessionID(ctx, workerSession)

	blocked, err := s.TaskExecutionBlock(ctx, TaskExecutionBlockInput{
		ProjectID: "example",
		Key:       task.ID,
		Reason:    "awaiting owner decision",
	})
	if err != nil {
		t.Fatalf("block Task: %v", err)
	}
	if blocked.Status != model.TaskExecutionBlocked || blocked.Reason != "awaiting owner decision" || blocked.ExecutionRevision != before.ExecutionRevision+1 {
		t.Fatalf("blocked output=%#v", blocked)
	}
	parked, found, err := db.ReadTaskExecutionState(ctx, "example", task.ID)
	if err != nil || !found {
		t.Fatalf("read blocked state found=%v err=%v", found, err)
	}
	if parked.Status != model.TaskExecutionBlocked || parked.Stage != before.Stage || parked.Worktree != before.Worktree || parked.BaseHead != before.BaseHead || parked.Head != before.Head || parked.Branch != before.Branch || parked.Agent != before.Agent || parked.ExecutionRevision != before.ExecutionRevision+1 {
		t.Fatalf("block changed lane authority: before=%#v parked=%#v", before, parked)
	}
	phases, err := db.ReadTaskExecutionPhases(ctx, "example", task.ID, before.Stage)
	if err != nil || len(phases) != 1 || phases[0].EventKind != "block" || phases[0].Status != model.TaskExecutionBlocked || phases[0].Decision != before.Status || phases[0].Comment != "awaiting owner decision" {
		t.Fatalf("block phase=%#v err=%v", phases, err)
	}
	if _, err := s.TaskExecutionCurrent(workerCtx, "example"); err == nil {
		t.Fatal("blocked Task remained Worker-current")
	}

	restarted := NewWithDurabilityDeferredWorkers(s.Config, db)
	status, err := restarted.TaskExecutionStatus(ctx, "example", task.ID)
	if err != nil || status.Status != model.TaskExecutionBlocked || status.Reason != "awaiting owner decision" {
		t.Fatalf("restart status=%#v err=%v", status, err)
	}

	again, err := restarted.TaskExecutionBlock(ctx, TaskExecutionBlockInput{
		ProjectID: "example",
		Key:       task.ID,
		Reason:    "awaiting owner decision",
	})
	if err != nil || again.ExecutionRevision != parked.ExecutionRevision {
		t.Fatalf("idempotent block=%#v err=%v", again, err)
	}
	if phases, err = db.ReadTaskExecutionPhases(ctx, "example", task.ID, before.Stage); err != nil || len(phases) != 1 {
		t.Fatalf("idempotent block changed phase history=%#v err=%v", phases, err)
	}
	if _, err := restarted.TaskExecutionBlock(ctx, TaskExecutionBlockInput{
		ProjectID: "example",
		Key:       task.ID,
		Reason:    "different reason",
	}); err == nil {
		t.Fatal("conflicting block reason was accepted")
	}

	resumed, err := restarted.TaskExecutionResume(ctx, TaskExecutionResumeInput{
		ProjectID: "example",
		Key:       task.ID,
		Reason:    "owner decision recorded",
	})
	if err != nil {
		t.Fatalf("resume Task: %v", err)
	}
	if resumed.Status != before.Status || resumed.Stage != before.Stage || resumed.Worktree != before.Worktree || resumed.Head != before.Head[:8] || resumed.Agent != before.Agent || resumed.ExecutionRevision != parked.ExecutionRevision+1 || resumed.Reason != "owner decision recorded" {
		t.Fatalf("resumed output=%#v before=%#v parked=%#v", resumed, before, parked)
	}
	resumedState, found, err := db.ReadTaskExecutionState(ctx, "example", task.ID)
	if err != nil || !found || resumedState.Status != before.Status || resumedState.Worktree != before.Worktree || resumedState.Head != before.Head || resumedState.Branch != before.Branch || resumedState.Agent != before.Agent {
		t.Fatalf("resumed state=%#v found=%v err=%v", resumedState, found, err)
	}
	phases, err = db.ReadTaskExecutionPhases(ctx, "example", task.ID, before.Stage)
	if err != nil || len(phases) != 2 || phases[1].EventKind != "resume" || phases[1].Status != before.Status || phases[1].Decision != before.Status || phases[1].Comment != "owner decision recorded" {
		t.Fatalf("resume phases=%#v err=%v", phases, err)
	}
	resumedAgain, err := restarted.TaskExecutionResume(ctx, TaskExecutionResumeInput{
		ProjectID: "example",
		Key:       task.ID,
		Reason:    "owner decision recorded",
	})
	if err != nil || resumedAgain.ExecutionRevision != resumedState.ExecutionRevision || resumedAgain.Reason != "owner decision recorded" {
		t.Fatalf("idempotent resume=%#v err=%v", resumedAgain, err)
	}
	if phases, err = db.ReadTaskExecutionPhases(ctx, "example", task.ID, before.Stage); err != nil || len(phases) != 2 {
		t.Fatalf("idempotent resume changed phase history=%#v err=%v", phases, err)
	}
}

func TestTSK625BlockedStatusFailsClosedWithoutDurableBlockEvidence(t *testing.T) {
	for _, tc := range []struct {
		name  string
		query string
	}{
		{name: "missing", query: `DELETE FROM shared_task_execution_phases WHERE project_id=? AND task_id=? AND event_kind='block'`},
		{name: "inconsistent", query: `UPDATE shared_task_execution_phases SET status=? WHERE project_id=? AND task_id=? AND event_kind='block'`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, db := tsk585Setup(t)
			defer db.Close()
			ctx := context.Background()
			task := tsk585Task(t, s, "tsk625-evidence-"+tc.name, "TSK625 block evidence")
			tsk585Dispatch(t, s, task.ID)
			if _, err := s.TaskExecutionBlock(ctx, TaskExecutionBlockInput{
				ProjectID: "example",
				Key:       task.ID,
				Reason:    "evidence check",
			}); err != nil {
				t.Fatal(err)
			}
			var err error
			if tc.name == "missing" {
				_, err = db.Shared.Exec(ctx, tc.query, "example", task.ID)
			} else {
				_, err = db.Shared.Exec(ctx, tc.query, model.TaskExecutionDispatched, "example", task.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.TaskExecutionStatus(ctx, "example", task.ID); err == nil {
				t.Fatal("blocked status accepted missing or inconsistent block evidence")
			}
		})
	}
}

func TestTSK625ResumeFailsClosedWhenAnotherWorkerTaskIsActionable(t *testing.T) {
	s, db := tsk585Setup(t)
	defer db.Close()
	ctx := context.Background()
	first := tsk585Task(t, s, "tsk625-resume-conflict-a", "TSK625 first lane")
	second := tsk585Task(t, s, "tsk625-resume-conflict-b", "TSK625 second lane")
	tsk585Dispatch(t, s, first.ID)
	if _, err := s.TaskExecutionBlock(ctx, TaskExecutionBlockInput{
		ProjectID: "example",
		Key:       first.ID,
		Reason:    "pause before review",
	}); err != nil {
		t.Fatal(err)
	}
	tsk585Dispatch(t, s, second.ID)
	before, _, _ := db.ReadTaskExecutionState(ctx, "example", first.ID)
	if _, err := s.TaskExecutionResume(ctx, TaskExecutionResumeInput{
		ProjectID: "example",
		Key:       first.ID,
		Reason:    "resume now",
	}); err == nil {
		t.Fatal("resume ignored another actionable Worker Task")
	}
	after, _, _ := db.ReadTaskExecutionState(ctx, "example", first.ID)
	if after != before {
		t.Fatalf("failed resume changed state: before=%#v after=%#v", before, after)
	}
	tsk622SetExecutionStatus(t, db, second.ID, model.TaskExecutionAwaitingReview, "code")
	if _, err := s.TaskExecutionResume(ctx, TaskExecutionResumeInput{
		ProjectID: "example",
		Key:       first.ID,
		Reason:    "resume now",
	}); err != nil {
		t.Fatalf("resume after actionable lane release: %v", err)
	}
}

func TestTSK625BlockFailsClosedForWorkerTurnStates(t *testing.T) {
	for _, status := range []string{"accepted", "running", "outcome_unknown"} {
		t.Run(status, func(t *testing.T) {
			s, db := tsk585Setup(t)
			defer db.Close()
			ctx := context.Background()
			task := tsk585Task(t, s, "tsk625-turn-"+strings.ReplaceAll(status, "_", "-"), "TSK625 Worker turn")
			tsk585Dispatch(t, s, task.ID)
			state, _, _ := db.ReadTaskExecutionState(ctx, "example", task.ID)
			sessionID := tsk622AssertWorkerSessionCount(t, db)
			input, err := json.Marshal(AgentPromptInput{
				ProjectID: "example",
				AgentID:   state.Agent,
				Message:   "Worker turn",
			})
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			allocated, err := db.AllocateLocalOperation(ctx, "example", "EXM", strings.Repeat("a", 64), "agent-prompt", now)
			if err != nil {
				t.Fatal(err)
			}
			durable := durableMutationOperation{
				SchemaVersion: durableMutationSchemaVersion,
				OperationID:   allocated.OperationID,
				MutationID:    allocated.MutationID,
				Kind:          "agent-prompt",
				RequestSHA256: allocated.MutationID,
				SessionID:     sessionID,
				ProjectID:     "example",
				Input:         input,
				Status:        status,
				CreatedAt:     now,
				UpdatedAt:     now,
			}
			if err := s.writeDurableMutation(durable); err != nil {
				t.Fatal(err)
			}
			if _, err := s.TaskExecutionBlock(ctx, TaskExecutionBlockInput{
				ProjectID: "example",
				Key:       task.ID,
				Reason:    "safe boundary",
			}); err == nil {
				t.Fatalf("block accepted Worker turn status %q", status)
			}
			after, _, _ := db.ReadTaskExecutionState(ctx, "example", task.ID)
			if after != state {
				t.Fatalf("failed block changed state: before=%#v after=%#v", state, after)
			}
		})
	}
}

func TestTSK625LegacyLocalOperationsSchemaTurnGuard(t *testing.T) {
	for _, status := range []string{"failed", "accepted", "running", "outcome_unknown"} {
		t.Run(status, func(t *testing.T) {
			s, db := tsk585Setup(t)
			defer db.Close()
			ctx := context.Background()
			task := tsk585Task(t, s, "tsk625-legacy-"+strings.ReplaceAll(status, "_", "-"), "TSK625 legacy Local schema")
			tsk585Dispatch(t, s, task.ID)
			before, found, err := db.ReadTaskExecutionState(ctx, "example", task.ID)
			if err != nil || !found {
				t.Fatalf("read Task state found=%v err=%v", found, err)
			}
			tsk625SeedAgentPrompt(t, s, db, task.ID, status, strings.Repeat("c", 64))
			tsk625RemoveAdmissionColumns(t, db)
			blocked, blockErr := s.TaskExecutionBlock(ctx, TaskExecutionBlockInput{
				ProjectID: "example",
				Key:       task.ID,
				Reason:    "legacy schema boundary",
			})
			if status == "failed" {
				if blockErr != nil || blocked.Status != model.TaskExecutionBlocked {
					t.Fatalf("failed prompt was not blockable on legacy schema: output=%#v err=%v", blocked, blockErr)
				}
				return
			}
			if blockErr == nil {
				t.Fatalf("Worker turn status %q was accepted on legacy schema", status)
			}
			after, found, readErr := db.ReadTaskExecutionState(ctx, "example", task.ID)
			if readErr != nil || !found || after != before {
				t.Fatalf("rejected legacy block mutated state: before=%#v after=%#v found=%v err=%v", before, after, found, readErr)
			}
		})
	}
}

func TestTSK625FailedWorkerPromptDoesNotPreventBlock(t *testing.T) {
	s, db := tsk585Setup(t)
	defer db.Close()
	ctx := context.Background()
	task := tsk585Task(t, s, "tsk625-failed-prompt", "TSK625 failed prompt")
	tsk585Dispatch(t, s, task.ID)
	state, _, _ := db.ReadTaskExecutionState(ctx, "example", task.ID)
	sessionID := tsk622AssertWorkerSessionCount(t, db)
	input, err := json.Marshal(AgentPromptInput{
		ProjectID: "example",
		AgentID:   state.Agent,
		Message:   "failed before admission",
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	allocated, err := db.AllocateLocalOperation(ctx, "example", "EXM", strings.Repeat("b", 64), "agent-prompt", now)
	if err != nil {
		t.Fatal(err)
	}
	failed := durableMutationOperation{
		SchemaVersion: durableMutationSchemaVersion,
		OperationID:   allocated.OperationID,
		MutationID:    allocated.MutationID,
		Kind:          "agent-prompt",
		RequestSHA256: allocated.MutationID,
		SessionID:     sessionID,
		ProjectID:     "example",
		Input:         input,
		Status:        "failed",
		Error:         "prompt failed before admission",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := s.writeDurableMutation(failed); err != nil {
		t.Fatal(err)
	}
	blocked, err := s.TaskExecutionBlock(ctx, TaskExecutionBlockInput{
		ProjectID: "example",
		Key:       task.ID,
		Reason:    "safe boundary",
	})
	if err != nil || blocked.Status != model.TaskExecutionBlocked {
		t.Fatalf("failed prompt prevented safe block: output=%#v err=%v", blocked, err)
	}
}

func TestTSK625CombinedLegacyBlockResumePreservesEvidenceAndLane(t *testing.T) {
	s, db := tsk585Setup(t)
	defer db.Close()
	ctx := context.Background()
	original := tsk585Task(t, s, "tsk625-combined-original", "TSK625 combined original")
	tsk585Dispatch(t, s, original.ID)
	tsk585LaneCommit(t, s, original.ID, "original candidate")
	tsk585DriveToVerification(t, s, original.ID)
	verificationOperation := tsk585VerifyTask(t, s, original.ID)
	if verificationOperation.Status != "completed" {
		t.Fatalf("verification operation=%#v", verificationOperation)
	}
	verificationBefore, found, err := db.ReadLatestTaskExecutionVerification(ctx, "example", original.ID)
	if err != nil || !found {
		t.Fatalf("read verification evidence found=%v err=%v", found, err)
	}
	if _, err := s.TaskExecutionRework(ctx, TaskExecutionReworkInput{
		ProjectID: "example",
		Key:       original.ID,
		Stage:     "code",
		Comment:   "reopen for bounded pause",
	}); err != nil {
		t.Fatalf("reopen verified Task: %v", err)
	}
	beforeBlock, found, err := db.ReadTaskExecutionState(ctx, "example", original.ID)
	if err != nil || !found || beforeBlock.Status != model.TaskExecutionChangesRequested || beforeBlock.Stage != "code" {
		t.Fatalf("original pre-block state=%#v found=%v err=%v", beforeBlock, found, err)
	}
	phasesBefore, err := db.ReadTaskExecutionPhases(ctx, "example", original.ID, "code")
	if err != nil || len(phasesBefore) < 3 {
		t.Fatalf("original lifecycle evidence=%#v err=%v", phasesBefore, err)
	}
	workerBefore, err := s.ResolveProjectWorker(ctx, "example")
	if err != nil || workerBefore.Session.ID == "" || workerBefore.Session.Status != durableSession.StatusActive {
		t.Fatalf("resolve active Worker binding before block: worker=%#v err=%v", workerBefore, err)
	}
	workerRecordsBefore, err := durableSession.NewStoreWithDurability(db).List()
	if err != nil {
		t.Fatal(err)
	}
	workerRecordsBefore = slices.DeleteFunc(workerRecordsBefore, func(record durableSession.Record) bool {
		return record.Role != durableSession.RoleWorker || record.Status != durableSession.StatusActive
	})
	if len(workerRecordsBefore) != 1 || workerRecordsBefore[0].ID != workerBefore.Session.ID {
		t.Fatalf("Worker Session authority before block: records=%#v worker=%#v", workerRecordsBefore, workerBefore)
	}
	assertWorkerContinuity := func(label string, candidate *Service) {
		t.Helper()
		worker, resolveErr := candidate.ResolveProjectWorker(ctx, "example")
		if resolveErr != nil || worker.Session.ID != workerBefore.Session.ID || worker.Session.Role != durableSession.RoleWorker || worker.Session.Status != durableSession.StatusActive || worker.Agent.AgentID != workerBefore.Agent.AgentID || worker.Binding.SessionKey != workerBefore.Binding.SessionKey {
			t.Fatalf("Worker binding changed %s: before=%#v after=%#v err=%v", label, workerBefore, worker, resolveErr)
		}
		records, listErr := durableSession.NewStoreWithDurability(db).List()
		records = slices.DeleteFunc(records, func(record durableSession.Record) bool {
			return record.Role != durableSession.RoleWorker || record.Status != durableSession.StatusActive
		})
		if listErr != nil || len(records) != len(workerRecordsBefore) || records[0].ID != workerBefore.Session.ID {
			t.Fatalf("Worker Session cardinality changed %s: before=%#v after=%#v err=%v", label, workerRecordsBefore, records, listErr)
		}
	}

	tsk625SeedAgentPrompt(t, s, db, original.ID, "failed", strings.Repeat("d", 64))
	if _, err := s.TaskExecutionBlock(ctx, TaskExecutionBlockInput{
		ProjectID: "example",
		Key:       original.ID,
		Reason:    "failed prompt is reconciled",
	}); err != nil {
		t.Fatalf("block after failed prompt: %v", err)
	}
	blocked, found, err := db.ReadTaskExecutionState(ctx, "example", original.ID)
	if err != nil || !found || blocked.Status != model.TaskExecutionBlocked {
		t.Fatalf("blocked original=%#v found=%v err=%v", blocked, found, err)
	}
	assertWorkerContinuity("block", s)
	blockedPhases, err := db.ReadTaskExecutionPhases(ctx, "example", original.ID, "code")
	if err != nil {
		t.Fatal(err)
	}
	workerCtx := WithAgentSessionID(ctx, workerBefore.Session.ID)
	if _, err := s.TaskExecutionSubmitCode(workerCtx, "example", original.ID); err == nil {
		t.Fatal("Worker submission was accepted while the Task was blocked")
	}
	afterRejectedSubmit, found, err := db.ReadTaskExecutionState(ctx, "example", original.ID)
	if err != nil || !found || afterRejectedSubmit != blocked {
		t.Fatalf("rejected blocked Worker submission mutated state: before=%#v after=%#v found=%v err=%v", blocked, afterRejectedSubmit, found, err)
	}
	afterRejectedPhases, err := db.ReadTaskExecutionPhases(ctx, "example", original.ID, "code")
	if err != nil || !reflect.DeepEqual(blockedPhases, afterRejectedPhases) {
		t.Fatalf("rejected blocked Worker submission mutated history: before=%#v after=%#v err=%v", blockedPhases, afterRejectedPhases, err)
	}

	restarted := NewWithDurabilityDeferredWorkers(s.Config, db)
	statusAfterRestart, err := restarted.TaskExecutionStatus(ctx, "example", original.ID)
	if err != nil || statusAfterRestart.Status != model.TaskExecutionBlocked || statusAfterRestart.Reason != "failed prompt is reconciled" {
		t.Fatalf("blocked status after restart=%#v err=%v", statusAfterRestart, err)
	}
	assertWorkerContinuity("restart", restarted)

	prerequisite, integrationHead := tsk585IntegratedCompleteFixture(t, s, "tsk625-combined-prerequisite")
	plannerSession := tsk585PlannerSession(t, s)
	completionReview := tsk585CompleteEvidence(t, s, prerequisite, &plannerSession, model.OperatorTaskReview, []string{tsk585ReviewRationale(t, prerequisite, 1, "integrated", integrationHead, "")}, []string{integrationHead})
	completion, err := s.TaskComplete(ctx, tsk585CompletionInput(prerequisite, "integrated", "prerequisite completed", completionReview.ID), "planner")
	if err != nil || completion.Status != "done" {
		t.Fatalf("complete prerequisite: output=%#v err=%v", completion, err)
	}
	prerequisiteState, found, err := db.ReadTaskExecutionState(ctx, "example", prerequisite.ID)
	if err != nil || !found || prerequisiteState.Status != model.TaskExecutionDone || !model.IsTaskExecutionTerminal(prerequisiteState.Status) || model.IsTaskExecutionAgentActionable(prerequisiteState.Status, prerequisiteState.Stage) {
		t.Fatalf("prerequisite did not reach terminal non-actionable boundary: state=%#v found=%v err=%v", prerequisiteState, found, err)
	}

	resumed, err := restarted.TaskExecutionResume(ctx, TaskExecutionResumeInput{
		ProjectID: "example",
		Key:       original.ID,
		Reason:    "prerequisite completed",
	})
	if err != nil {
		t.Fatalf("resume original after prerequisite handoff: %v", err)
	}
	if resumed.Status != beforeBlock.Status || resumed.Stage != beforeBlock.Stage || resumed.Worktree != beforeBlock.Worktree || resumed.Head != strings.ToLower(beforeBlock.Head[:8]) || resumed.Agent != beforeBlock.Agent || resumed.ExecutionRevision != beforeBlock.ExecutionRevision+2 {
		t.Fatalf("resumed output=%#v before=%#v", resumed, beforeBlock)
	}
	afterResume, found, err := db.ReadTaskExecutionState(ctx, "example", original.ID)
	if err != nil || !found || afterResume.Status != beforeBlock.Status || afterResume.Stage != beforeBlock.Stage || afterResume.Worktree != beforeBlock.Worktree || afterResume.BaseHead != beforeBlock.BaseHead || afterResume.Head != beforeBlock.Head || afterResume.Branch != beforeBlock.Branch || afterResume.Agent != beforeBlock.Agent || afterResume.TaskRevision != beforeBlock.TaskRevision || afterResume.TaskRevisionSHA256 != beforeBlock.TaskRevisionSHA256 || afterResume.ExecutionRevision != beforeBlock.ExecutionRevision+2 {
		t.Fatalf("resumed lane/lineage changed: before=%#v after=%#v found=%v err=%v", beforeBlock, afterResume, found, err)
	}
	assertWorkerContinuity("resume", restarted)
	phasesAfter, err := db.ReadTaskExecutionPhases(ctx, "example", original.ID, "code")
	if err != nil || len(phasesAfter) != len(phasesBefore)+2 || !reflect.DeepEqual(phasesBefore, phasesAfter[:len(phasesBefore)]) {
		t.Fatalf("original lifecycle evidence changed: before=%#v after=%#v err=%v", phasesBefore, phasesAfter, err)
	}
	if phasesAfter[len(phasesBefore)].EventKind != "block" || phasesAfter[len(phasesBefore)+1].EventKind != "resume" {
		t.Fatalf("missing block/resume lineage phases=%#v", phasesAfter)
	}
	verificationAfter, found, err := db.ReadLatestTaskExecutionVerification(ctx, "example", original.ID)
	if err != nil || !found || !reflect.DeepEqual(verificationBefore, verificationAfter) {
		t.Fatalf("verification evidence changed: before=%#v after=%#v found=%v err=%v", verificationBefore, verificationAfter, found, err)
	}
}

func TestTSK625TransitionCASFailsClosedWithoutPhaseMutation(t *testing.T) {
	s, db := tsk585Setup(t)
	defer db.Close()
	ctx := context.Background()
	task := tsk585Task(t, s, "tsk625-cas", "TSK625 CAS")
	tsk585Dispatch(t, s, task.ID)
	state, _, _ := db.ReadTaskExecutionState(ctx, "example", task.ID)
	next := state
	next.Status = model.TaskExecutionBlocked
	next.ExecutionRevision++
	next.UpdatedAt = time.Now().UTC()
	phase := sqlitestore.TaskExecutionPhase{TaskID: next.TaskID, ProjectID: next.ProjectID, ExecutionRevision: next.ExecutionRevision, Stage: next.Stage, Status: next.Status, Head: next.Head, Branch: next.Branch, TaskRevisionSHA256: next.TaskRevisionSHA256, EventKind: "block", Decision: state.Status, Comment: "cas", CreatedAt: next.UpdatedAt}
	if err := db.TransitionTaskExecutionState(ctx, next, state.ExecutionRevision-1, phase); err == nil {
		t.Fatal("stale execution revision was accepted")
	}
	current, _, _ := db.ReadTaskExecutionState(ctx, "example", task.ID)
	if current != state {
		t.Fatalf("CAS failure changed state: before=%#v after=%#v", state, current)
	}
	phases, err := db.ReadTaskExecutionPhases(ctx, "example", task.ID, state.Stage)
	if err != nil || len(phases) != 0 {
		t.Fatalf("CAS failure inserted phase history=%#v err=%v", phases, err)
	}
}

func TestTSK625BlockedStatusIsNotAgentOwnedActionable(t *testing.T) {
	if model.IsTaskExecutionAgentActionable(model.TaskExecutionBlocked, "code") {
		t.Fatal("blocked execution is Worker-actionable")
	}
	if !model.IsTaskExecutionNonTerminal(model.TaskExecutionBlocked) {
		t.Fatal("blocked execution became terminal")
	}
	if !model.IsTaskExecutionAgentOwned(model.TaskExecutionBlocked) {
		t.Fatal("blocked execution lost durable Worker ownership")
	}
}
