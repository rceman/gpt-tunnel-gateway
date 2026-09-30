package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

const (
	tsk678TaskID               = "GTW-TSK677"
	tsk678ExecutionRevision    = 2
	tsk678BootstrapPhasePrefix = "task-bootstrap-reconciliation:"
	tsk678MainBase             = "e54c87ffb56bb8a49c9c5da521f0e2bdfba29331"
	tsk678CandidateHead        = "b407c9e4560c94ec1871080bced9f7c7c46587b0"
	tsk678CandidateTree        = "4a9b2ac73334f47e3586a10b17e39b0b5e129084"
)

type DebugTaskBootstrapReconciliationResult struct {
	Key               string   `json:"key"`
	Status            string   `json:"status"`
	ExecutionRevision int      `json:"execution_revision"`
	IntegrationHead   string   `json:"integration_head"`
	Evidence          []string `json:"evidence"`
	AlreadyReconciled bool     `json:"already_reconciled"`
}

type taskBootstrapPlannerJournalData struct {
	Summary     string   `json:"summary"`
	Decisions   []string `json:"decisions"`
	Commitments []string `json:"commitments"`
	Facts       []string `json:"facts"`
	Assumptions []string `json:"assumptions"`
	Blockers    []string `json:"blockers"`
	Unresolved  []string `json:"unresolved"`
	NextActions []string `json:"next_actions"`
	References  []string `json:"references"`
}

type taskBootstrapLeadJournalData struct {
	Problem             string   `json:"problem"`
	Evidence            []string `json:"evidence"`
	Impact              string   `json:"impact"`
	ProposedImprovement string   `json:"proposed_improvement"`
}

type taskBootstrapJournalEvidence struct {
	PlannerEntries []string
	PlannerDigests []string
	LeadEntry      string
	LeadDigest     string
}

type taskBootstrapPhaseEvidence struct {
	SchemaVersion                 int      `json:"schema_version"`
	Kind                          string   `json:"kind"`
	TaskID                        string   `json:"task"`
	TaskRevision                  int      `json:"task_revision"`
	TaskRevisionSHA256            string   `json:"task_revision_sha256"`
	PreviousStatus                string   `json:"previous_status"`
	PreviousExecutionRevision     int      `json:"previous_execution_revision"`
	PlannerAuthorization          []string `json:"planner_authorization"`
	PlannerEvidenceSHA256         []string `json:"planner_evidence_sha256"`
	LeadReviewEvidence            string   `json:"lead_review_evidence"`
	LeadEvidenceSHA256            string   `json:"lead_evidence_sha256"`
	MainBase                      string   `json:"main_base"`
	CandidateHead                 string   `json:"candidate_head"`
	CandidateTree                 string   `json:"candidate_tree"`
	IntegrationHead               string   `json:"integration_head"`
	CanonicalMainHead             string   `json:"canonical_main_head"`
	NormalSubmitPhaseCreated      bool     `json:"normal_submit_phase_created"`
	NormalReviewPhaseCreated      bool     `json:"normal_review_phase_created"`
	NormalVerificationReceiptMade bool     `json:"normal_verification_receipt_created"`
}

type taskBootstrapPhases struct {
	code        []sqlitestore.TaskExecutionPhase
	tests       []sqlitestore.TaskExecutionPhase
	rebase      []sqlitestore.TaskExecutionPhase
	integration []sqlitestore.TaskExecutionPhase
}

