package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

const (
	tsk660TaskID                      = "GTW-TSK660"
	tsk660TaskRevision                = 6
	tsk660IntegratedRevision          = 9
	tsk660CompletedRevision           = 10
	tsk660ReconcileKind               = "planner-authorized-pre-execution-reconciliation"
	tsk660MainBase                    = "f09d0834d125362d63e1d12b361ad34c63e50d8d"
	tsk660ImplementationCommit        = "899abc90157ee5b4e8b6b27e4284e0c841ccbb17"
	tsk660ImplementationTree          = "e9073e7db4e6e8a876b1d88eed1ab1760b5ea4a0"
	tsk660FirstSubmissionHead         = "418e175fb42f5e2e9c07c161a6129f93acbed9c0"
	tsk660FinalSubmissionHead         = "8a7ec281b349599d26f803bb8b3f1bea557d0570"
	tsk660ExecutionBranch             = "task/GTW-TSK660-task-implement-milestone-roadmap-membership-and-"
	tsk660ExecutionWorktree           = "WT-TSK660-8a7ec281"
	tsk660PlannerJournalKey           = "GTW-JRN16"
	tsk660PlannerJournalSeq           = uint64(16)
	tsk660LeadJournalKey              = "GTW-JRN14"
	tsk660LeadJournalSeq              = uint64(14)
	tsk660LegacyVerificationOperation = "GTW-OPR3793"
	tsk660LegacyVerificationCandidate = tsk660FinalSubmissionHead
	tsk660LegacyVerificationReviewID  = int64(424)
	tsk660ReconcileReason             = "transition reconciliation of already-landed pre-execution TSK660 per GTW-JRN16; validates the existing rev9 integrated lifecycle and reconciles the authoring record"
)

type taskPreExecutionPhaseEvidence struct {
	SchemaVersion               int      `json:"schema_version"`
	Kind                        string   `json:"kind"`
	TaskID                      string   `json:"task"`
	TaskRevision                int      `json:"task_revision"`
	TaskRevisionSHA256          string   `json:"task_revision_sha256"`
	PlannerAuthorization        string   `json:"planner_authorization"`
	PlannerEvidenceSHA256       string   `json:"planner_evidence_sha256"`
	LeadReviewEvidence          string   `json:"lead_review_evidence"`
	LeadReviewSHA256            string   `json:"lead_review_sha256"`
	LeadGateEvidence            []string `json:"lead_gate_evidence"`
	LeadGateEvidenceSHA256      []string `json:"lead_gate_evidence_sha256"`
	ImplementationCommit        string   `json:"implementation_commit"`
	ImplementationTree          string   `json:"implementation_tree"`
	MainBase                    string   `json:"main_base"`
	CanonicalMainHead           string   `json:"canonical_main_head"`
	ExecutionRevision           int      `json:"execution_revision"`
	PhasesSHA256                string   `json:"phases_sha256"`
	LegacyVerificationOperation string   `json:"legacy_verification_operation"`
	LegacyVerificationSHA256    string   `json:"legacy_verification_sha256"`
	LegacyVerificationCandidate string   `json:"legacy_verification_candidate"`
	ExecutionStateMinted        bool     `json:"execution_state_minted"`
	PhaseMinted                 bool     `json:"phase_minted"`
	VerificationReceiptMinted   bool     `json:"verification_receipt_minted"`
}

type taskPreExecutionVerificationProof struct {
	OperationID   string
	CandidateHead string
	ReceiptSHA256 string
	CompletedAt   time.Time
}

type taskPreExecutionJournalEvidence struct {
	PlannerKey    string
	PlannerDigest string
	LeadKey       string
	LeadDigest    string
	GateKeys      []string
	GateDigests   []string
}

type taskPreExecutionPhases struct {
	code        []sqlitestore.TaskExecutionPhase
	tests       []sqlitestore.TaskExecutionPhase
	rebase      []sqlitestore.TaskExecutionPhase
	integration []sqlitestore.TaskExecutionPhase
}

