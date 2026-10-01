package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	upstream "github.com/rceman/go-sqlite-store/store"
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

func tsk660TestTask() model.TaskAuthoring {
	return model.TaskAuthoring{
		ID:             tsk660TaskID,
		ProjectID:      config.GTWProjectID,
		Revision:       tsk660TaskRevision,
		RevisionSHA256: strings.Repeat("a", 64),
		Status:         model.TaskAuthoringPlanned,
	}
}

func tsk660TestPlannerEntry() model.JournalEntry {
	data, _ := json.Marshal(taskBootstrapPlannerJournalData{
		Summary: "Planner authorizes a bounded transition reconciliation for GTW-TSK660.",
	})
	return model.JournalEntry{
		SchemaVersion: model.SchemaVersion,
		ID:            tsk660PlannerJournalKey,
		ProjectID:     config.GTWProjectID,
		Sequence:      tsk660PlannerJournalSeq,
		Stream:        model.JournalStreamPlannerNotes,
		Status:        model.JournalStatusPublished,
		Role:          "planner",
		Actor:         "HOM_GTW_P_0vext",
		SessionID:     "HOM_GTW_P_0vext",
		Data:          data,
		CreatedAt:     time.Date(2026, 10, 1, 11, 37, 41, 0, time.UTC),
	}
}

func tsk660TestLeadGateEntry(sequence uint64, problem string, evidence []string, createdAt time.Time, actor, sessionID string) model.JournalEntry {
	data, _ := json.Marshal(taskBootstrapLeadJournalData{
		Problem:             problem,
		Evidence:            evidence,
		Impact:              "TSK660 reconciliation blocked until durable Lead review exists.",
		ProposedImprovement: "Record Lead review evidence before reconciliation.",
	})
	return model.JournalEntry{
		SchemaVersion: model.SchemaVersion,
		ID:            fmt.Sprintf("GTW-JRN%d", sequence),
		ProjectID:     config.GTWProjectID,
		Sequence:      sequence,
		Stream:        model.JournalStreamLeadFriction,
		Status:        model.JournalStatusPublished,
		Role:          "lead",
		Actor:         actor,
		SessionID:     sessionID,
		Data:          data,
		CreatedAt:     createdAt,
	}
}

func TestTSK660PlannerAuthorizationContentIsExact(t *testing.T) {
	valid := taskBootstrapPlannerJournalData{
		Summary: "Planner authorizes a bounded transition reconciliation for GTW-TSK660, whose implementation commit 899abc9 is already on canonical main from the pre-execution lifecycle while the durable Task record is still planned with no execution state.",
		Decisions: []string{
			"Do not normally redispatch/reimplement GTW-TSK660; its implementation already landed before the current execution lifecycle.",
			"Extend the transition-only debug reconciliation mechanism with an exact TSK660 historical-pre-execution case rather than creating a general normal integration bypass.",
			"Reconciliation must derive and validate the exact TSK660 implementation commit/tree/base from repository history and current main ancestry; caller supplies only the exact Task key.",
			"Preserve the truth that TSK660 had no current-model Worker execution/submission/review/test lifecycle. Do not fabricate execution phases or receipts.",
			"The reconciliation may create only the minimum durable historical integration/projection evidence needed for dependency and Track semantics, explicitly marked as pre-execution historical reconciliation.",
			"Current Planner journal authorization and Lead read-only review/gate evidence are required before terminalization.",
			"After TSK660 reconciles as integrated, TSK606 and TSK529 may become eligible through their existing dependencies.",
		},
		Commitments: []string{
			"Keep this mechanism under transition-only debug/* and out of the final clean-room MIL1 happy path.",
			"Do not generalize to arbitrary already-landed Tasks without exact Planner authorization and source identity.",
			"No direct Shared/Hub/SQLite surgery and no source mutation.",
		},
		Facts: []string{
			"GTW-TSK660 is revision 6, planned, with dependency GTW-TSK478 already done.",
			"Lead reports implementation commit 899abc9 is an ancestor of current main and was landed under the pre-execution lifecycle.",
			"GTW-TSK660 is not a member of the old execution Tracks that could otherwise supply current-model execution evidence.",
			"GTW-TSK606 and GTW-TSK529 are blocked on GTW-TSK660.",
		},
		Assumptions: []string{
			"Lead will independently verify the exact implementation commit, base/tree identities, current-main ancestry, and semantic match to TSK660 before reconciliation.",
		},
		Blockers:   []string{},
		Unresolved: []string{},
		NextActions: []string{
			"Implement the exact transition reconciliation for TSK660.",
			"Reconcile TSK660 honestly as historical/pre-execution integrated.",
			"Reread TRK2 eligibility and continue with TSK606/TSK529.",
		},
		References: []string{"GTW-TSK660", "GTW-TSK678", "GTW-JRN14", "899abc9"},
	}
	if !tsk660PlannerAuthorizationValid(valid) {
		t.Fatal("authorized Planner journal data was rejected")
	}
	drifted := valid
	drifted.Summary = "Planner authorizes arbitrary reconciliation."
	if tsk660PlannerAuthorizationValid(drifted) {
		t.Fatal("mutated Planner journal data was accepted")
	}
	drifted = valid
	drifted.Decisions = append(drifted.Decisions, "Also allow any other Task.")
	if tsk660PlannerAuthorizationValid(drifted) {
		t.Fatal("broadened Planner authorization was accepted")
	}
}