func (s *Service) DebugReconcileBootstrappedTask(ctx context.Context, key string) (DebugTaskBootstrapReconciliationResult, error) {
	if s == nil || s.Durability == nil || !s.Config.Debug.Enabled {
		return DebugTaskBootstrapReconciliationResult{}, fmt.Errorf("debug bootstrap reconciliation is unavailable")
	}
	if key != tsk678TaskID {
		return DebugTaskBootstrapReconciliationResult{}, fmt.Errorf("bootstrap reconciliation only accepts %s", tsk678TaskID)
	}
	session, err := s.journalSession(ctx)
	if err != nil {
		return DebugTaskBootstrapReconciliationResult{}, err
	}
	if session.ProjectID != config.GTWProjectID || (session.Role != "planner" && session.Role != "lead") {
		return DebugTaskBootstrapReconciliationResult{}, fmt.Errorf("bootstrap reconciliation requires a Planner or Lead Session for %s", config.GTWProjectID)
	}

	s.taskExecutionMu.Lock()
	defer s.taskExecutionMu.Unlock()

	task, err := s.readSharedTask(ctx, config.GTWProjectID, tsk678TaskID)
	if err != nil {
		return DebugTaskBootstrapReconciliationResult{}, err
	}
	state, found, err := s.Durability.ReadTaskExecutionState(ctx, config.GTWProjectID, tsk678TaskID)
	if err != nil {
		return DebugTaskBootstrapReconciliationResult{}, err
	}
	if !found {
		return DebugTaskBootstrapReconciliationResult{}, fmt.Errorf("TSK677 has no durable execution state")
	}
	journals, err := s.readTSK678JournalEvidence(ctx)
	if err != nil {
		return DebugTaskBootstrapReconciliationResult{}, err
	}
	phases, err := s.readTSK678Phases(ctx)
	if err != nil {
		return DebugTaskBootstrapReconciliationResult{}, err
	}
	_, verificationFound, err := s.Durability.ReadLatestTaskExecutionVerification(ctx, config.GTWProjectID, tsk678TaskID)
	if err != nil {
		return DebugTaskBootstrapReconciliationResult{}, err
	}
	existing, alreadyReconciled, err := validateTSK678Execution(task, state, phases, verificationFound, journals)
	if err != nil {
		return DebugTaskBootstrapReconciliationResult{}, err
	}
	project, err := s.EffectiveProjectConfig(config.GTWProjectID)
	if err != nil {
		return DebugTaskBootstrapReconciliationResult{}, err
	}
	if strings.TrimPrefix(project.DefaultBranch, "refs/heads/") != "main" {
		return DebugTaskBootstrapReconciliationResult{}, fmt.Errorf("TSK677 bootstrap evidence is bound to canonical main")
	}
	_, initialSnapshot, canonicalMain, err := s.proveTSK678BootstrapSource(ctx, project, state)
	if err != nil {
		return DebugTaskBootstrapReconciliationResult{}, err
	}
	if alreadyReconciled {
		if err := s.proveTSK678RecordedMain(ctx, project, existing.CanonicalMainHead, canonicalMain); err != nil {
			return DebugTaskBootstrapReconciliationResult{}, err
		}
		return tsk678PublicResult(state, true), nil
	}

	comment, err := tsk678PhaseComment(task, journals, canonicalMain)
	if err != nil {
		return DebugTaskBootstrapReconciliationResult{}, err
	}
	freshJournals, err := s.readTSK678JournalEvidence(ctx)
	if err != nil || !sameTSK678JournalEvidence(freshJournals, journals) {
		if err != nil {
			return DebugTaskBootstrapReconciliationResult{}, err
		}
		return DebugTaskBootstrapReconciliationResult{}, fmt.Errorf("TSK677 bootstrap journal evidence changed during admission")
	}
	currentTask, err := s.readSharedTask(ctx, config.GTWProjectID, tsk678TaskID)
	if err != nil {
		return DebugTaskBootstrapReconciliationResult{}, err
	}
	if currentTask.Revision != task.Revision || currentTask.RevisionSHA256 != task.RevisionSHA256 {
		return DebugTaskBootstrapReconciliationResult{}, fmt.Errorf("TSK677 authoring revision changed during bootstrap admission")
	}
	_, finalSnapshot, err := s.taskExecutionHistoricalBootstrapLaneSnapshot(ctx, project, config.GTWProjectID, state, tsk678HistoricalInput())
	if err != nil {
		return DebugTaskBootstrapReconciliationResult{}, err
	}
	if !sameVerificationSnapshot(initialSnapshot, finalSnapshot) {
		return DebugTaskBootstrapReconciliationResult{}, fmt.Errorf("TSK677 candidate lane changed during bootstrap reconciliation")
	}
	finalMain, err := s.Git.RemoteBranchHead(ctx, project, "main")
	if err != nil {
		return DebugTaskBootstrapReconciliationResult{}, err
	}
	if finalMain != canonicalMain {
		return DebugTaskBootstrapReconciliationResult{}, fmt.Errorf("canonical main moved during bootstrap reconciliation; retry is required")
	}
	if err := ctx.Err(); err != nil {
		return DebugTaskBootstrapReconciliationResult{}, err
	}

	state.Status = model.TaskExecutionIntegrated
	state.ExecutionRevision++
	state.UpdatedAt = s.durableNow()
	phase := sqlitestore.TaskExecutionPhase{
		TaskID: state.TaskID, ProjectID: state.ProjectID, ExecutionRevision: state.ExecutionRevision,
		Stage: "integration", Status: model.TaskExecutionIntegrated, Head: tsk678CandidateHead,
		Branch: state.Branch, TaskRevisionSHA256: state.TaskRevisionSHA256,
		EventKind: "integration", Decision: "accept", Comment: comment, CreatedAt: state.UpdatedAt,
	}
	if err := s.Durability.TransitionTaskExecutionState(ctx, state, tsk678ExecutionRevision, phase); err != nil {
		return DebugTaskBootstrapReconciliationResult{}, fmt.Errorf("TSK677 bootstrap reconciliation state remains pending; retry is required: %w", err)
	}
	return tsk678PublicResult(state, false), nil
}