// DebugReconcilePreExecutionTask reconciles exactly the Planner-authorized
// GTW-TSK660, whose real pre-execution dispatch/submit/review/verify/integrate
// lifecycle (execution revision 9) exists durably while the Shared authoring
// record still reads planned. The action validates that existing evidence
// exactly, then completes only the authoring record through the canonical
// completion write — no execution state, phase, or verification receipt is
// minted or rewritten.
func (s *Service) DebugReconcilePreExecutionTask(ctx context.Context, key string) (DebugTaskBootstrapReconciliationResult, error) {
	if s == nil || s.Durability == nil || !s.Config.Debug.Enabled {
		return DebugTaskBootstrapReconciliationResult{}, fmt.Errorf("debug pre-execution reconciliation is unavailable")
	}
	if key != tsk660TaskID {
		return DebugTaskBootstrapReconciliationResult{}, fmt.Errorf("pre-execution reconciliation only accepts %s", tsk660TaskID)
	}
	session, err := s.journalSession(ctx)
	if err != nil {
		return DebugTaskBootstrapReconciliationResult{}, err
	}
	if session.ProjectID != config.GTWProjectID || (session.Role != "planner" && session.Role != "lead") {
		return DebugTaskBootstrapReconciliationResult{}, fmt.Errorf("pre-execution reconciliation requires a Planner or Lead Session for %s", config.GTWProjectID)
	}

	s.taskExecutionMu.Lock()
	defer s.taskExecutionMu.Unlock()

	entityRow, err := s.Durability.ReadSharedTask(ctx, tsk660TaskID)
	if err != nil {
		return DebugTaskBootstrapReconciliationResult{}, err
	}
	var task model.TaskAuthoring
	if err := json.Unmarshal(entityRow.Payload, &task); err != nil {
		return DebugTaskBootstrapReconciliationResult{}, fmt.Errorf("TSK660 authoring record is undecodable")
	}
	state, found, err := s.Durability.ReadTaskExecutionState(ctx, config.GTWProjectID, tsk660TaskID)
	if err != nil {
		return DebugTaskBootstrapReconciliationResult{}, err
	}
	journals, err := s.readTSK660JournalEvidence(ctx)
	if err != nil {
		return DebugTaskBootstrapReconciliationResult{}, err
	}
	phases, err := s.readTSK660Phases(ctx)
	if err != nil {
		return DebugTaskBootstrapReconciliationResult{}, err
	}
	_, resetFound, err := s.Durability.ReadLatestTaskExecutionResetPhase(ctx, config.GTWProjectID, tsk660TaskID)
	if err != nil {
		return DebugTaskBootstrapReconciliationResult{}, err
	}
	verification, err := s.readTSK660LegacyVerification(ctx, task)
	if err != nil {
		return DebugTaskBootstrapReconciliationResult{}, err
	}
	event, eventFound, err := s.Durability.ReadTaskCompletionEvent(ctx, config.GTWProjectID, tsk660TaskID)
	if err != nil {
		return DebugTaskBootstrapReconciliationResult{}, err
	}
	phasesSHA256, err := tsk660PhasesDigest(phases)
	if err != nil {
		return DebugTaskBootstrapReconciliationResult{}, err
	}
	existing, alreadyReconciled, err := validateTSK660State(task, state, found, phases, phasesSHA256, verification, event, eventFound, resetFound, journals)
	if err != nil {
		return DebugTaskBootstrapReconciliationResult{}, err
	}
	project, err := s.EffectiveProjectConfig(config.GTWProjectID)
	if err != nil {
		return DebugTaskBootstrapReconciliationResult{}, err
	}
	if strings.TrimPrefix(project.DefaultBranch, "refs/heads/") != "main" {
		return DebugTaskBootstrapReconciliationResult{}, fmt.Errorf("TSK660 pre-execution evidence is bound to canonical main")
	}
	canonicalMain, err := s.proveTSK660LandedSource(ctx, project)
	if err != nil {
		return DebugTaskBootstrapReconciliationResult{}, err
	}
	if alreadyReconciled {
		if err := s.proveTSK660RecordedMain(ctx, project, existing.CanonicalMainHead, canonicalMain); err != nil {
			return DebugTaskBootstrapReconciliationResult{}, err
		}
		return tsk660PublicResult(state, journals, true), nil
	}

	contract, err := tsk660CompletionContract(task, journals, verification, phasesSHA256, canonicalMain)
	if err != nil {
		return DebugTaskBootstrapReconciliationResult{}, err
	}
	freshRow, err := s.Durability.ReadSharedTask(ctx, tsk660TaskID)
	if err != nil {
		return DebugTaskBootstrapReconciliationResult{}, err
	}
	var freshTask model.TaskAuthoring
	if err := json.Unmarshal(freshRow.Payload, &freshTask); err != nil || freshTask.Revision != task.Revision || freshTask.RevisionSHA256 != task.RevisionSHA256 || freshTask.Status != task.Status {
		return DebugTaskBootstrapReconciliationResult{}, fmt.Errorf("TSK660 authoring revision changed during pre-execution admission")
	}
	freshJournals, err := s.readTSK660JournalEvidence(ctx)
	if err != nil || !sameTSK660JournalEvidence(freshJournals, journals) {
		if err != nil {
			return DebugTaskBootstrapReconciliationResult{}, err
		}
		return DebugTaskBootstrapReconciliationResult{}, fmt.Errorf("TSK660 journal evidence changed during pre-execution admission")
	}
	freshPhases, err := s.readTSK660Phases(ctx)
	if err != nil {
		return DebugTaskBootstrapReconciliationResult{}, err
	}
	freshSHA, err := tsk660PhasesDigest(freshPhases)
	if err != nil || freshSHA != phasesSHA256 {
		return DebugTaskBootstrapReconciliationResult{}, fmt.Errorf("TSK660 phase evidence changed during pre-execution admission")
	}
	finalMain, err := s.Git.RemoteBranchHead(ctx, project, "main")
	if err != nil {
		return DebugTaskBootstrapReconciliationResult{}, err
	}
	if finalMain != canonicalMain {
		return DebugTaskBootstrapReconciliationResult{}, fmt.Errorf("canonical main moved during pre-execution reconciliation; retry is required")
	}
	if err := ctx.Err(); err != nil {
		return DebugTaskBootstrapReconciliationResult{}, err
	}

	now := s.durableNow()
	if now.Before(phases.integration[0].CreatedAt) || now.Before(verification.CompletedAt) {
		return DebugTaskBootstrapReconciliationResult{}, fmt.Errorf("TSK660 reconciliation timestamp ordering conflicts with durable evidence")
	}
	finalTask := task
	finalTask.Status = model.TaskAuthoringDone
	finalTask.UpdatedAt = now
	finalTask.ReadySeal = nil
	if err := model.ValidateTaskAuthoring(finalTask); err != nil {
		return DebugTaskBootstrapReconciliationResult{}, err
	}
	payload, err := json.Marshal(finalTask)
	if err != nil {
		return DebugTaskBootstrapReconciliationResult{}, err
	}
	contractSHA := sha256.Sum256(contract)
	opSum := sha256.Sum256(append([]byte(config.GTWProjectID+"\x00"+tsk660TaskID+"\x00"), contract...))
	prevState := state
	finalState := state
	finalState.Status = model.TaskExecutionDone
	finalState.ExecutionRevision = tsk660CompletedRevision
	finalState.UpdatedAt = now
	req := sqlitestore.CommitTaskCompletionRequest{
		OperationID: "task-complete-" + hex.EncodeToString(opSum[:]),
		ProjectID:   config.GTWProjectID, TaskID: tsk660TaskID,
		Revision: entityRow.Revision, PreviousTaskPayload: entityRow.Payload, TaskPayload: payload,
		FromStatus: model.TaskAuthoringPlanned, Actor: session.ID, Reason: tsk660ReconcileReason,
		Contract: contract, ContractSHA256: hex.EncodeToString(contractSHA[:]), RecordedAt: now,
		PreviousExecution: &prevState, FinalExecution: &finalState,
	}
	if err := s.Durability.CommitTaskCompletion(ctx, req); err != nil {
		return DebugTaskBootstrapReconciliationResult{}, fmt.Errorf("TSK660 pre-execution reconciliation could not be durably recorded: %w", err)
	}
	return tsk660PublicResult(finalState, journals, false), nil
}

