package service

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

func TestTSK678ExactPlannerAndLeadJournalEvidence(t *testing.T) {
	authority, followup, review := tsk678TestJournalEntries(t)
	proof, err := validateTSK678JournalEvidence(authority, followup, review, config.GTWProjectID)
	if err != nil {
		t.Fatalf("exact bootstrap evidence rejected: %v", err)
	}
	if !slices.Equal(proof.PlannerEntries, []string{"GTW-JRN8", "GTW-JRN9"}) || proof.LeadEntry != "GTW-JRN10" || len(proof.PlannerDigests) != 2 || proof.LeadDigest == "" {
		t.Fatalf("journal evidence proof=%#v", proof)
	}

	var planner taskBootstrapPlannerJournalData
	if err := json.Unmarshal(authority.Data, &planner); err != nil {
		t.Fatal(err)
	}
	planner.Decisions = []string{"Use a one-time transition bootstrap for an arbitrary candidate."}
	mutatedPlanner := tsk678MarshalJournalData(t, planner)
	authority.Data = mutatedPlanner
	if _, err := validateTSK678JournalEvidence(authority, followup, review, config.GTWProjectID); err == nil {
		t.Fatal("bootstrap authorization accepted altered Planner decisions")
	}

	authority, followup, review = tsk678TestJournalEntries(t)
	var lead taskBootstrapLeadJournalData
	if err := json.Unmarshal(review.Data, &lead); err != nil {
		t.Fatal(err)
	}
	lead.Evidence[4] = strings.ReplaceAll(lead.Evidence[4], tsk678CandidateTree, strings.Repeat("f", 40))
	review.Data = tsk678MarshalJournalData(t, lead)
	if _, err := validateTSK678JournalEvidence(authority, followup, review, config.GTWProjectID); err == nil {
		t.Fatal("Lead review accepted a mismatched candidate tree")
	}

	authority, followup, review = tsk678TestJournalEntries(t)
	review.Role = "planner"
	if _, err := validateTSK678JournalEvidence(authority, followup, review, config.GTWProjectID); err == nil {
		t.Fatal("Planner record was accepted as independent Lead review evidence")
	}
}

func TestTSK678SourceProofRequiresExactCandidateBaseTreeAndCanonicalMain(t *testing.T) {
	main := strings.Repeat("c", 40)
	if err := validateTSK678SourceIdentity(tsk678CandidateHead, tsk678CandidateTree, []string{tsk678MainBase}, main, true); err != nil {
		t.Fatalf("exact already-landed source rejected: %v", err)
	}
	cases := map[string]struct {
		candidate string
		tree      string
		parents   []string
		main      string
		reachable bool
	}{
		"near-miss candidate": {candidate: strings.Repeat("b", 40), tree: tsk678CandidateTree, parents: []string{tsk678MainBase}, main: main, reachable: true},
		"wrong tree":          {candidate: tsk678CandidateHead, tree: strings.Repeat("f", 40), parents: []string{tsk678MainBase}, main: main, reachable: true},
		"wrong base":          {candidate: tsk678CandidateHead, tree: tsk678CandidateTree, parents: []string{strings.Repeat("e", 40)}, main: main, reachable: true},
		"merge commit":        {candidate: tsk678CandidateHead, tree: tsk678CandidateTree, parents: []string{tsk678MainBase, strings.Repeat("d", 40)}, main: main, reachable: true},
		"wrong main":          {candidate: tsk678CandidateHead, tree: tsk678CandidateTree, parents: []string{tsk678MainBase}, main: main, reachable: false},
		"invalid main":        {candidate: tsk678CandidateHead, tree: tsk678CandidateTree, parents: []string{tsk678MainBase}, main: "main", reachable: true},
	}
	for name, proof := range cases {
		t.Run(name, func(t *testing.T) {
			if err := validateTSK678SourceIdentity(proof.candidate, proof.tree, proof.parents, proof.main, proof.reachable); err == nil {
				t.Fatal("mismatched source proof was accepted")
			}
		})
	}
}