func (s *Service) readTSK678JournalEvidence(ctx context.Context) (taskBootstrapJournalEvidence, error) {
	entries := make([]model.JournalEntry, 3)
	for index, key := range []string{"GTW-JRN8", "GTW-JRN9", "GTW-JRN10"} {
		entry, err := s.JournalRead(ctx, JournalReadInput{Key: key})
		if err != nil {
			return taskBootstrapJournalEvidence{}, fmt.Errorf("read bootstrap authority %s: %w", key, err)
		}
		entries[index] = entry
	}
	return validateTSK678JournalEvidence(entries[0], entries[1], entries[2], config.GTWProjectID)
}

func validateTSK678JournalEntry(entry model.JournalEntry, key string, sequence uint64, stream model.JournalStream, role, projectID string) error {
	if err := model.ValidateJournalEntry(entry); err != nil {
		return err
	}
	if entry.ID != key || entry.Sequence != sequence || entry.Stream != stream || entry.Role != role || entry.ProjectID != projectID {
		return fmt.Errorf("journal %s does not match the required %s authority", key, role)
	}
	return nil
}

func validateTSK678JournalEvidence(plannerAuth, plannerContext, leadReview model.JournalEntry, projectID string) (taskBootstrapJournalEvidence, error) {
	if err := validateTSK678JournalEntry(plannerAuth, "GTW-JRN8", 8, model.JournalStreamPlannerNotes, "planner", projectID); err != nil {
		return taskBootstrapJournalEvidence{}, err
	}
	if err := validateTSK678JournalEntry(plannerContext, "GTW-JRN9", 9, model.JournalStreamPlannerNotes, "planner", projectID); err != nil {
		return taskBootstrapJournalEvidence{}, err
	}
	if err := validateTSK678JournalEntry(leadReview, "GTW-JRN10", 10, model.JournalStreamLeadFriction, "lead", projectID); err != nil {
		return taskBootstrapJournalEvidence{}, err
	}
	if plannerAuth.Actor != plannerContext.Actor || plannerAuth.SessionID != plannerContext.SessionID || leadReview.Actor == plannerAuth.Actor || leadReview.SessionID == plannerAuth.SessionID || !plannerAuth.CreatedAt.Before(plannerContext.CreatedAt) || !plannerContext.CreatedAt.Before(leadReview.CreatedAt) {
		return taskBootstrapJournalEvidence{}, fmt.Errorf("TSK677 bootstrap journal provenance is inconsistent")
	}
	var first taskBootstrapPlannerJournalData
	var second taskBootstrapPlannerJournalData
	var review taskBootstrapLeadJournalData
	if err := decodeStrict(plannerAuth.Data, &first); err != nil {
		return taskBootstrapJournalEvidence{}, err
	}
	if err := decodeStrict(plannerContext.Data, &second); err != nil {
		return taskBootstrapJournalEvidence{}, err
	}
	if err := decodeStrict(leadReview.Data, &review); err != nil {
		return taskBootstrapJournalEvidence{}, err
	}
	if !tsk678PlannerAuthorizationValid(first) || !tsk678PlannerFollowupValid(second) || !tsk678LeadReviewValid(review) {
		return taskBootstrapJournalEvidence{}, fmt.Errorf("TSK677 bootstrap journal evidence does not authorize the exact candidate and review")
	}
	firstDigest, err := tsk678JournalDigest(plannerAuth)
	if err != nil {
		return taskBootstrapJournalEvidence{}, err
	}
	secondDigest, err := tsk678JournalDigest(plannerContext)
	if err != nil {
		return taskBootstrapJournalEvidence{}, err
	}
	leadDigest, err := tsk678JournalDigest(leadReview)
	if err != nil {
		return taskBootstrapJournalEvidence{}, err
	}
	return taskBootstrapJournalEvidence{
		PlannerEntries: []string{plannerAuth.ID, plannerContext.ID},
		PlannerDigests: []string{firstDigest, secondDigest},
		LeadEntry:      leadReview.ID,
		LeadDigest:     leadDigest,
	}, nil
}