func TestTSK660LeadRecordContentIsExact(t *testing.T) {
	valid := taskBootstrapLeadJournalData{
		Problem: "GTW-TSK660 (Milestone+Track implementation, P0) is already-landed on main but unreconciled: it blocks dependent TSK606 (P0) and TSK529 (P1) from eligibility.",
		Evidence: []string{
			"TSK660 shared record: revision 6, status planned, no execution state, never dispatched, not a member of TRK1 or TRK2.",
			"Its implementation commit 899abc9 ('Task GTW-TSK660: Implement Milestone roadmap membership and Shared Track execution/delivery authority', Sept 22) is an ancestor of current main — the work landed under the pre-execution-model lifecycle.",
			"Record was likely reset to planned during the execution-model cutover, losing integrated state — same unreconciled-historical class as TSK677.",
			"TSK606 and TSK529 declare TSK660 as a dependency; while TSK660 reads 'planned' they remain dependency-blocked despite the functionality existing.",
			"debug/task-bootstrap-reconcile is enum-pinned to GTW-TSK677 and cannot reconcile TSK660.",
		},
		Impact:              "Planner decision needed: either reconcile TSK660 as already-landed via an extended mechanism, or authorize a normal dispatch path. Until then TSK606/TSK529 stay blocked; TSK627/TSK679/TSK669/TSK671/TSK672 remain eligible and are being worked.",
		ProposedImprovement: "Generalize the bootstrap-reconcile mechanism to a bounded already-landed reconciliation class (exact identity + Planner authorization per Task), or issue a dedicated reconciliation for TSK660.",
	}
	if !tsk660LeadRecordValid(valid) {
		t.Fatal("authorized Lead record was rejected")
	}
	drifted := valid
	drifted.Evidence = append(drifted.Evidence, "Additional fabricated claim.")
	if tsk660LeadRecordValid(drifted) {
		t.Fatal("mutated Lead record was accepted")
	}
}