func TestTSK678RecordedMainRequiresCandidateAndStableDescent(t *testing.T) {
	recorded := strings.Repeat("b", 40)
	canonical := strings.Repeat("c", 40)
	for name, proof := range map[string]struct {
		recorded          string
		canonical         string
		candidateAncestor bool
		recordedAncestor  bool
		wantValid         bool
	}{
		"exact head":             {recorded: recorded, canonical: recorded, candidateAncestor: true, recordedAncestor: true, wantValid: true},
		"advanced descendant":    {recorded: recorded, canonical: canonical, candidateAncestor: true, recordedAncestor: true, wantValid: true},
		"candidate absent":       {recorded: recorded, canonical: canonical, candidateAncestor: false, recordedAncestor: true},
		"recorded head diverged": {recorded: recorded, canonical: canonical, candidateAncestor: true, recordedAncestor: false},
		"invalid recorded head":  {recorded: "main", canonical: canonical, candidateAncestor: true, recordedAncestor: true},
	} {
		t.Run(name, func(t *testing.T) {
			err := validateTSK678RecordedMainIdentity(proof.recorded, proof.canonical, proof.candidateAncestor, proof.recordedAncestor)
			if (err == nil) != proof.wantValid {
				t.Fatalf("recorded main proof err=%v", err)
			}
		})
	}
}

func TestTSK678ExecutionAcceptsOnlyBlockedRevisionTwoWithoutNormalReceipts(t *testing.T) {
	task, state, phases := tsk678TestExecution()
	journals := tsk678TestJournalProof(t)
	if _, already, err := validateTSK678Execution(task, state, phases, false, journals); err != nil || already {
		t.Fatalf("exact blocked TSK677 state rejected: already=%v err=%v", already, err)
	}
	cases := map[string]func(*model.TaskAuthoring, *model.TaskExecutionState, *taskBootstrapPhases, *bool){
		"unrelated task": func(task *model.TaskAuthoring, state *model.TaskExecutionState, _ *taskBootstrapPhases, _ *bool) {
			task.ID = "GTW-TSK668"
			state.TaskID = "GTW-TSK668"
		},
		"wrong execution revision": func(_ *model.TaskAuthoring, state *model.TaskExecutionState, _ *taskBootstrapPhases, _ *bool) {
			state.ExecutionRevision++
		},
		"wrong base": func(_ *model.TaskAuthoring, state *model.TaskExecutionState, _ *taskBootstrapPhases, _ *bool) {
			state.BaseHead = strings.Repeat("a", 40)
		},
		"changed Task revision": func(task *model.TaskAuthoring, _ *model.TaskExecutionState, _ *taskBootstrapPhases, _ *bool) {
			task.Revision++
		},
		"review phase": func(_ *model.TaskAuthoring, _ *model.TaskExecutionState, phases *taskBootstrapPhases, _ *bool) {
			phases.code = append(phases.code, sqlitestore.TaskExecutionPhase{EventKind: "review"})
		},
		"test phase": func(_ *model.TaskAuthoring, _ *model.TaskExecutionState, phases *taskBootstrapPhases, _ *bool) {
			phases.tests = []sqlitestore.TaskExecutionPhase{{}}
		},
		"rebase phase": func(_ *model.TaskAuthoring, _ *model.TaskExecutionState, phases *taskBootstrapPhases, _ *bool) {
			phases.rebase = []sqlitestore.TaskExecutionPhase{{}}
		},
		"verification receipt": func(_ *model.TaskAuthoring, _ *model.TaskExecutionState, _ *taskBootstrapPhases, found *bool) {
			*found = true
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			caseTask, caseState, casePhases := tsk678TestExecution()
			verified := false
			mutate(&caseTask, &caseState, &casePhases, &verified)
			if _, _, err := validateTSK678Execution(caseTask, caseState, casePhases, verified, journals); err == nil {
				t.Fatal("near-miss execution was accepted")
			}
		})
	}
}