func tsk678PlannerAuthorizationValid(data taskBootstrapPlannerJournalData) bool {
	want := taskBootstrapPlannerJournalData{
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
	return sameTSK678PlannerJournalData(data, want)
}

func tsk678PlannerFollowupValid(data taskBootstrapPlannerJournalData) bool {
	want := taskBootstrapPlannerJournalData{
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
		NextActions: []string{
			"Execute/integrate GTW-TSK668.",
			"Implement a transition-only debug bootstrap reconciliation action for TSK677.",
			"After both are complete, verify HUB_SYNC_READY and close TSK677 honestly.",
		},
		References: []string{"GTW-TSK668", "GTW-TSK677", "GTW-JRN8", "b407c9e4"},
	}
	return sameTSK678PlannerJournalData(data, want)
}

func sameTSK678PlannerJournalData(left, right taskBootstrapPlannerJournalData) bool {
	return left.Summary == right.Summary && slices.Equal(left.Decisions, right.Decisions) && slices.Equal(left.Commitments, right.Commitments) && slices.Equal(left.Facts, right.Facts) && slices.Equal(left.Assumptions, right.Assumptions) && slices.Equal(left.Blockers, right.Blockers) && slices.Equal(left.Unresolved, right.Unresolved) && slices.Equal(left.NextActions, right.NextActions) && slices.Equal(left.References, right.References)
}

func tsk678LeadReviewValid(data taskBootstrapLeadJournalData) bool {
	wantEvidence := []string{
		"Reviewed diff e54c87ff..b407c9e4 read-only: 9 files +426/-27; exact JRN7 legacy-epoch predicate (emitted_at set, hook_outcome/operation_id/session_id/hook_completed_at all empty); set-exact admission requiring count+digest match; no row mutation; ordinary retirement gate unchanged; gpt-tunnel-gateway fenced.",
		"Gate format: 'go run ./cmd/gofmt-struct --check .' — PASS on b407c9e4 (exit 0).",
		"Gate static_check: 'python3 scripts/static-check.py' — PASS (STATIC_CHECK_OK).",
		"Gate full_test: './scripts/test-full.sh' — PASS, all packages including sqlitestore gate inventories (105.8s).",
		"candidate_head=b407c9e4560c94ec1871080bced9f7c7c46587b0; candidate_tree=4a9b2ac73334f47e3586a10b17e39b0b5e129084; main_base=e54c87ffb56bb8a49c9c5da521f0e2bdfba29331 (exact parent); integration_head=b407c9e4 (ff-only, no merge commit).",
		"Preconditions verified pre-landing: canonical main exactly e54c87ff; worktree WT-TSK677-e54c87ff clean; candidate exact b407c9e4.",
		"Activation via install-and-restart-gateway: PID 957910, exact_source_match, running-exe sha256 d50180241ee8ba737a87caa9e4b1f79153a0e06785d8a979a51ce154cee5b813 == installed, tunnel PID 712 preserved.",
		"References: GTW-TSK677, GTW-JRN8, GTW-JRN9.",
	}
	return data.Problem == "The GTW-TSK677 bootstrap landing required independent Lead review and full Gate verification of candidate b407c9e4 outside the frozen Task lifecycle (per GTW-JRN8); this durable record captures that evidence post-facto because no lifecycle action could record it during the freeze." &&
		data.Impact == "Preserves truthful evidence that the bootstrap landing was independently reviewed and verified before the ff-only merge, without fabricating submit/review/test lifecycle receipts; supports the TSK678 transition-only reconciliation of GTW-TSK677 as already-landed." &&
		data.ProposedImprovement == "TSK678's debug reconciliation action should validate this durable Lead record (or equivalent) as the review/gate evidence class for bootstrap candidates." &&
		slices.Equal(data.Evidence, wantEvidence)
}

func tsk678JournalDigest(entry model.JournalEntry) (string, error) {
	raw, err := json.Marshal(entry)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

func sameTSK678JournalEvidence(left, right taskBootstrapJournalEvidence) bool {
	return slices.Equal(left.PlannerEntries, right.PlannerEntries) && slices.Equal(left.PlannerDigests, right.PlannerDigests) && left.LeadEntry == right.LeadEntry && left.LeadDigest == right.LeadDigest
}

func (s *Service) readTSK678Phases(ctx context.Context) (taskBootstrapPhases, error) {
	var result taskBootstrapPhases
	for _, item := range []struct {
		stage  string
		target *[]sqlitestore.TaskExecutionPhase
	}{
		{stage: "code", target: &result.code},
		{stage: "tests", target: &result.tests},
		{stage: "rebase", target: &result.rebase},
		{stage: "integration", target: &result.integration},
	} {
		phases, err := s.Durability.ReadTaskExecutionPhases(ctx, config.GTWProjectID, tsk678TaskID, item.stage)
		if err != nil {
			return taskBootstrapPhases{}, err
		}
		*item.target = phases
	}
	return result, nil
}

func validateTSK678Execution(task model.TaskAuthoring, state model.TaskExecutionState, phases taskBootstrapPhases, verificationFound bool, journals taskBootstrapJournalEvidence) (taskBootstrapPhaseEvidence, bool, error) {
	if err := model.ValidateTaskExecutionState(state); err != nil {
		return taskBootstrapPhaseEvidence{}, false, err
	}
	if task.ID != tsk678TaskID || task.ProjectID != config.GTWProjectID || task.Revision != 1 || state.TaskID != tsk678TaskID || state.ProjectID != config.GTWProjectID || state.TaskRevision != 1 || state.TaskRevisionSHA256 != task.RevisionSHA256 || state.Stage != "code" || state.Worktree != "WT-TSK677-e54c87ff" || state.Agent != config.GTWWorkerAgentID || state.BaseHead != tsk678MainBase || state.Head != tsk678MainBase {
		return taskBootstrapPhaseEvidence{}, false, fmt.Errorf("TSK677 execution does not match the authorized bootstrap identity")
	}
	if verificationFound || len(phases.code) != 1 || len(phases.tests) != 0 || len(phases.rebase) != 0 {
		return taskBootstrapPhaseEvidence{}, false, fmt.Errorf("TSK677 normal submit, review, or verification evidence is present or ambiguous")
	}
	block := phases.code[0]
	if block.ProjectID != config.GTWProjectID || block.TaskID != tsk678TaskID || block.ExecutionRevision != tsk678ExecutionRevision || block.Stage != "code" || block.Status != model.TaskExecutionBlocked || block.Head != state.Head || block.Branch != state.Branch || block.TaskRevisionSHA256 != state.TaskRevisionSHA256 || block.EventKind != "block" || block.Decision != model.TaskExecutionDispatched || strings.TrimSpace(block.Comment) == "" {
		return taskBootstrapPhaseEvidence{}, false, fmt.Errorf("TSK677 blocked execution evidence does not match revision 2")
	}
	switch state.Status {
	case model.TaskExecutionBlocked:
		if state.ExecutionRevision != tsk678ExecutionRevision || len(phases.integration) != 0 || !block.CreatedAt.Equal(state.UpdatedAt) {
			return taskBootstrapPhaseEvidence{}, false, fmt.Errorf("TSK677 is not at the authorized blocked revision")
		}
		return taskBootstrapPhaseEvidence{}, false, nil
	case model.TaskExecutionIntegrated:
		if state.ExecutionRevision != tsk678ExecutionRevision+1 || len(phases.integration) != 1 {
			return taskBootstrapPhaseEvidence{}, false, fmt.Errorf("TSK677 integrated state lacks exact bootstrap reconciliation evidence")
		}
		phase := phases.integration[0]
		if phase.ProjectID != config.GTWProjectID || phase.TaskID != tsk678TaskID || phase.ExecutionRevision != state.ExecutionRevision || phase.Stage != "integration" || phase.Status != model.TaskExecutionIntegrated || phase.Head != tsk678CandidateHead || phase.Branch != state.Branch || phase.TaskRevisionSHA256 != state.TaskRevisionSHA256 || phase.EventKind != "integration" || phase.Decision != "accept" || !phase.CreatedAt.Equal(state.UpdatedAt) || !strings.HasPrefix(phase.Comment, tsk678BootstrapPhasePrefix) {
			return taskBootstrapPhaseEvidence{}, false, fmt.Errorf("TSK677 integration phase is not the exact bootstrap reconciliation")
		}
		var evidence taskBootstrapPhaseEvidence
		encoded := strings.TrimPrefix(phase.Comment, tsk678BootstrapPhasePrefix)
		if err := decodeStrict([]byte(encoded), &evidence); err != nil {
			return taskBootstrapPhaseEvidence{}, false, err
		}
		canonical, err := json.Marshal(evidence)
		if err != nil || string(canonical) != encoded {
			return taskBootstrapPhaseEvidence{}, false, fmt.Errorf("TSK677 bootstrap phase envelope is not canonical")
		}
		if !tsk678PhaseEvidenceValid(evidence, task, state, journals) {
			return taskBootstrapPhaseEvidence{}, false, fmt.Errorf("TSK677 bootstrap reconciliation receipt conflicts with current authority")
		}
		return evidence, true, nil
	default:
		return taskBootstrapPhaseEvidence{}, false, fmt.Errorf("TSK677 execution is not at the authorized bootstrap boundary")
	}
}

func tsk678PhaseEvidenceValid(evidence taskBootstrapPhaseEvidence, task model.TaskAuthoring, state model.TaskExecutionState, journals taskBootstrapJournalEvidence) bool {
	return evidence.SchemaVersion == 1 && evidence.Kind == "planner-authorized-bootstrap-reconciliation" && evidence.TaskID == tsk678TaskID && evidence.TaskRevision == task.Revision && evidence.TaskRevisionSHA256 == state.TaskRevisionSHA256 && evidence.PreviousStatus == model.TaskExecutionBlocked && evidence.PreviousExecutionRevision == tsk678ExecutionRevision && slices.Equal(evidence.PlannerAuthorization, journals.PlannerEntries) && slices.Equal(evidence.PlannerEvidenceSHA256, journals.PlannerDigests) && evidence.LeadReviewEvidence == journals.LeadEntry && evidence.LeadEvidenceSHA256 == journals.LeadDigest && evidence.MainBase == tsk678MainBase && evidence.CandidateHead == tsk678CandidateHead && evidence.CandidateTree == tsk678CandidateTree && evidence.IntegrationHead == tsk678CandidateHead && model.ValidateCommitSHA(evidence.CanonicalMainHead) == nil && !evidence.NormalSubmitPhaseCreated && !evidence.NormalReviewPhaseCreated && !evidence.NormalVerificationReceiptMade
}

func tsk678PhaseComment(task model.TaskAuthoring, journals taskBootstrapJournalEvidence, canonicalMain string) (string, error) {
	evidence := taskBootstrapPhaseEvidence{
		SchemaVersion:             1,
		Kind:                      "planner-authorized-bootstrap-reconciliation",
		TaskID:                    tsk678TaskID,
		TaskRevision:              task.Revision,
		TaskRevisionSHA256:        task.RevisionSHA256,
		PreviousStatus:            model.TaskExecutionBlocked,
		PreviousExecutionRevision: tsk678ExecutionRevision,
		PlannerAuthorization:      journals.PlannerEntries,
		PlannerEvidenceSHA256:     journals.PlannerDigests,
		LeadReviewEvidence:        journals.LeadEntry,
		LeadEvidenceSHA256:        journals.LeadDigest,
		MainBase:                  tsk678MainBase,
		CandidateHead:             tsk678CandidateHead,
		CandidateTree:             tsk678CandidateTree,
		IntegrationHead:           tsk678CandidateHead,
		CanonicalMainHead:         canonicalMain,
	}
	raw, err := json.Marshal(evidence)
	if err != nil {
		return "", err
	}
	return tsk678BootstrapPhasePrefix + string(raw), nil
}

func tsk678PublicResult(state model.TaskExecutionState, alreadyReconciled bool) DebugTaskBootstrapReconciliationResult {
	return DebugTaskBootstrapReconciliationResult{
		Key:               tsk678TaskID,
		Status:            model.TaskExecutionIntegrated,
		ExecutionRevision: state.ExecutionRevision,
		IntegrationHead:   tsk678CandidateHead[:8],
		Evidence:          []string{"GTW-JRN8", "GTW-JRN9", "GTW-JRN10"},
		AlreadyReconciled: alreadyReconciled,
	}
}

func tsk678HistoricalInput() *TaskExecutionHistoricalIntegrationInput {
	return &TaskExecutionHistoricalIntegrationInput{
		IntegrationHead: tsk678CandidateHead,
		Profile:         "bootstrap_full",
		Evidence:        "GTW-JRN10",
		CandidateHead:   tsk678CandidateHead,
		MainBase:        tsk678MainBase,
	}
}

func (s *Service) proveTSK678BootstrapSource(ctx context.Context, project config.ProjectConfig, state model.TaskExecutionState) (config.ProjectConfig, verificationGateSnapshot, string, error) {
	lane, snapshot, err := s.taskExecutionHistoricalBootstrapLaneSnapshot(ctx, project, config.GTWProjectID, state, tsk678HistoricalInput())
	if err != nil {
		return config.ProjectConfig{}, verificationGateSnapshot{}, "", err
	}
	if state.BaseHead != tsk678MainBase || snapshot.tree != tsk678CandidateTree {
		return config.ProjectConfig{}, verificationGateSnapshot{}, "", fmt.Errorf("TSK677 candidate lane does not match the authorized base and tree")
	}
	basedOn, err := s.Git.IsAncestor(ctx, lane.Root, tsk678MainBase, tsk678CandidateHead)
	if err != nil {
		return config.ProjectConfig{}, verificationGateSnapshot{}, "", err
	}
	if !basedOn {
		return config.ProjectConfig{}, verificationGateSnapshot{}, "", fmt.Errorf("TSK677 candidate is not based on the authorized main base")
	}
	canonicalMain, err := s.Git.RemoteBranchHead(ctx, project, "main")
	if err != nil {
		return config.ProjectConfig{}, verificationGateSnapshot{}, "", err
	}
	if err := s.Git.MaterializeRemoteBranchObjects(ctx, project, "main"); err != nil {
		return config.ProjectConfig{}, verificationGateSnapshot{}, "", err
	}
	tree, parents, err := s.Git.InspectTaskIntegrationCommit(ctx, project, tsk678CandidateHead)
	if err != nil {
		return config.ProjectConfig{}, verificationGateSnapshot{}, "", err
	}
	candidateOnMain := canonicalMain == tsk678CandidateHead
	if !candidateOnMain {
		candidateOnMain, err = s.Git.IsAncestor(ctx, project.Root, tsk678CandidateHead, canonicalMain)
		if err != nil {
			return config.ProjectConfig{}, verificationGateSnapshot{}, "", err
		}
	}
	if err := validateTSK678SourceIdentity(tsk678CandidateHead, tree, parents, canonicalMain, candidateOnMain); err != nil {
		return config.ProjectConfig{}, verificationGateSnapshot{}, "", err
	}
	finalMain, err := s.Git.RemoteBranchHead(ctx, project, "main")
	if err != nil {
		return config.ProjectConfig{}, verificationGateSnapshot{}, "", err
	}
	if finalMain != canonicalMain {
		return config.ProjectConfig{}, verificationGateSnapshot{}, "", fmt.Errorf("canonical main moved during bootstrap source proof; retry is required")
	}
	return lane, snapshot, canonicalMain, nil
}

func validateTSK678SourceIdentity(candidateHead, candidateTree string, parents []string, canonicalMain string, candidateOnMain bool) error {
	if candidateHead != tsk678CandidateHead || candidateTree != tsk678CandidateTree || len(parents) != 1 || parents[0] != tsk678MainBase || model.ValidateCommitSHA(canonicalMain) != nil || !candidateOnMain {
		return fmt.Errorf("TSK677 bootstrap source identity does not match the authorized candidate on canonical main")
	}
	return nil
}

func (s *Service) proveTSK678RecordedMain(ctx context.Context, project config.ProjectConfig, recordedMain, canonicalMain string) error {
	if model.ValidateCommitSHA(recordedMain) != nil || model.ValidateCommitSHA(canonicalMain) != nil {
		return fmt.Errorf("recorded bootstrap reconciliation head is invalid")
	}
	candidateAncestor, err := s.Git.IsAncestor(ctx, project.Root, tsk678CandidateHead, recordedMain)
	if err != nil {
		return err
	}
	recordedAncestor := recordedMain == canonicalMain
	if !recordedAncestor {
		recordedAncestor, err = s.Git.IsAncestor(ctx, project.Root, recordedMain, canonicalMain)
		if err != nil {
			return err
		}
	}
	if err := validateTSK678RecordedMainIdentity(recordedMain, canonicalMain, candidateAncestor, recordedAncestor); err != nil {
		return err
	}
	finalMain, err := s.Git.RemoteBranchHead(ctx, project, "main")
	if err != nil {
		return err
	}
	if finalMain != canonicalMain {
		return fmt.Errorf("canonical main moved during bootstrap replay proof; retry is required")
	}
	return nil
}

func validateTSK678RecordedMainIdentity(recordedMain, canonicalMain string, candidateAncestor, recordedAncestor bool) error {
	if model.ValidateCommitSHA(recordedMain) != nil || model.ValidateCommitSHA(canonicalMain) != nil {
		return fmt.Errorf("recorded bootstrap reconciliation head is invalid")
	}
	if !candidateAncestor {
		return fmt.Errorf("recorded bootstrap reconciliation head does not contain the authorized candidate")
	}
	if !recordedAncestor {
		return fmt.Errorf("canonical main no longer descends from the recorded bootstrap reconciliation head")
	}
	return nil
}

func sameVerificationSnapshot(left, right verificationGateSnapshot) bool {
	return left.head == right.head && left.branch == right.branch && left.clean == right.clean && left.porcelain == right.porcelain && left.tree == right.tree && left.contentID == right.contentID
}