func TestTSK660LeadGateEntryPredicateIsStrict(t *testing.T) {
	planner := tsk660TestPlannerEntry()
	after := planner.CreatedAt.Add(time.Hour)
	actor, sessionID := "gpt-tunnel-gateway_lead", "HOM_GTW_L_8yzfj"
	valid := tsk660TestLeadGateEntry(17, "Reviewed GTW-TSK660 implementation 899abc9.", []string{
		"Gate full_test passed on current main containing 899abc9.",
	}, after, actor, sessionID)
	if !tsk660LeadGateEntryValid(valid, planner) {
		t.Fatal("valid post-authorization Lead gate entry was rejected")
	}
	preAuth := valid
	preAuth.CreatedAt = planner.CreatedAt.Add(-time.Hour)
	if tsk660LeadGateEntryValid(preAuth, planner) {
		t.Fatal("pre-authorization entry counted as gate evidence")
	}
	sameSession := valid
	sameSession.Actor = planner.Actor
	if tsk660LeadGateEntryValid(sameSession, planner) {
		t.Fatal("Planner-authored entry counted as independent Lead evidence")
	}
	wrongRole := valid
	wrongRole.Role = "worker"
	if tsk660LeadGateEntryValid(wrongRole, planner) {
		t.Fatal("non-Lead entry counted as gate evidence")
	}
	noGate := tsk660TestLeadGateEntry(18, "GTW-TSK660 and 899abc9 discussed.", []string{"Discussed the record."}, after, actor, sessionID)
	gatelessData, _ := json.Marshal(taskBootstrapLeadJournalData{
		Problem:             "GTW-TSK660 and 899abc9.",
		Evidence:            []string{"Discussed the record."},
		Impact:              "none",
		ProposedImprovement: "none",
	})
	noGate.Data = gatelessData
	if tsk660LeadGateEntryValid(noGate, planner) {
		t.Fatal("entry without gate evidence counted")
	}
	unrelated := tsk660TestLeadGateEntry(19, "Some other task.", []string{"Gate full_test passed."}, after, actor, sessionID)
	if tsk660LeadGateEntryValid(unrelated, planner) {
		t.Fatal("entry not referencing the task/commit counted")
	}
}

func tsk660TestJournals() taskPreExecutionJournalEvidence {
	digest := strings.Repeat("b", 64)
	return taskPreExecutionJournalEvidence{
		PlannerKey:    tsk660PlannerJournalKey,
		PlannerDigest: digest,
		LeadKey:       tsk660LeadJournalKey,
		LeadDigest:    digest,
		GateKeys:      []string{"GTW-JRN17"},
		GateDigests:   []string{digest},
	}
}

func tsk660IntegratedState(task model.TaskAuthoring) model.TaskExecutionState {
	return model.TaskExecutionState{
		TaskID: tsk660TaskID, ProjectID: config.GTWProjectID,
		TaskRevision: task.Revision, TaskRevisionSHA256: task.RevisionSHA256,
		Status: model.TaskExecutionIntegrated, Stage: "code",
		Worktree: tsk660ExecutionWorktree, BaseHead: tsk660MainBase, Head: tsk660FinalSubmissionHead,
		Branch: tsk660ExecutionBranch, Agent: config.GTWWorkerAgentID,
		ExecutionRevision: tsk660IntegratedRevision, UpdatedAt: time.Now().UTC(),
	}
}

func tsk660TestPhase(task model.TaskAuthoring, revision int, stage, status, head, kind, decision, comment string, at time.Time) sqlitestore.TaskExecutionPhase {
	return sqlitestore.TaskExecutionPhase{
		TaskID: tsk660TaskID, ProjectID: config.GTWProjectID,
		ExecutionRevision: revision, Stage: stage, Status: status, Head: head,
		Branch: tsk660ExecutionBranch, TaskRevisionSHA256: task.RevisionSHA256,
		EventKind: kind, Decision: decision, Comment: comment, CreatedAt: at,
	}
}

func tsk660TestPhases(task model.TaskAuthoring) taskPreExecutionPhases {
	base := time.Date(2026, 9, 22, 9, 35, 0, 0, time.UTC)
	return taskPreExecutionPhases{
		code: []sqlitestore.TaskExecutionPhase{
			tsk660TestPhase(task, 2, "code", model.TaskExecutionAwaitingReview, tsk660FirstSubmissionHead, "submission", "", "", base),
			tsk660TestPhase(task, 3, "code", model.TaskExecutionChangesRequested, tsk660FirstSubmissionHead, "rework", "", "rework directive", base.Add(time.Minute)),
			tsk660TestPhase(task, 4, "code", model.TaskExecutionAwaitingReview, tsk660FinalSubmissionHead, "submission", "", "", base.Add(2*time.Minute)),
			tsk660TestPhase(task, 5, "code", model.TaskExecutionReadyForVerification, tsk660FinalSubmissionHead, "review", "accept", "review comment", base.Add(3*time.Minute)),
		},
		integration: []sqlitestore.TaskExecutionPhase{
			tsk660TestPhase(task, 9, "integration", model.TaskExecutionIntegrated, tsk660ImplementationCommit, "integration", "accept", "", base.Add(4*time.Minute)),
		},
	}
}