func TestTSK678IntegratedReplayRequiresItsOwnBootstrapEvidence(t *testing.T) {
	task, state, phases := tsk678TestExecution()
	journals := tsk678TestJournalProof(t)
	state.Status = model.TaskExecutionIntegrated
	state.ExecutionRevision++
	state.UpdatedAt = state.UpdatedAt.Add(time.Minute)
	canonicalMain := strings.Repeat("c", 40)
	comment, err := tsk678PhaseComment(task, journals, canonicalMain)
	if err != nil {
		t.Fatal(err)
	}
	phases.integration = []sqlitestore.TaskExecutionPhase{{
		TaskID: tsk678TaskID, ProjectID: config.GTWProjectID, ExecutionRevision: state.ExecutionRevision,
		Stage: "integration", Status: model.TaskExecutionIntegrated, Head: tsk678CandidateHead,
		Branch: state.Branch, TaskRevisionSHA256: state.TaskRevisionSHA256,
		EventKind: "integration", Decision: "accept", Comment: comment, CreatedAt: state.UpdatedAt,
	}}
	evidence, already, err := validateTSK678Execution(task, state, phases, false, journals)
	if err != nil || !already || evidence.CanonicalMainHead != canonicalMain {
		t.Fatalf("exact bootstrap replay evidence=%#v already=%v err=%v", evidence, already, err)
	}
	phases.integration[0].Comment = "historical-integration:{}"
	if _, _, err := validateTSK678Execution(task, state, phases, false, journals); err == nil {
		t.Fatal("normal historical integration evidence was accepted as bootstrap reconciliation")
	}
}

