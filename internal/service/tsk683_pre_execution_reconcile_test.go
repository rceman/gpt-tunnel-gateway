package service

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

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

func tsk660MintedState(task model.TaskAuthoring) model.TaskExecutionState {
	return model.TaskExecutionState{
		TaskID: tsk660TaskID, ProjectID: config.GTWProjectID,
		TaskRevision: task.Revision, TaskRevisionSHA256: task.RevisionSHA256,
		Status: model.TaskExecutionIntegrated, Stage: "code",
		Worktree: tsk660ExecutionWorktree, BaseHead: tsk660MainBase, Head: tsk660MainBase,
		Branch: tsk660ExecutionBranch, Agent: config.GTWWorkerAgentID,
		ExecutionRevision: tsk660ExecutionRevision, UpdatedAt: time.Now().UTC(),
	}
}

func TestTSK660ValidationFreshPlannedTaskHasNoState(t *testing.T) {
	task := tsk660TestTask()
	journals := tsk660TestJournals()
	evidence, already, err := validateTSK660State(task, model.TaskExecutionState{}, false, taskPreExecutionPhases{}, false, false, journals)
	if err != nil || already || evidence.Kind != "" {
		t.Fatalf("fresh validation evidence=%#v already=%v err=%v", evidence, already, err)
	}
	// A wrong task identity fails closed.
	wrong := task
	wrong.Revision = 7
	if _, _, err := validateTSK660State(wrong, model.TaskExecutionState{}, false, taskPreExecutionPhases{}, false, false, journals); err == nil {
		t.Fatal("revised authoring record passed pre-execution validation")
	}
	// Fabricated normal-lifecycle evidence fails closed.
	if _, _, err := validateTSK660State(task, model.TaskExecutionState{}, false, taskPreExecutionPhases{}, true, false, journals); err == nil {
		t.Fatal("verification receipt presence passed pre-execution validation")
	}
	if _, _, err := validateTSK660State(task, model.TaskExecutionState{}, false, taskPreExecutionPhases{code: []sqlitestore.TaskExecutionPhase{{TaskID: tsk660TaskID}}}, false, false, journals); err == nil {
		t.Fatal("code-phase evidence passed pre-execution validation")
	}
	// No execution state but a done authoring record is ambiguous: fail closed.
	done := task
	done.Status = model.TaskAuthoringDone
	if _, _, err := validateTSK660State(done, model.TaskExecutionState{}, false, taskPreExecutionPhases{}, false, false, journals); err == nil {
		t.Fatal("done authoring record without execution state passed")
	}
}

func TestTSK660ValidationReplayRequiresExactReceipt(t *testing.T) {
	task := tsk660TestTask()
	journals := tsk660TestJournals()
	state := tsk660MintedState(task)
	comment, err := tsk660PhaseComment(task, journals, strings.Repeat("c", 40))
	if err != nil {
		t.Fatal(err)
	}
	phase := sqlitestore.TaskExecutionPhase{
		TaskID: tsk660TaskID, ProjectID: config.GTWProjectID, ExecutionRevision: tsk660ExecutionRevision,
		Stage: "integration", Status: model.TaskExecutionIntegrated, Head: tsk660ImplementationCommit,
		Branch: state.Branch, TaskRevisionSHA256: state.TaskRevisionSHA256,
		EventKind: "integration", Decision: "accept", Comment: comment, CreatedAt: state.UpdatedAt,
	}
	phases := taskPreExecutionPhases{integration: []sqlitestore.TaskExecutionPhase{phase}}
	evidence, already, err := validateTSK660State(task, state, true, phases, false, false, journals)
	if err != nil || !already {
		t.Fatalf("exact replay rejected: evidence=%#v already=%v err=%v", evidence, already, err)
	}
	if evidence.ImplementationCommit != tsk660ImplementationCommit || evidence.MainBase != tsk660MainBase || evidence.ImplementationTree != tsk660ImplementationTree {
		t.Fatalf("replay evidence identity=%#v", evidence)
	}
	// Tampered head: fail.
	tampered := phase
	tampered.Head = strings.Repeat("d", 40)
	if _, _, err := validateTSK660State(task, state, true, taskPreExecutionPhases{integration: []sqlitestore.TaskExecutionPhase{tampered}}, false, false, journals); err == nil {
		t.Fatal("tampered phase head passed replay validation")
	}
	// Fabricated non-reconciliation comment: fail.
	fake := phase
	fake.Comment = `task-pre-execution-reconciliation:{"schema_version":1}`
	if _, _, err := validateTSK660State(task, state, true, taskPreExecutionPhases{integration: []sqlitestore.TaskExecutionPhase{fake}}, false, false, journals); err == nil {
		t.Fatal("fabricated evidence envelope passed replay validation")
	}
	// Different gate journals recorded than present: fail.
	changedJournals := journals
	changedJournals.GateKeys = []string{"GTW-JRN99"}
	if _, _, err := validateTSK660State(task, state, true, phases, false, false, changedJournals); err == nil {
		t.Fatal("drifted gate evidence passed replay validation")
	}
	// A normal in-progress state fails closed.
	progress := state
	progress.Status = model.TaskExecutionInProgress
	if _, _, err := validateTSK660State(task, progress, true, phases, false, false, journals); err == nil {
		t.Fatal("non-terminal execution state passed pre-execution validation")
	}
}