func tsk660TestVerification() taskPreExecutionVerificationProof {
	return taskPreExecutionVerificationProof{
		OperationID:   tsk660LegacyVerificationOperation,
		CandidateHead: tsk660LegacyVerificationCandidate,
		ReceiptSHA256: strings.Repeat("c", 64),
		CompletedAt:   time.Now().UTC(),
	}
}

func tsk660TestCompletionEvent(t *testing.T, task model.TaskAuthoring, journals taskPreExecutionJournalEvidence, verification taskPreExecutionVerificationProof, phases taskPreExecutionPhases, recordedAt time.Time) sqlitestore.TaskLifecycleEvent {
	t.Helper()
	phasesSHA, err := tsk660PhasesDigest(phases)
	if err != nil {
		t.Fatal(err)
	}
	contract, err := tsk660CompletionContract(task, journals, verification, phasesSHA, strings.Repeat("d", 40))
	if err != nil {
		t.Fatal(err)
	}
	return sqlitestore.TaskLifecycleEvent{
		OperationID: "task-complete-test", ProjectID: config.GTWProjectID, TaskID: tsk660TaskID,
		Revision: tsk660TaskRevision, EventKind: sqlitestore.TaskLifecycleEventKindComplete,
		FromStatus: model.TaskAuthoringPlanned, ToStatus: model.TaskAuthoringDone,
		Actor: "HOM_GTW_L_test", Reason: tsk660ReconcileReason, Contract: contract, RecordedAt: recordedAt,
	}
}

func TestTSK660ValidationRequiresExactExistingLifecycle(t *testing.T) {
	task := tsk660TestTask()
	journals := tsk660TestJournals()
	verification := tsk660TestVerification()
	state := tsk660IntegratedState(task)
	phases := tsk660TestPhases(task)
	phasesSHA, err := tsk660PhasesDigest(phases)
	if err != nil {
		t.Fatal(err)
	}
	evidence, already, err := validateTSK660State(task, state, true, phases, phasesSHA, verification, sqlitestore.TaskLifecycleEvent{}, false, false, journals)
	if err != nil || already || evidence.Kind != "" {
		t.Fatalf("fresh validation evidence=%#v already=%v err=%v", evidence, already, err)
	}
	// A wrong task identity fails closed.
	wrong := task
	wrong.Revision = 7
	if _, _, err := validateTSK660State(wrong, state, true, phases, phasesSHA, verification, sqlitestore.TaskLifecycleEvent{}, false, false, journals); err == nil {
		t.Fatal("revised authoring record passed pre-execution validation")
	}
	// No execution state fails closed — this reconcile never mints one.
	if _, _, err := validateTSK660State(task, model.TaskExecutionState{}, false, phases, phasesSHA, verification, sqlitestore.TaskLifecycleEvent{}, false, false, journals); err == nil {
		t.Fatal("missing execution state passed pre-execution validation")
	}
	// Reset evidence fails closed.
	if _, _, err := validateTSK660State(task, state, true, phases, phasesSHA, verification, sqlitestore.TaskLifecycleEvent{}, false, true, journals); err == nil {
		t.Fatal("reset-phase evidence passed pre-execution validation")
	}
	// Divergent real-state fields fail closed.
	tampered := state
	tampered.Head = strings.Repeat("e", 40)
	if _, _, err := validateTSK660State(task, tampered, true, phases, phasesSHA, verification, sqlitestore.TaskLifecycleEvent{}, false, false, journals); err == nil {
		t.Fatal("tampered execution head passed pre-execution validation")
	}
	// Missing or extra lifecycle phases fail closed.
	short := phases
	short.code = phases.code[:3]
	if _, _, err := validateTSK660State(task, state, true, short, phasesSHA, verification, sqlitestore.TaskLifecycleEvent{}, false, false, journals); err == nil {
		t.Fatal("truncated phase history passed pre-execution validation")
	}
	extra := phases
	extra.tests = []sqlitestore.TaskExecutionPhase{{TaskID: tsk660TaskID}}
	if _, _, err := validateTSK660State(task, state, true, extra, phasesSHA, verification, sqlitestore.TaskLifecycleEvent{}, false, false, journals); err == nil {
		t.Fatal("extra phase evidence passed pre-execution validation")
	}
	// A done authoring record without the completion event fails closed.
	done := task
	done.Status = model.TaskAuthoringDone
	if _, _, err := validateTSK660State(done, state, true, phases, phasesSHA, verification, sqlitestore.TaskLifecycleEvent{}, false, false, journals); err == nil {
		t.Fatal("done authoring without completion event passed")
	}
}