func (s *Service) readTSK660JournalEvidence(ctx context.Context) (taskPreExecutionJournalEvidence, error) {
	planner, err := s.JournalRead(ctx, JournalReadInput{Key: tsk660PlannerJournalKey})
	if err != nil {
		return taskPreExecutionJournalEvidence{}, fmt.Errorf("read TSK660 Planner authorization %s: %w", tsk660PlannerJournalKey, err)
	}
	lead, err := s.JournalRead(ctx, JournalReadInput{Key: tsk660LeadJournalKey})
	if err != nil {
		return taskPreExecutionJournalEvidence{}, fmt.Errorf("read TSK660 Lead record %s: %w", tsk660LeadJournalKey, err)
	}
	if err := validateTSK660JournalEntry(planner, tsk660PlannerJournalKey, tsk660PlannerJournalSeq, model.JournalStreamPlannerNotes, "planner"); err != nil {
		return taskPreExecutionJournalEvidence{}, err
	}
	if err := validateTSK660JournalEntry(lead, tsk660LeadJournalKey, tsk660LeadJournalSeq, model.JournalStreamLeadFriction, "lead"); err != nil {
		return taskPreExecutionJournalEvidence{}, err
	}
	var auth taskBootstrapPlannerJournalData
	if err := decodeStrict(planner.Data, &auth); err != nil {
		return taskPreExecutionJournalEvidence{}, err
	}
	var review taskBootstrapLeadJournalData
	if err := decodeStrict(lead.Data, &review); err != nil {
		return taskPreExecutionJournalEvidence{}, err
	}
	if !tsk660PlannerAuthorizationValid(auth) || !tsk660LeadRecordValid(review) {
		return taskPreExecutionJournalEvidence{}, fmt.Errorf("TSK660 journal evidence does not match the authorized Planner decision or Lead record")
	}
	plannerDigest, err := tsk660JournalDigest(planner)
	if err != nil {
		return taskPreExecutionJournalEvidence{}, err
	}
	leadDigest, err := tsk660JournalDigest(lead)
	if err != nil {
		return taskPreExecutionJournalEvidence{}, err
	}
	gates, err := s.readTSK660LeadGateEvidence(ctx, planner)
	if err != nil {
		return taskPreExecutionJournalEvidence{}, err
	}
	gateKeys := make([]string, 0, len(gates))
	gateDigests := make([]string, 0, len(gates))
	for _, gate := range gates {
		digest, err := tsk660JournalDigest(gate)
		if err != nil {
			return taskPreExecutionJournalEvidence{}, err
		}
		gateKeys = append(gateKeys, gate.ID)
		gateDigests = append(gateDigests, digest)
	}
	return taskPreExecutionJournalEvidence{
		PlannerKey:    planner.ID,
		PlannerDigest: plannerDigest,
		LeadKey:       lead.ID,
		LeadDigest:    leadDigest,
		GateKeys:      gateKeys,
		GateDigests:   gateDigests,
	}, nil
}