func tsk678TestJournalEntries(t *testing.T) (model.JournalEntry, model.JournalEntry, model.JournalEntry) {
	t.Helper()
	plannerAuth := taskBootstrapPlannerJournalData{
		Summary: "Planner authorizes one bounded transition-bootstrap landing for GTW-TSK677 because the deferred v2 ProjectConfiguration state deadlocks every hook-gated Task submit/verify/integrate transition needed to land the code that retires the final stale project.",
		Decisions: []string{
			"Use a one-time transition bootstrap for exact candidate b407c9e4 based on exact main e54c87ff only if main has not moved and the worktree is clean.",
			"Lead must independently review the exact candidate and run the full required read-only project verification/gate evidence outside the frozen Task hook path before landing it.",
			"If and only if review+verification pass, Lead may fast-forward canonical main to exact b407c9e4 with no source edits and activate that exact main through the owner-authorized debug/recovery activation path.",
			"After activation, retire reposuite-mcp via debug/project-retire, run debug/project-configuration-migrate, and verify deferred migration converges to normal ready state.",
			"After migration unfreezes the normal lifecycle, reconcile TSK677 as an already-landed historical/bootstrap integration using exact candidate/base/integration identity and durable journal evidence rather than fabricating submit/review receipts.",
			"This exception is transition-only, does not weaken the normal lifecycle, and must not appear in the final MIL1 clean-room success path.",
		},
		Commitments: []string{
			"No direct Shared/Hub/SQLite mutation.",
			"No edits while manually landing b407c9e4; any required source change cancels this authorization and returns to Planner.",
			"No second arbitrary Task may use this bootstrap exception without a new explicit Planner decision.",
			"Preserve exact before/after source identity and Gate 1-20 evidence for the bootstrap landing.",
		},
		Facts: []string{
			"TSK677 Worker candidate is b407c9e4 on WT-TSK677-e54c87ff.",
			"task/submit-code is blocked because pre_task_submit strict-reads still-v2 ProjectConfiguration.",
			"task/test and task/integrate are blocked by the same pre-hook config read.",
			"ProjectConfiguration migration cannot finish until reposuite-mcp is retired, and reposuite retirement needs the TSK677 legacy-epoch exception.",
		},
		Assumptions: []string{
			"Canonical main remains exactly e54c87ff when Lead begins the bootstrap landing; if not, Lead must stop.",
			"b407c9e4 contains only the TSK677 implementation already reported and no unrelated changes.",
		},
		Blockers:   []string{},
		Unresolved: []string{},
		NextActions: []string{
			"Lead reviews and verifies exact b407c9e4 outside the frozen lifecycle.",
			"If clean and main is still e54c87ff, Lead fast-forwards main to b407c9e4 and debug-activates it.",
			"Lead retires reposuite-mcp and runs debug/project-configuration-migrate.",
			"Lead reports exact final runtime/source/migration state and historical reconciliation identifiers.",
		},
		References: []string{"GTW-TSK677", "GTW-JRN7", "b407c9e4", "e54c87ff"},
	}
	plannerFollowup := taskBootstrapPlannerJournalData{
		Summary: "ADR145 transition bootstrap completed successfully through TSK677 candidate b407c9e4: runtime is live, reposuite-mcp retired, v3 ProjectConfiguration migration completed. Two remaining transition defects are isolated: poisoned TRK1 outbox starvation (owned by TSK668) and historical TSK677 reconciliation relying on retired operator-journal evidence.",
		Decisions: []string{
			"Prioritize GTW-TSK668 next because the poisoned GTW-TRK1 rev8 outbox retry loop is structurally starving Hub reads and preventing HUB_SYNC_READY convergence.",
			"Do not resurrect the retired operator-journal writer merely to reconcile TSK677.",
			"Add a transition-only debug reconciliation action for exact already-landed bootstrap Tasks, using current Planner journal evidence plus exact immutable source identities rather than fabricating submit/review receipts.",
			"The debug reconciliation must be narrowly constrained to an already-landed candidate explicitly authorized by Planner, verified by exact base/candidate/current-main identity, and may only terminalize the Task execution as integrated without rewriting source or inventing prior lifecycle phases.",
			"Record debug/activate sticky failed-receipt behavior as a separate reliability defect; it is not a blocker for the current ADR145 cutover.",
		},
		Commitments: []string{
			"Final MIL1 clean-room success path will use the normal lifecycle and will not depend on bootstrap reconciliation.",
			"Do not reintroduce OperatorJournalEvent as a live authority merely for legacy compatibility.",
			"No direct Hub/Shared/SQLite surgery for TSK677 reconciliation.",
		},
		Facts: []string{
			"Runtime is serving exact source b407c9e4 and ProjectConfiguration v3 migration completed.",
			"reposuite-mcp retirement succeeded with 13 legacy epochs recorded in durable evidence.",
			"TSK677 remains dispatched because historical task/integrate requires an OperatorJournalEvent family with no live writer.",
			"GTW-TRK1 rev8 poison outbox retry is structurally starving Hub read lock acquisition and preventing HUB_SYNC_READY.",
		},
		Assumptions: []string{"TSK668 can resolve the existing GTW-TRK1 rev8 same-revision conflict and drain the poison outbox without new owner semantics."},
		Blockers:    []string{},
		Unresolved:  []string{},
		NextActions: []string{"Execute/integrate GTW-TSK668.", "Implement a transition-only debug bootstrap reconciliation action for TSK677.", "After both are complete, verify HUB_SYNC_READY and close TSK677 honestly."},
		References:  []string{"GTW-TSK668", "GTW-TSK677", "GTW-JRN8", "b407c9e4"},
	}
	leadReview := taskBootstrapLeadJournalData{
		Problem: "The GTW-TSK677 bootstrap landing required independent Lead review and full Gate verification of candidate b407c9e4 outside the frozen Task lifecycle (per GTW-JRN8); this durable record captures that evidence post-facto because no lifecycle action could record it during the freeze.",
		Evidence: []string{
			"Reviewed diff e54c87ff..b407c9e4 read-only: 9 files +426/-27; exact JRN7 legacy-epoch predicate (emitted_at set, hook_outcome/operation_id/session_id/hook_completed_at all empty); set-exact admission requiring count+digest match; no row mutation; ordinary retirement gate unchanged; gpt-tunnel-gateway fenced.",
			"Gate format: 'go run ./cmd/gofmt-struct --check .' — PASS on b407c9e4 (exit 0).",
			"Gate static_check: 'python3 scripts/static-check.py' — PASS (STATIC_CHECK_OK).",
			"Gate full_test: './scripts/test-full.sh' — PASS, all packages including sqlitestore gate inventories (105.8s).",
			"candidate_head=b407c9e4560c94ec1871080bced9f7c7c46587b0; candidate_tree=4a9b2ac73334f47e3586a10b17e39b0b5e129084; main_base=e54c87ffb56bb8a49c9c5da521f0e2bdfba29331 (exact parent); integration_head=b407c9e4 (ff-only, no merge commit).",
			"Preconditions verified pre-landing: canonical main exactly e54c87ff; worktree WT-TSK677-e54c87ff clean; candidate exact b407c9e4.",
			"Activation via install-and-restart-gateway: PID 957910, exact_source_match, running-exe sha256 d50180241ee8ba737a87caa9e4b1f79153a0e06785d8a979a51ce154cee5b813 == installed, tunnel PID 712 preserved.",
			"References: GTW-TSK677, GTW-JRN8, GTW-JRN9.",
		},
		Impact:              "Preserves truthful evidence that the bootstrap landing was independently reviewed and verified before the ff-only merge, without fabricating submit/review/test lifecycle receipts; supports the TSK678 transition-only reconciliation of GTW-TSK677 as already-landed.",
		ProposedImprovement: "TSK678's debug reconciliation action should validate this durable Lead record (or equivalent) as the review/gate evidence class for bootstrap candidates.",
	}
	authorityEntry := tsk678MakeJournalEntry(t, "GTW-JRN8", 8, model.JournalStreamPlannerNotes, "planner", "planner-actor", "planner-session", plannerAuth, time.Date(2026, 9, 30, 4, 43, 35, 0, time.UTC))
	followupEntry := tsk678MakeJournalEntry(t, "GTW-JRN9", 9, model.JournalStreamPlannerNotes, "planner", "planner-actor", "planner-session", plannerFollowup, time.Date(2026, 9, 30, 5, 31, 49, 0, time.UTC))
	leadEntry := tsk678MakeJournalEntry(t, "GTW-JRN10", 10, model.JournalStreamLeadFriction, "lead", "lead-actor", "lead-session", leadReview, time.Date(2026, 9, 30, 5, 49, 58, 0, time.UTC))
	return authorityEntry, followupEntry, leadEntry
}