func TestTSK660ValidationReplayRequiresExactReceipt(t *testing.T) {
	task := tsk660TestTask()
	journals := tsk660TestJournals()
	verification := tsk660TestVerification()
	phases := tsk660TestPhases(task)
	phasesSHA, err := tsk660PhasesDigest(phases)
	if err != nil {
		t.Fatal(err)
	}
	recordedAt := time.Now().UTC()
	event := tsk660TestCompletionEvent(t, task, journals, verification, phases, recordedAt)
	done := task
	done.Status = model.TaskAuthoringDone
	state := tsk660IntegratedState(task)
	state.Status = model.TaskExecutionDone
	state.ExecutionRevision = tsk660CompletedRevision
	state.UpdatedAt = recordedAt
	evidence, already, err := validateTSK660State(done, state, true, phases, phasesSHA, verification, event, true, false, journals)
	if err != nil || !already {
		t.Fatalf("exact replay rejected: evidence=%#v already=%v err=%v", evidence, already, err)
	}
	if evidence.ImplementationCommit != tsk660ImplementationCommit || evidence.MainBase != tsk660MainBase || evidence.ImplementationTree != tsk660ImplementationTree || evidence.LegacyVerificationOperation != tsk660LegacyVerificationOperation || evidence.ExecutionRevision != tsk660IntegratedRevision {
		t.Fatalf("replay evidence identity=%#v", evidence)
	}
	// Tampered integration head fails the exact lifecycle check.
	tamperedPhases := phases
	bad := phases.integration[0]
	bad.Head = strings.Repeat("e", 40)
	tamperedPhases.integration = []sqlitestore.TaskExecutionPhase{bad}
	badSHA, _ := tsk660PhasesDigest(tamperedPhases)
	if _, _, err := validateTSK660State(done, state, true, tamperedPhases, badSHA, verification, event, true, false, journals); err == nil {
		t.Fatal("tampered phase head passed replay validation")
	}
	// A fabricated non-reconciliation contract fails.
	fakeEvent := event
	fakeEvent.Contract = []byte(`{"schema_version":1}`)
	if _, _, err := validateTSK660State(done, state, true, phases, phasesSHA, verification, fakeEvent, true, false, journals); err == nil {
		t.Fatal("fabricated evidence envelope passed replay validation")
	}
	// Different gate journals recorded than present: fail.
	changedJournals := journals
	changedJournals.GateKeys = []string{"GTW-JRN99"}
	if _, _, err := validateTSK660State(done, state, true, phases, phasesSHA, verification, event, true, false, changedJournals); err == nil {
		t.Fatal("drifted gate evidence passed replay validation")
	}
	// A different persisted receipt than the minted one fails replay.
	changedVerification := verification
	changedVerification.ReceiptSHA256 = strings.Repeat("f", 64)
	if _, _, err := validateTSK660State(done, state, true, phases, phasesSHA, changedVerification, event, true, false, journals); err == nil {
		t.Fatal("drifted legacy verification identity passed replay validation")
	}
	// A changed phase history (digest mismatch) fails replay.
	otherPhases := phases
	other := phases.integration[0]
	other.Comment = "tampered"
	otherPhases.integration = []sqlitestore.TaskExecutionPhase{other}
	// Other fails the exact check before the digest — use a comment-free
	// tamper surface: different created_at keeps structure valid but moves the digest.
	other = phases.integration[0]
	other.CreatedAt = phases.integration[0].CreatedAt.Add(time.Second)
	otherPhases.integration = []sqlitestore.TaskExecutionPhase{other}
	otherSHA, _ := tsk660PhasesDigest(otherPhases)
	if _, _, err := validateTSK660State(done, state, true, otherPhases, otherSHA, verification, event, true, false, journals); err == nil {
		t.Fatal("drifted phase digest passed replay validation")
	}
	// A non-terminal execution state fails closed.
	progress := state
	progress.Status = model.TaskExecutionInProgress
	if _, _, err := validateTSK660State(done, progress, true, phases, phasesSHA, verification, event, true, false, journals); err == nil {
		t.Fatal("non-terminal execution state passed pre-execution validation")
	}
}