func validateTSK660JournalEntry(entry model.JournalEntry, key string, sequence uint64, stream model.JournalStream, role string) error {
	if err := model.ValidateJournalEntry(entry); err != nil {
		return err
	}
	if entry.ID != key || entry.Sequence != sequence || entry.Stream != stream || entry.Role != role || entry.ProjectID != config.GTWProjectID {
		return fmt.Errorf("journal %s does not match the required %s authority", key, role)
	}
	return nil
}

// readTSK660LeadGateEvidence requires a durable Lead record written after the
// Planner authorization that independently documents the review and the
// applicable gate verification of the exact implementation commit. Without it
// the reconciliation fails closed rather than terminalizing on Planner
// authorization alone.
func (s *Service) readTSK660LeadGateEvidence(ctx context.Context, planner model.JournalEntry) ([]model.JournalEntry, error) {
	var matches []model.JournalEntry
	cursor := ""
	for {
		page, err := s.JournalList(ctx, JournalListInput{
			Stream: string(model.JournalStreamLeadFriction),
			Limit:  256,
			Cursor: cursor,
		})
		if err != nil {
			return nil, err
		}
		for _, entry := range page.Items {
			if tsk660LeadGateEntryValid(entry, planner) {
				matches = append(matches, entry)
			}
		}
		if !page.HasMore {
			break
		}
		cursor = page.NextCursor
	}
	slices.SortFunc(matches, func(a, b model.JournalEntry) int { return int(a.Sequence - b.Sequence) })
	if len(matches) == 0 {
		return nil, fmt.Errorf("TSK660 Lead review and gate evidence for %s is not durably recorded", tsk660ImplementationCommit[:8])
	}
	return matches, nil
}

func tsk660LeadGateEntryValid(entry model.JournalEntry, planner model.JournalEntry) bool {
	if model.ValidateJournalEntry(entry) != nil || entry.Role != "lead" || entry.Stream != model.JournalStreamLeadFriction || entry.ProjectID != config.GTWProjectID {
		return false
	}
	if entry.Actor == planner.Actor || entry.SessionID == planner.SessionID || !entry.CreatedAt.After(planner.CreatedAt) {
		return false
	}
	var data taskBootstrapLeadJournalData
	if err := decodeStrict(entry.Data, &data); err != nil {
		return false
	}
	joined := data.Problem + "\n" + data.Impact + "\n" + data.ProposedImprovement + "\n" + strings.Join(data.Evidence, "\n")
	if !strings.Contains(joined, tsk660TaskID) || !strings.Contains(joined, tsk660ImplementationCommit[:7]) {
		return false
	}
	return slices.ContainsFunc(data.Evidence, func(item string) bool {
		lower := strings.ToLower(item)
		return strings.Contains(lower, "gate") || strings.Contains(lower, "test") || strings.Contains(lower, "verif")
	})
}

func (s *Service) readTSK660Phases(ctx context.Context) (taskPreExecutionPhases, error) {
	var result taskPreExecutionPhases
	for _, item := range []struct {
		stage  string
		target *[]sqlitestore.TaskExecutionPhase
	}{
		{stage: "code", target: &result.code},
		{stage: "tests", target: &result.tests},
		{stage: "rebase", target: &result.rebase},
		{stage: "integration", target: &result.integration},
	} {
		phases, err := s.Durability.ReadTaskExecutionPhases(ctx, config.GTWProjectID, tsk660TaskID, item.stage)
		if err != nil {
			return taskPreExecutionPhases{}, err
		}
		*item.target = phases
	}
	return result, nil
}