func tsk678MakeJournalEntry(t *testing.T, id string, sequence uint64, stream model.JournalStream, role, actor, session string, data any, created time.Time) model.JournalEntry {
	t.Helper()
	raw := tsk678MarshalJournalData(t, data)
	entry := model.JournalEntry{
		SchemaVersion: model.SchemaVersion, ID: id, ProjectID: config.GTWProjectID,
		Status: model.JournalStatusPublished, Stream: stream, Data: raw,
		Actor: actor, Role: role, SessionID: session, Sequence: sequence, CreatedAt: created,
	}
	if err := model.ValidateJournalEntry(entry); err != nil {
		t.Fatalf("invalid TSK678 journal fixture %s: %v", id, err)
	}
	return entry
}

func tsk678MarshalJournalData(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func tsk678TestJournalProof(t *testing.T) taskBootstrapJournalEvidence {
	t.Helper()
	authority, followup, review := tsk678TestJournalEntries(t)
	proof, err := validateTSK678JournalEvidence(authority, followup, review, config.GTWProjectID)
	if err != nil {
		t.Fatal(err)
	}
	return proof
}

func tsk678TestExecution() (model.TaskAuthoring, model.TaskExecutionState, taskBootstrapPhases) {
	hash := strings.Repeat("a", 64)
	updated := time.Date(2026, 9, 30, 5, 50, 0, 0, time.UTC)
	task := model.TaskAuthoring{ID: tsk678TaskID, ProjectID: config.GTWProjectID, Revision: 1, RevisionSHA256: hash}
	state := model.TaskExecutionState{
		TaskID: tsk678TaskID, ProjectID: config.GTWProjectID,
		TaskRevision: 1, TaskRevisionSHA256: hash, Status: model.TaskExecutionBlocked, Stage: "code",
		Worktree: "WT-TSK677-e54c87ff", BaseHead: tsk678MainBase, Head: tsk678MainBase,
		Branch: "task/GTW-TSK677-bootstrap", Agent: config.GTWWorkerAgentID,
		ExecutionRevision: tsk678ExecutionRevision, UpdatedAt: updated,
	}
	block := sqlitestore.TaskExecutionPhase{
		TaskID: tsk678TaskID, ProjectID: config.GTWProjectID, ExecutionRevision: tsk678ExecutionRevision,
		Stage: "code", Status: model.TaskExecutionBlocked, Head: state.Head, Branch: state.Branch,
		TaskRevisionSHA256: hash, EventKind: "block", Decision: model.TaskExecutionDispatched,
		Comment: "pre-transition Hook configuration unavailable", CreatedAt: updated,
	}
	return task, state, taskBootstrapPhases{code: []sqlitestore.TaskExecutionPhase{block}}
}