func tsk660ReceiptJSON(t *testing.T, task model.TaskAuthoring, mutate func(*model.TaskExecutionVerification)) string {
	t.Helper()
	receipt := model.TaskExecutionVerification{
		ProjectID:          config.GTWProjectID,
		TaskID:             tsk660TaskID,
		OperationID:        tsk660LegacyVerificationOperation,
		TaskRevisionSHA256: task.RevisionSHA256,
		BaseHead:           tsk660MainBase,
		CandidateHead:      tsk660LegacyVerificationCandidate,
		CandidateTree:      tsk660ImplementationTree,
		Branch:             tsk660ExecutionBranch,
		GateProfileSHA256:  strings.Repeat("1", 64),
		Outcome:            model.TaskExecutionVerificationSucceeded,
		TaskRevision:       tsk660TaskRevision,
		AttemptRevision:    tsk660TaskRevision,
		CodeReviewID:       tsk660LegacyVerificationReviewID,
		Gates: []model.CompletionGateResult{
			{ID: "format", ExitCode: 0, Execution: "executed", TreeID: tsk660ImplementationTree},
			{ID: "check", ExitCode: 0, Execution: "executed", TreeID: tsk660ImplementationTree},
			{ID: "test", ExitCode: 0, Execution: "executed", TreeID: tsk660ImplementationTree},
		},
		StartedAt:   time.Now().UTC().Add(-time.Hour),
		CompletedAt: time.Now().UTC(),
	}
	if mutate != nil {
		mutate(&receipt)
	}
	raw, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func tsk660InsertReceipt(t *testing.T, db *sqlitestore.Databases, operationID string, attempt int, outcome, receiptJSON string) {
	t.Helper()
	_, err := db.Local.Batch(context.Background(), []upstream.Statement{{
		SQL:  `INSERT INTO local_task_execution_verifications(project_id,task_id,operation_id,attempt_revision,outcome,receipt_json,created_at) VALUES(?,?,?,?,?,?,?)`,
		Args: []any{config.GTWProjectID, tsk660TaskID, operationID, attempt, outcome, receiptJSON, time.Now().UTC().Format(time.RFC3339Nano)},
	}})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTSK660LegacyVerificationExactShapeOnly(t *testing.T) {
	task := tsk660TestTask()
	cases := []struct {
		name    string
		setup   func(t *testing.T, db *sqlitestore.Databases)
		wantErr bool
	}{
		{name: "exact receipt accepted", setup: func(t *testing.T, db *sqlitestore.Databases) {
			tsk660InsertReceipt(t, db, tsk660LegacyVerificationOperation, tsk660TaskRevision, model.TaskExecutionVerificationSucceeded, tsk660ReceiptJSON(t, task, nil))
		}},
		{name: "no receipt fails closed", wantErr: true, setup: func(t *testing.T, db *sqlitestore.Databases) {}},
		{name: "second receipt fails closed", wantErr: true, setup: func(t *testing.T, db *sqlitestore.Databases) {
			tsk660InsertReceipt(t, db, tsk660LegacyVerificationOperation, tsk660TaskRevision, model.TaskExecutionVerificationSucceeded, tsk660ReceiptJSON(t, task, nil))
			tsk660InsertReceipt(t, db, "GTW-OPR9999", tsk660TaskRevision+1, model.TaskExecutionVerificationSucceeded, tsk660ReceiptJSON(t, task, func(r *model.TaskExecutionVerification) { r.OperationID = "GTW-OPR9999" }))
		}},
		{name: "wrong operation fails", wantErr: true, setup: func(t *testing.T, db *sqlitestore.Databases) {
			tsk660InsertReceipt(t, db, "GTW-OPR9999", tsk660TaskRevision, model.TaskExecutionVerificationSucceeded, tsk660ReceiptJSON(t, task, func(r *model.TaskExecutionVerification) { r.OperationID = "GTW-OPR9999" }))
		}},
		{name: "failed outcome fails", wantErr: true, setup: func(t *testing.T, db *sqlitestore.Databases) {
			tsk660InsertReceipt(t, db, tsk660LegacyVerificationOperation, tsk660TaskRevision, model.TaskExecutionVerificationFailed, tsk660ReceiptJSON(t, task, func(r *model.TaskExecutionVerification) {
				r.Outcome = model.TaskExecutionVerificationFailed
				r.Error = "gate failed"
			}))
		}},
		{name: "wrong candidate tree fails", wantErr: true, setup: func(t *testing.T, db *sqlitestore.Databases) {
			tsk660InsertReceipt(t, db, tsk660LegacyVerificationOperation, tsk660TaskRevision, model.TaskExecutionVerificationSucceeded, tsk660ReceiptJSON(t, task, func(r *model.TaskExecutionVerification) { r.CandidateTree = strings.Repeat("9", 40) }))
		}},
		{name: "undecodable payload fails", wantErr: true, setup: func(t *testing.T, db *sqlitestore.Databases) {
			tsk660InsertReceipt(t, db, tsk660LegacyVerificationOperation, tsk660TaskRevision, model.TaskExecutionVerificationSucceeded, "{not-json")
		}},
		{name: "failing gate fails", wantErr: true, setup: func(t *testing.T, db *sqlitestore.Databases) {
			tsk660InsertReceipt(t, db, tsk660LegacyVerificationOperation, tsk660TaskRevision, model.TaskExecutionVerificationSucceeded, tsk660ReceiptJSON(t, task, func(r *model.TaskExecutionVerification) { r.Gates[1].ExitCode = 1 }))
		}},
		{name: "missing gate fails", wantErr: true, setup: func(t *testing.T, db *sqlitestore.Databases) {
			tsk660InsertReceipt(t, db, tsk660LegacyVerificationOperation, tsk660TaskRevision, model.TaskExecutionVerificationSucceeded, tsk660ReceiptJSON(t, task, func(r *model.TaskExecutionVerification) { r.Gates = r.Gates[:2] }))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, err := sqlitestore.Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			tc.setup(t, db)
			s := &Service{
				Config:     config.Config{StateDir: t.TempDir()},
				Durability: db,
			}
			proof, err := s.readTSK660LegacyVerification(context.Background(), task)
			if tc.wantErr {
				if err == nil {
					t.Fatal("invalid verification receipt shape was accepted")
				}
				return
			}
			if err != nil {
				t.Fatalf("authorized receipt rejected: %v", err)
			}
			if proof.OperationID != tsk660LegacyVerificationOperation || proof.CandidateHead != tsk660LegacyVerificationCandidate || len(proof.ReceiptSHA256) != 64 {
				t.Fatalf("unexpected proof=%#v", proof)
			}
		})
	}
}