// tsk660PhasesDigest binds the complete ordered phase history — including
// comments and timestamps — into the reconciliation contract.
func tsk660PhasesDigest(phases taskPreExecutionPhases) (string, error) {
	all := make([]sqlitestore.TaskExecutionPhase, 0, 5)
	all = append(all, phases.code...)
	all = append(all, phases.tests...)
	all = append(all, phases.rebase...)
	all = append(all, phases.integration...)
	raw, err := json.Marshal(all)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

// readTSK660LegacyVerification recognizes TSK660's one pre-execution-era
// verification receipt (GTW-OPR3793, produced before the current
// Procedure-gate contract) as its historical gate evidence. Any other receipt
// shape, a second receipt, or an undecodable/mismatched record fails closed.
func (s *Service) readTSK660LegacyVerification(ctx context.Context, task model.TaskAuthoring) (taskPreExecutionVerificationProof, error) {
	receipts, err := s.Durability.ReadTaskExecutionVerificationReceipts(ctx, config.GTWProjectID, tsk660TaskID)
	if err != nil {
		return taskPreExecutionVerificationProof{}, err
	}
	if len(receipts) != 1 {
		return taskPreExecutionVerificationProof{}, fmt.Errorf("TSK660 has %d verification receipts; exactly the authorized pre-execution receipt is required", len(receipts))
	}
	row := receipts[0]
	if row.OperationID != tsk660LegacyVerificationOperation || row.AttemptRevision != tsk660TaskRevision || row.Outcome != model.TaskExecutionVerificationSucceeded {
		return taskPreExecutionVerificationProof{}, fmt.Errorf("TSK660 verification receipt is not the authorized pre-execution gate proof")
	}
	var receipt model.TaskExecutionVerification
	if err := json.Unmarshal([]byte(row.ReceiptJSON), &receipt); err != nil {
		return taskPreExecutionVerificationProof{}, fmt.Errorf("TSK660 legacy verification receipt is undecodable")
	}
	if receipt.ProjectID != config.GTWProjectID || receipt.TaskID != tsk660TaskID || receipt.OperationID != row.OperationID || receipt.TaskRevision != tsk660TaskRevision || receipt.AttemptRevision != row.AttemptRevision || receipt.TaskRevisionSHA256 != task.RevisionSHA256 || receipt.Outcome != model.TaskExecutionVerificationSucceeded || receipt.Error != "" || receipt.BaseHead != tsk660MainBase || receipt.CandidateHead != tsk660LegacyVerificationCandidate || receipt.CandidateTree != tsk660ImplementationTree || receipt.Branch != tsk660ExecutionBranch || receipt.CodeReviewID != tsk660LegacyVerificationReviewID || receipt.TestsReviewID != 0 || receipt.RebaseReviewID != 0 {
		return taskPreExecutionVerificationProof{}, fmt.Errorf("TSK660 verification receipt identity does not match the authorized pre-execution gate proof")
	}
	if receipt.StartedAt.IsZero() || receipt.CompletedAt.IsZero() || !receipt.CompletedAt.After(receipt.StartedAt) {
		return taskPreExecutionVerificationProof{}, fmt.Errorf("TSK660 legacy verification receipt timing is invalid")
	}
	want := map[string]bool{"format": false, "check": false, "test": false}
	for _, gate := range receipt.Gates {
		if _, ok := want[gate.ID]; !ok || want[gate.ID] || gate.ExitCode != 0 || gate.Execution != "executed" || gate.TreeID != tsk660ImplementationTree {
			return taskPreExecutionVerificationProof{}, fmt.Errorf("TSK660 legacy verification gate %q does not match the authorized pre-execution gate proof", gate.ID)
		}
		want[gate.ID] = true
	}
	for _, seen := range want {
		if !seen {
			return taskPreExecutionVerificationProof{}, fmt.Errorf("TSK660 legacy verification is missing a required pre-execution gate")
		}
	}
	digest := sha256.Sum256([]byte(row.ReceiptJSON))
	return taskPreExecutionVerificationProof{
		OperationID:   row.OperationID,
		CandidateHead: receipt.CandidateHead,
		ReceiptSHA256: hex.EncodeToString(digest[:]),
		CompletedAt:   receipt.CompletedAt,
	}, nil
}

// validateTSK660Phases pins the exact real pre-execution lifecycle: revision 2
// first submission, revision 3 rework, revision 4 resubmission, revision 5
// review accept, revision 9 integration accept. Any extra or divergent phase
// fails closed.
func validateTSK660Phases(phases taskPreExecutionPhases) error {
	if len(phases.tests) != 0 || len(phases.rebase) != 0 {
		return fmt.Errorf("TSK660 carries tests/rebase phase evidence the pre-execution lifecycle never produced")
	}
	type want struct {
		revision     int
		status       string
		head         string
		eventKind    string
		decision     string
		commentEmpty bool
	}
	codeWant := []want{
		{2, model.TaskExecutionAwaitingReview, tsk660FirstSubmissionHead, "submission", "", true},
		{3, model.TaskExecutionChangesRequested, tsk660FirstSubmissionHead, "rework", "", false},
		{4, model.TaskExecutionAwaitingReview, tsk660FinalSubmissionHead, "submission", "", true},
		{5, model.TaskExecutionReadyForVerification, tsk660FinalSubmissionHead, "review", "accept", false},
	}
	if len(phases.code) != len(codeWant) {
		return fmt.Errorf("TSK660 code-phase history does not match the authorized pre-execution lifecycle")
	}
	for i, w := range codeWant {
		phase := phases.code[i]
		if phase.ProjectID != config.GTWProjectID || phase.TaskID != tsk660TaskID || phase.ExecutionRevision != w.revision || phase.Stage != "code" || phase.Status != w.status || phase.Head != w.head || phase.Branch != tsk660ExecutionBranch || phase.TaskRevisionSHA256 == "" || phase.EventKind != w.eventKind || phase.Decision != w.decision || (len(phase.Comment) == 0) != w.commentEmpty || phase.CreatedAt.IsZero() {
			return fmt.Errorf("TSK660 code phase %d does not match the authorized pre-execution lifecycle", w.revision)
		}
	}
	if len(phases.integration) != 1 {
		return fmt.Errorf("TSK660 lacks the exact pre-execution integration phase")
	}
	phase := phases.integration[0]
	if phase.ProjectID != config.GTWProjectID || phase.TaskID != tsk660TaskID || phase.ExecutionRevision != tsk660IntegratedRevision || phase.Stage != "integration" || phase.Status != model.TaskExecutionIntegrated || phase.Head != tsk660ImplementationCommit || phase.Branch != tsk660ExecutionBranch || phase.TaskRevisionSHA256 == "" || phase.EventKind != "integration" || phase.Decision != "accept" || len(phase.Comment) != 0 || phase.CreatedAt.IsZero() {
		return fmt.Errorf("TSK660 integration phase does not match the authorized pre-execution lifecycle")
	}
	return nil
}

func validateTSK660State(task model.TaskAuthoring, state model.TaskExecutionState, found bool, phases taskPreExecutionPhases, phasesSHA256 string, verification taskPreExecutionVerificationProof, event sqlitestore.TaskLifecycleEvent, eventFound, resetFound bool, journals taskPreExecutionJournalEvidence) (taskPreExecutionPhaseEvidence, bool, error) {
	if task.ID != tsk660TaskID || task.ProjectID != config.GTWProjectID || task.Revision != tsk660TaskRevision {
		return taskPreExecutionPhaseEvidence{}, false, fmt.Errorf("TSK660 authoring record does not match the authorized pre-execution identity")
	}
	if resetFound {
		return taskPreExecutionPhaseEvidence{}, false, fmt.Errorf("TSK660 carries reset evidence the pre-execution lifecycle never produced")
	}
	if err := validateTSK660Phases(phases); err != nil {
		return taskPreExecutionPhaseEvidence{}, false, err
	}
	if !found {
		return taskPreExecutionPhaseEvidence{}, false, fmt.Errorf("TSK660 has no durable execution state to reconcile")
	}
	if err := model.ValidateTaskExecutionState(state); err != nil {
		return taskPreExecutionPhaseEvidence{}, false, err
	}
	if state.TaskID != tsk660TaskID || state.ProjectID != config.GTWProjectID || state.TaskRevision != tsk660TaskRevision || state.TaskRevisionSHA256 != task.RevisionSHA256 || state.Stage != "code" || state.Worktree != tsk660ExecutionWorktree || state.BaseHead != tsk660MainBase || state.Head != tsk660FinalSubmissionHead || state.Branch != tsk660ExecutionBranch || state.Agent != config.GTWWorkerAgentID {
		return taskPreExecutionPhaseEvidence{}, false, fmt.Errorf("TSK660 execution state is not the exact authorized pre-execution lifecycle")
	}
	switch {
	case !eventFound:
		if task.Status != model.TaskAuthoringPlanned || state.Status != model.TaskExecutionIntegrated || state.ExecutionRevision != tsk660IntegratedRevision {
			return taskPreExecutionPhaseEvidence{}, false, fmt.Errorf("TSK660 without completion evidence must be planned with the exact rev9 integrated state")
		}
		return taskPreExecutionPhaseEvidence{}, false, nil
	default:
		if task.Status != model.TaskAuthoringDone || state.Status != model.TaskExecutionDone || state.ExecutionRevision != tsk660CompletedRevision || !state.UpdatedAt.Equal(event.RecordedAt.UTC()) {
			return taskPreExecutionPhaseEvidence{}, false, fmt.Errorf("TSK660 completion evidence does not bind the reconciled state")
		}
		if event.EventKind != sqlitestore.TaskLifecycleEventKindComplete || event.ProjectID != config.GTWProjectID || event.TaskID != tsk660TaskID || event.Revision != tsk660TaskRevision || event.FromStatus != model.TaskAuthoringPlanned || event.ToStatus != model.TaskAuthoringDone {
			return taskPreExecutionPhaseEvidence{}, false, fmt.Errorf("TSK660 completion event is not the authorized pre-execution reconciliation")
		}
		var evidence taskPreExecutionPhaseEvidence
		if err := decodeStrict(event.Contract, &evidence); err != nil {
			return taskPreExecutionPhaseEvidence{}, false, fmt.Errorf("TSK660 completion contract is malformed: %w", err)
		}
		canonical, err := json.Marshal(evidence)
		if err != nil || string(canonical) != string(event.Contract) {
			return taskPreExecutionPhaseEvidence{}, false, fmt.Errorf("TSK660 completion contract is not canonical")
		}
		if !tsk660EvidenceValid(evidence, task, verification, phasesSHA256, journals) {
			return taskPreExecutionPhaseEvidence{}, false, fmt.Errorf("TSK660 reconciliation contract conflicts with current authority")
		}
		return evidence, true, nil
	}
}

func tsk660EvidenceValid(evidence taskPreExecutionPhaseEvidence, task model.TaskAuthoring, verification taskPreExecutionVerificationProof, phasesSHA256 string, journals taskPreExecutionJournalEvidence) bool {
	return evidence.SchemaVersion == 1 && evidence.Kind == tsk660ReconcileKind && evidence.TaskID == tsk660TaskID && evidence.TaskRevision == tsk660TaskRevision && evidence.TaskRevisionSHA256 == task.RevisionSHA256 && evidence.PlannerAuthorization == journals.PlannerKey && evidence.PlannerEvidenceSHA256 == journals.PlannerDigest && evidence.LeadReviewEvidence == journals.LeadKey && evidence.LeadReviewSHA256 == journals.LeadDigest && slices.Equal(evidence.LeadGateEvidence, journals.GateKeys) && slices.Equal(evidence.LeadGateEvidenceSHA256, journals.GateDigests) && evidence.ImplementationCommit == tsk660ImplementationCommit && evidence.ImplementationTree == tsk660ImplementationTree && evidence.MainBase == tsk660MainBase && model.ValidateCommitSHA(evidence.CanonicalMainHead) == nil && evidence.ExecutionRevision == tsk660IntegratedRevision && evidence.PhasesSHA256 == phasesSHA256 && evidence.LegacyVerificationOperation == verification.OperationID && evidence.LegacyVerificationSHA256 == verification.ReceiptSHA256 && evidence.LegacyVerificationCandidate == verification.CandidateHead && !evidence.ExecutionStateMinted && !evidence.PhaseMinted && !evidence.VerificationReceiptMinted
}

func tsk660CompletionContract(task model.TaskAuthoring, journals taskPreExecutionJournalEvidence, verification taskPreExecutionVerificationProof, phasesSHA256, canonicalMain string) ([]byte, error) {
	evidence := taskPreExecutionPhaseEvidence{
		SchemaVersion:               1,
		Kind:                        tsk660ReconcileKind,
		TaskID:                      tsk660TaskID,
		TaskRevision:                task.Revision,
		TaskRevisionSHA256:          task.RevisionSHA256,
		PlannerAuthorization:        journals.PlannerKey,
		PlannerEvidenceSHA256:       journals.PlannerDigest,
		LeadReviewEvidence:          journals.LeadKey,
		LeadReviewSHA256:            journals.LeadDigest,
		LeadGateEvidence:            journals.GateKeys,
		LeadGateEvidenceSHA256:      journals.GateDigests,
		ImplementationCommit:        tsk660ImplementationCommit,
		ImplementationTree:          tsk660ImplementationTree,
		MainBase:                    tsk660MainBase,
		CanonicalMainHead:           canonicalMain,
		ExecutionRevision:           tsk660IntegratedRevision,
		PhasesSHA256:                phasesSHA256,
		LegacyVerificationOperation: verification.OperationID,
		LegacyVerificationSHA256:    verification.ReceiptSHA256,
		LegacyVerificationCandidate: verification.CandidateHead,
	}
	return json.Marshal(evidence)
}

func (s *Service) proveTSK660LandedSource(ctx context.Context, project config.ProjectConfig) (string, error) {
	canonicalMain, err := s.Git.RemoteBranchHead(ctx, project, "main")
	if err != nil {
		return "", err
	}
	if err := s.Git.MaterializeRemoteBranchObjects(ctx, project, "main"); err != nil {
		return "", err
	}
	tree, parents, err := s.Git.InspectTaskIntegrationCommit(ctx, project, tsk660ImplementationCommit)
	if err != nil {
		return "", err
	}
	if tree != tsk660ImplementationTree || len(parents) != 1 || parents[0] != tsk660MainBase || model.ValidateCommitSHA(canonicalMain) != nil {
		return "", fmt.Errorf("TSK660 implementation commit identity does not match the authorized base and tree")
	}
	landed, err := s.Git.IsAncestor(ctx, project.Root, tsk660ImplementationCommit, canonicalMain)
	if err != nil {
		return "", err
	}
	if !landed {
		return "", fmt.Errorf("TSK660 implementation commit %s is not an ancestor of canonical main", tsk660ImplementationCommit[:8])
	}
	finalMain, err := s.Git.RemoteBranchHead(ctx, project, "main")
	if err != nil {
		return "", err
	}
	if finalMain != canonicalMain {
		return "", fmt.Errorf("canonical main moved during TSK660 source proof; retry is required")
	}
	return canonicalMain, nil
}

func (s *Service) proveTSK660RecordedMain(ctx context.Context, project config.ProjectConfig, recordedMain, canonicalMain string) error {
	if model.ValidateCommitSHA(recordedMain) != nil || model.ValidateCommitSHA(canonicalMain) != nil {
		return fmt.Errorf("recorded pre-execution reconciliation head is invalid")
	}
	candidateAncestor, err := s.Git.IsAncestor(ctx, project.Root, tsk660ImplementationCommit, recordedMain)
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
	if !candidateAncestor {
		return fmt.Errorf("recorded pre-execution reconciliation head does not contain the authorized implementation")
	}
	if !recordedAncestor {
		return fmt.Errorf("canonical main no longer descends from the recorded pre-execution reconciliation head")
	}
	finalMain, err := s.Git.RemoteBranchHead(ctx, project, "main")
	if err != nil {
		return err
	}
	if finalMain != canonicalMain {
		return fmt.Errorf("canonical main moved during pre-execution replay proof; retry is required")
	}
	return nil
}

func sameTSK660JournalEvidence(left, right taskPreExecutionJournalEvidence) bool {
	return left.PlannerKey == right.PlannerKey && left.PlannerDigest == right.PlannerDigest && left.LeadKey == right.LeadKey && left.LeadDigest == right.LeadDigest && slices.Equal(left.GateKeys, right.GateKeys) && slices.Equal(left.GateDigests, right.GateDigests)
}

func tsk660JournalDigest(entry model.JournalEntry) (string, error) {
	raw, err := json.Marshal(entry)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

func tsk660PublicResult(state model.TaskExecutionState, journals taskPreExecutionJournalEvidence, alreadyReconciled bool) DebugTaskBootstrapReconciliationResult {
	evidence := append([]string{tsk660LeadJournalKey, tsk660PlannerJournalKey}, journals.GateKeys...)
	return DebugTaskBootstrapReconciliationResult{
		Key:               tsk660TaskID,
		Status:            model.TaskExecutionIntegrated,
		ExecutionRevision: state.ExecutionRevision,
		IntegrationHead:   tsk660ImplementationCommit[:8],
		Evidence:          evidence,
		AlreadyReconciled: alreadyReconciled,
	}
}

func tsk660PlannerAuthorizationValid(data taskBootstrapPlannerJournalData) bool {
	want := taskBootstrapPlannerJournalData{
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
	return sameTSK678PlannerJournalData(data, want)
}

func tsk660LeadRecordValid(data taskBootstrapLeadJournalData) bool {
	wantEvidence := []string{
		"TSK660 shared record: revision 6, status planned, no execution state, never dispatched, not a member of TRK1 or TRK2.",
		"Its implementation commit 899abc9 ('Task GTW-TSK660: Implement Milestone roadmap membership and Shared Track execution/delivery authority', Sept 22) is an ancestor of current main — the work landed under the pre-execution-model lifecycle.",
		"Record was likely reset to planned during the execution-model cutover, losing integrated state — same unreconciled-historical class as TSK677.",
		"TSK606 and TSK529 declare TSK660 as a dependency; while TSK660 reads 'planned' they remain dependency-blocked despite the functionality existing.",
		"debug/task-bootstrap-reconcile is enum-pinned to GTW-TSK677 and cannot reconcile TSK660.",
	}
	return data.Problem == "GTW-TSK660 (Milestone+Track implementation, P0) is already-landed on main but unreconciled: it blocks dependent TSK606 (P0) and TSK529 (P1) from eligibility." &&
		data.Impact == "Planner decision needed: either reconcile TSK660 as already-landed via an extended mechanism, or authorize a normal dispatch path. Until then TSK606/TSK529 stay blocked; TSK627/TSK679/TSK669/TSK671/TSK672 remain eligible and are being worked." &&
		data.ProposedImprovement == "Generalize the bootstrap-reconcile mechanism to a bounded already-landed reconciliation class (exact identity + Planner authorization per Task), or issue a dedicated reconciliation for TSK660." &&
		slices.Equal(data.Evidence, wantEvidence)
}
