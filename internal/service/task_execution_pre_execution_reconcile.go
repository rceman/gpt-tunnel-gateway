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

// taskReconcilePhaseSpec pins one expected lifecycle phase of a historical
// integrated execution.
type taskReconcilePhaseSpec struct {
	Stage        string
	Revision     int
	Status       string
	Head         string
	EventKind    string
	Decision     string
	CommentEmpty bool
}

// taskReconcileReceiptSpec pins one expected persisted verification receipt.
type taskReconcileReceiptSpec struct {
	OperationID    string
	Attempt        int
	Outcome        string
	CandidateHead  string
	CandidateTree  string
	CodeReviewID   int64
	TestsReviewID  int64
	RebaseReviewID int64
	RequiredGates  []string
}

// taskReconcileSpec is the exact per-Task evidence contract for one
// transition-only stale-authoring reconciliation.
type taskReconcileSpec struct {
	taskID             string
	taskRevision       int
	integratedRevision int
	completedRevision  int
	kind               string
	reason             string
	mainBase           string
	implCommit         string
	implTree           string
	stateHead          string
	stage              string
	worktree           string
	branch             string
	phases             []taskReconcilePhaseSpec
	receipts           []taskReconcileReceiptSpec
	successReceipt     string
	plannerKey         string
	plannerSeq         uint64
	plannerValid       func(taskBootstrapPlannerJournalData) bool
	leadKey            string
	leadSeq            uint64
	leadValid          func(taskBootstrapLeadJournalData) bool
	semantic           func(ctx context.Context, s *Service, project config.ProjectConfig, canonicalMain string) error
}

// tsk660ReconcileSpec is the exact TSK660 contract landed by GTW-TSK685.
var tsk660ReconcileSpec = taskReconcileSpec{
	taskID:             tsk660TaskID,
	taskRevision:       tsk660TaskRevision,
	integratedRevision: tsk660IntegratedRevision,
	completedRevision:  tsk660CompletedRevision,
	kind:               tsk660ReconcileKind,
	reason:             tsk660ReconcileReason,
	mainBase:           tsk660MainBase,
	implCommit:         tsk660ImplementationCommit,
	implTree:           tsk660ImplementationTree,
	stateHead:          tsk660FinalSubmissionHead,
	stage:              "code",
	worktree:           tsk660ExecutionWorktree,
	branch:             tsk660ExecutionBranch,
	phases: []taskReconcilePhaseSpec{
		{Stage: "code", Revision: 2, Status: model.TaskExecutionAwaitingReview, Head: tsk660FirstSubmissionHead, EventKind: "submission", CommentEmpty: true},
		{Stage: "code", Revision: 3, Status: model.TaskExecutionChangesRequested, Head: tsk660FirstSubmissionHead, EventKind: "rework"},
		{Stage: "code", Revision: 4, Status: model.TaskExecutionAwaitingReview, Head: tsk660FinalSubmissionHead, EventKind: "submission", CommentEmpty: true},
		{Stage: "code", Revision: 5, Status: model.TaskExecutionReadyForVerification, Head: tsk660FinalSubmissionHead, EventKind: "review", Decision: "accept"},
		{Stage: "integration", Revision: tsk660IntegratedRevision, Status: model.TaskExecutionIntegrated, Head: tsk660ImplementationCommit, EventKind: "integration", Decision: "accept", CommentEmpty: true},
	},
	receipts: []taskReconcileReceiptSpec{{
		OperationID: tsk660LegacyVerificationOperation, Attempt: tsk660TaskRevision,
		Outcome: model.TaskExecutionVerificationSucceeded, CandidateHead: tsk660LegacyVerificationCandidate,
		CandidateTree: tsk660ImplementationTree,
		CodeReviewID:  tsk660LegacyVerificationReviewID, RequiredGates: []string{"format", "check", "test"},
	}},
	successReceipt: tsk660LegacyVerificationOperation,
	plannerKey:     tsk660PlannerJournalKey,
	plannerSeq:     tsk660PlannerJournalSeq,
	plannerValid:   tsk660PlannerAuthorizationValid,
	leadKey:        tsk660LeadJournalKey,
	leadSeq:        tsk660LeadJournalSeq,
	leadValid:      tsk660LeadRecordValid,
}

// reconcileSpecFor returns the exact authorized spec for one reconcilable
// Task — TSK660 plus the TSK688-authorized TSK589/TSK594/TSK593.
func reconcileSpecFor(key string) (taskReconcileSpec, bool) {
	switch key {
	case tsk660TaskID:
		return tsk660ReconcileSpec, true
	case tsk589TaskID:
		return tsk589ReconcileSpec, true
	case tsk594TaskID:
		return tsk594ReconcileSpec, true
	case tsk593TaskID:
		return tsk593ReconcileSpec, true
	}
	return taskReconcileSpec{}, false
}

// reconcileAllowlist is the closed set of Tasks this transition-only debug
// action may reconcile, in declaration order.
var reconcileAllowlist = []string{tsk660TaskID, tsk589TaskID, tsk594TaskID, tsk593TaskID}

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
	ReceiptsSHA256              string   `json:"receipts_sha256"`
	LegacyVerificationOperation string   `json:"legacy_verification_operation"`
	LegacyVerificationSHA256    string   `json:"legacy_verification_sha256"`
	LegacyVerificationCandidate string   `json:"legacy_verification_candidate"`
	ExecutionStateMinted        bool     `json:"execution_state_minted"`
	PhaseMinted                 bool     `json:"phase_minted"`
	VerificationReceiptMinted   bool     `json:"verification_receipt_minted"`
}

type taskPreExecutionVerificationProof struct {
	OperationID    string
	CandidateHead  string
	ReceiptSHA256  string
	ReceiptsSHA256 string
	CompletedAt    time.Time
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

// DebugReconcilePreExecutionTask reconciles one of the Planner-authorized
// Tasks whose real pre-execution integrated lifecycle exists durably while
// the Shared authoring record still reads planned. The action validates that
// existing evidence exactly, then completes only the authoring record through
// the canonical completion write — no execution state, phase, or verification
// receipt is minted or rewritten.
func (s *Service) DebugReconcilePreExecutionTask(ctx context.Context, key string) (DebugTaskBootstrapReconciliationResult, error) {
	if s == nil || s.Durability == nil || !s.Config.Debug.Enabled {
		return DebugTaskBootstrapReconciliationResult{}, fmt.Errorf("debug pre-execution reconciliation is unavailable")
	}
	spec, allowed := reconcileSpecFor(key)
	if !allowed {
		return DebugTaskBootstrapReconciliationResult{}, fmt.Errorf("pre-execution reconciliation only accepts %s", strings.Join(reconcileAllowlist, ", "))
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

	entityRow, err := s.Durability.ReadSharedTask(ctx, spec.taskID)
	if err != nil {
		return DebugTaskBootstrapReconciliationResult{}, err
	}
	var task model.TaskAuthoring
	if err := json.Unmarshal(entityRow.Payload, &task); err != nil {
		return DebugTaskBootstrapReconciliationResult{}, fmt.Errorf("%s authoring record is undecodable", spec.taskID)
	}
	state, found, err := s.Durability.ReadTaskExecutionState(ctx, config.GTWProjectID, spec.taskID)
	if err != nil {
		return DebugTaskBootstrapReconciliationResult{}, err
	}
	journals, err := s.readJournalEvidence(ctx, spec)
	if err != nil {
		return DebugTaskBootstrapReconciliationResult{}, err
	}
	phases, err := s.readReconcilePhases(ctx, spec)
	if err != nil {
		return DebugTaskBootstrapReconciliationResult{}, err
	}
	_, resetFound, err := s.Durability.ReadLatestTaskExecutionResetPhase(ctx, config.GTWProjectID, spec.taskID)
	if err != nil {
		return DebugTaskBootstrapReconciliationResult{}, err
	}
	verification, err := s.readLegacyVerification(ctx, spec, task)
	if err != nil {
		return DebugTaskBootstrapReconciliationResult{}, err
	}
	event, eventFound, err := s.Durability.ReadTaskCompletionEvent(ctx, config.GTWProjectID, spec.taskID)
	if err != nil {
		return DebugTaskBootstrapReconciliationResult{}, err
	}
	phasesSHA256, err := reconcilePhasesDigest(phases)
	if err != nil {
		return DebugTaskBootstrapReconciliationResult{}, err
	}
	existing, alreadyReconciled, err := validateReconcileState(spec, task, state, found, phases, phasesSHA256, verification, event, eventFound, resetFound, journals)
	if err != nil {
		return DebugTaskBootstrapReconciliationResult{}, err
	}
	project, err := s.EffectiveProjectConfig(config.GTWProjectID)
	if err != nil {
		return DebugTaskBootstrapReconciliationResult{}, err
	}
	if strings.TrimPrefix(project.DefaultBranch, "refs/heads/") != "main" {
		return DebugTaskBootstrapReconciliationResult{}, fmt.Errorf("%s pre-execution evidence is bound to canonical main", spec.taskID)
	}
	canonicalMain, err := s.proveLandedSource(ctx, spec, project)
	if err != nil {
		return DebugTaskBootstrapReconciliationResult{}, err
	}
	if spec.semantic != nil {
		if err := spec.semantic(ctx, s, project, canonicalMain); err != nil {
			return DebugTaskBootstrapReconciliationResult{}, err
		}
	}
	if alreadyReconciled {
		if err := s.proveRecordedMain(ctx, spec, project, existing.CanonicalMainHead, canonicalMain); err != nil {
			return DebugTaskBootstrapReconciliationResult{}, err
		}
		return reconcilePublicResult(spec, state, journals, true), nil
	}

	contract, err := reconcileCompletionContract(spec, task, journals, verification, phasesSHA256, canonicalMain)
	if err != nil {
		return DebugTaskBootstrapReconciliationResult{}, err
	}
	freshRow, err := s.Durability.ReadSharedTask(ctx, spec.taskID)
	if err != nil {
		return DebugTaskBootstrapReconciliationResult{}, err
	}
	var freshTask model.TaskAuthoring
	if err := json.Unmarshal(freshRow.Payload, &freshTask); err != nil || freshTask.Revision != task.Revision || freshTask.RevisionSHA256 != task.RevisionSHA256 || freshTask.Status != task.Status {
		return DebugTaskBootstrapReconciliationResult{}, fmt.Errorf("%s authoring revision changed during pre-execution admission", spec.taskID)
	}
	freshJournals, err := s.readJournalEvidence(ctx, spec)
	if err != nil || !sameJournalEvidence(freshJournals, journals) {
		if err != nil {
			return DebugTaskBootstrapReconciliationResult{}, err
		}
		return DebugTaskBootstrapReconciliationResult{}, fmt.Errorf("%s journal evidence changed during pre-execution admission", spec.taskID)
	}
	freshPhases, err := s.readReconcilePhases(ctx, spec)
	if err != nil {
		return DebugTaskBootstrapReconciliationResult{}, err
	}
	freshSHA, err := reconcilePhasesDigest(freshPhases)
	if err != nil || freshSHA != phasesSHA256 {
		return DebugTaskBootstrapReconciliationResult{}, fmt.Errorf("%s phase evidence changed during pre-execution admission", spec.taskID)
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

	integrationPhase := phases.integration[len(phases.integration)-1]
	now := s.durableNow()
	if now.Before(integrationPhase.CreatedAt) || now.Before(verification.CompletedAt) {
		return DebugTaskBootstrapReconciliationResult{}, fmt.Errorf("%s reconciliation timestamp ordering conflicts with durable evidence", spec.taskID)
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
	opSum := sha256.Sum256(append([]byte(config.GTWProjectID+"\x00"+spec.taskID+"\x00"), contract...))
	prevState := state
	finalState := state
	finalState.Status = model.TaskExecutionDone
	finalState.ExecutionRevision = spec.completedRevision
	finalState.UpdatedAt = now
	req := sqlitestore.CommitTaskCompletionRequest{
		OperationID: "task-complete-" + hex.EncodeToString(opSum[:]),
		ProjectID:   config.GTWProjectID, TaskID: spec.taskID,
		Revision: entityRow.Revision, PreviousTaskPayload: entityRow.Payload, TaskPayload: payload,
		FromStatus: model.TaskAuthoringPlanned, Actor: session.ID, Reason: spec.reason,
		Contract: contract, ContractSHA256: hex.EncodeToString(contractSHA[:]), RecordedAt: now,
		PreviousExecution: &prevState, FinalExecution: &finalState,
	}
	if err := s.Durability.CommitTaskCompletion(ctx, req); err != nil {
		return DebugTaskBootstrapReconciliationResult{}, fmt.Errorf("%s pre-execution reconciliation could not be durably recorded: %w", spec.taskID, err)
	}
	return reconcilePublicResult(spec, finalState, journals, false), nil
}

func (s *Service) readJournalEvidence(ctx context.Context, spec taskReconcileSpec) (taskPreExecutionJournalEvidence, error) {
	planner, err := s.JournalRead(ctx, JournalReadInput{Key: spec.plannerKey})
	if err != nil {
		return taskPreExecutionJournalEvidence{}, fmt.Errorf("read Planner authorization %s: %w", spec.plannerKey, err)
	}
	if err := validateJournalEntry(planner, spec.plannerKey, spec.plannerSeq, model.JournalStreamPlannerNotes, "planner"); err != nil {
		return taskPreExecutionJournalEvidence{}, err
	}
	var auth taskBootstrapPlannerJournalData
	if err := decodeStrict(planner.Data, &auth); err != nil {
		return taskPreExecutionJournalEvidence{}, err
	}
	if spec.plannerValid == nil || !spec.plannerValid(auth) {
		return taskPreExecutionJournalEvidence{}, fmt.Errorf("%s journal evidence does not match the authorized Planner decision", spec.plannerKey)
	}
	plannerDigest, err := journalEntryDigest(planner)
	if err != nil {
		return taskPreExecutionJournalEvidence{}, err
	}
	evidence := taskPreExecutionJournalEvidence{
		PlannerKey:    planner.ID,
		PlannerDigest: plannerDigest,
	}
	if spec.leadKey != "" {
		lead, err := s.JournalRead(ctx, JournalReadInput{Key: spec.leadKey})
		if err != nil {
			return taskPreExecutionJournalEvidence{}, fmt.Errorf("read Lead record %s: %w", spec.leadKey, err)
		}
		if err := validateJournalEntry(lead, spec.leadKey, spec.leadSeq, model.JournalStreamLeadFriction, "lead"); err != nil {
			return taskPreExecutionJournalEvidence{}, err
		}
		var review taskBootstrapLeadJournalData
		if err := decodeStrict(lead.Data, &review); err != nil {
			return taskPreExecutionJournalEvidence{}, err
		}
		if spec.leadValid == nil || !spec.leadValid(review) {
			return taskPreExecutionJournalEvidence{}, fmt.Errorf("%s journal evidence does not match the authorized Lead record", spec.leadKey)
		}
		leadDigest, err := journalEntryDigest(lead)
		if err != nil {
			return taskPreExecutionJournalEvidence{}, err
		}
		evidence.LeadKey = lead.ID
		evidence.LeadDigest = leadDigest
	}
	gates, err := s.readLeadGateEvidence(ctx, spec, planner)
	if err != nil {
		return taskPreExecutionJournalEvidence{}, err
	}
	for _, gate := range gates {
		digest, err := journalEntryDigest(gate)
		if err != nil {
			return taskPreExecutionJournalEvidence{}, err
		}
		evidence.GateKeys = append(evidence.GateKeys, gate.ID)
		evidence.GateDigests = append(evidence.GateDigests, digest)
	}
	return evidence, nil
}

func validateJournalEntry(entry model.JournalEntry, key string, sequence uint64, stream model.JournalStream, role string) error {
	if err := model.ValidateJournalEntry(entry); err != nil {
		return err
	}
	if entry.ID != key || entry.Sequence != sequence || entry.Stream != stream || entry.Role != role || entry.ProjectID != config.GTWProjectID {
		return fmt.Errorf("journal %s does not match the required %s authority", key, role)
	}
	return nil
}

func (s *Service) readTSK660JournalEvidence(ctx context.Context) (taskPreExecutionJournalEvidence, error) {
	return s.readJournalEvidence(ctx, tsk660ReconcileSpec)
}

func validateTSK660JournalEntry(entry model.JournalEntry, key string, sequence uint64, stream model.JournalStream, role string) error {
	return validateJournalEntry(entry, key, sequence, stream, role)
}

// readLeadGateEvidence requires durable Lead records written after the
// Planner authorization that independently document the review and the
// applicable gate verification of the exact implementation commit. Without
// them the reconciliation fails closed rather than terminalizing on Planner
// authorization alone.
func (s *Service) readLeadGateEvidence(ctx context.Context, spec taskReconcileSpec, planner model.JournalEntry) ([]model.JournalEntry, error) {
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
			if preExecutionLeadGateEntryValid(entry, planner, spec) {
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
		return nil, fmt.Errorf("%s Lead review and gate evidence for %s is not durably recorded", spec.taskID, spec.implCommit[:8])
	}
	return matches, nil
}

func (s *Service) readTSK660LeadGateEvidence(ctx context.Context, planner model.JournalEntry) ([]model.JournalEntry, error) {
	return s.readLeadGateEvidence(ctx, tsk660ReconcileSpec, planner)
}

func preExecutionLeadGateEntryValid(entry model.JournalEntry, planner model.JournalEntry, spec taskReconcileSpec) bool {
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
	if !strings.Contains(joined, spec.taskID) || !strings.Contains(joined, spec.implCommit[:7]) {
		return false
	}
	return slices.ContainsFunc(data.Evidence, func(item string) bool {
		lower := strings.ToLower(item)
		return strings.Contains(lower, "gate") || strings.Contains(lower, "test") || strings.Contains(lower, "verif")
	})
}

func tsk660LeadGateEntryValid(entry model.JournalEntry, planner model.JournalEntry) bool {
	return preExecutionLeadGateEntryValid(entry, planner, tsk660ReconcileSpec)
}

func (s *Service) readReconcilePhases(ctx context.Context, spec taskReconcileSpec) (taskPreExecutionPhases, error) {
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
		phases, err := s.Durability.ReadTaskExecutionPhases(ctx, config.GTWProjectID, spec.taskID, item.stage)
		if err != nil {
			return taskPreExecutionPhases{}, err
		}
		*item.target = phases
	}
	return result, nil
}

func (s *Service) readTSK660Phases(ctx context.Context) (taskPreExecutionPhases, error) {
	return s.readReconcilePhases(ctx, tsk660ReconcileSpec)
}

// reconcilePhasesDigest binds the complete ordered phase history — including
// comments and timestamps — into the reconciliation contract.
func reconcilePhasesDigest(phases taskPreExecutionPhases) (string, error) {
	all := make([]sqlitestore.TaskExecutionPhase, 0, 8)
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

func tsk660PhasesDigest(phases taskPreExecutionPhases) (string, error) {
	return reconcilePhasesDigest(phases)
}

// readLegacyVerification recognizes each spec's exact persisted verification
// receipt set (produced before the current Procedure-gate contract) as its
// historical gate evidence. Any other receipt shape, a missing or extra
// receipt, or an undecodable/mismatched record fails closed.
func (s *Service) readLegacyVerification(ctx context.Context, spec taskReconcileSpec, task model.TaskAuthoring) (taskPreExecutionVerificationProof, error) {
	rows, err := s.Durability.ReadTaskExecutionVerificationReceipts(ctx, config.GTWProjectID, spec.taskID)
	if err != nil {
		return taskPreExecutionVerificationProof{}, err
	}
	if len(rows) != len(spec.receipts) {
		return taskPreExecutionVerificationProof{}, fmt.Errorf("%s has %d verification receipts; exactly %d authorized historical receipts are required", spec.taskID, len(rows), len(spec.receipts))
	}
	var success taskPreExecutionVerificationProof
	receiptsRaw := make([]string, 0, len(rows))
	for index, want := range spec.receipts {
		row := rows[index]
		if row.OperationID != want.OperationID || row.AttemptRevision != want.Attempt || row.Outcome != want.Outcome {
			return taskPreExecutionVerificationProof{}, fmt.Errorf("%s verification receipt %d is not the authorized historical gate proof", spec.taskID, index)
		}
		receipt, err := sqlitestore.DecodeTaskExecutionVerificationLegacy(row.ReceiptJSON)
		if err != nil {
			return taskPreExecutionVerificationProof{}, fmt.Errorf("%s legacy verification receipt %d is undecodable: %w", spec.taskID, index, err)
		}
		if receipt.ProjectID != config.GTWProjectID || receipt.TaskID != spec.taskID || receipt.OperationID != want.OperationID || receipt.TaskRevision != spec.taskRevision || receipt.AttemptRevision != want.Attempt || receipt.TaskRevisionSHA256 != task.RevisionSHA256 || receipt.Outcome != want.Outcome || receipt.BaseHead != spec.mainBase || receipt.CandidateHead != want.CandidateHead || receipt.CandidateTree != want.CandidateTree || receipt.Branch != spec.branch || receipt.CodeReviewID != want.CodeReviewID || receipt.TestsReviewID != want.TestsReviewID || receipt.RebaseReviewID != want.RebaseReviewID {
			return taskPreExecutionVerificationProof{}, fmt.Errorf("%s verification receipt %d identity does not match the authorized historical gate proof", spec.taskID, index)
		}
		if want.Outcome == model.TaskExecutionVerificationSucceeded {
			// A succeeded receipt must carry a complete executed gate proof
			// bound to the authorized implementation tree.
			if len(receipt.Gates) == 0 {
				return taskPreExecutionVerificationProof{}, fmt.Errorf("%s succeeded verification receipt %d has no gate evidence", spec.taskID, index)
			}
			seenGates := map[string]bool{}
			for _, gate := range receipt.Gates {
				if gate.ID == "" || seenGates[gate.ID] || gate.Execution != "executed" || gate.ExitCode != 0 || gate.TreeID != spec.implTree {
					return taskPreExecutionVerificationProof{}, fmt.Errorf("%s legacy verification gate %q does not match the authorized historical gate proof", spec.taskID, gate.ID)
				}
				seenGates[gate.ID] = true
			}
			for _, required := range want.RequiredGates {
				if !seenGates[required] {
					return taskPreExecutionVerificationProof{}, fmt.Errorf("%s legacy verification is missing required gate %q", spec.taskID, required)
				}
			}
		}
		receiptsRaw = append(receiptsRaw, row.ReceiptJSON)
		if want.OperationID == spec.successReceipt {
			digest := sha256.Sum256([]byte(row.ReceiptJSON))
			success = taskPreExecutionVerificationProof{
				OperationID:   row.OperationID,
				CandidateHead: receipt.CandidateHead,
				ReceiptSHA256: hex.EncodeToString(digest[:]),
				CompletedAt:   receipt.CompletedAt,
			}
		}
	}
	if success.OperationID == "" {
		return taskPreExecutionVerificationProof{}, fmt.Errorf("%s lacks the authorized successful historical gate proof", spec.taskID)
	}
	allRaw, err := json.Marshal(receiptsRaw)
	if err != nil {
		return taskPreExecutionVerificationProof{}, err
	}
	setDigest := sha256.Sum256(allRaw)
	success.ReceiptsSHA256 = hex.EncodeToString(setDigest[:])
	return success, nil
}

func (s *Service) readTSK660LegacyVerification(ctx context.Context, task model.TaskAuthoring) (taskPreExecutionVerificationProof, error) {
	return s.readLegacyVerification(ctx, tsk660ReconcileSpec, task)
}

// validateReconcilePhases pins the exact real lifecycle against the spec's
// ordered phase set. Any extra or divergent phase fails closed.
func validateReconcilePhases(spec taskReconcileSpec, phases taskPreExecutionPhases) error {
	byStage := map[string][]sqlitestore.TaskExecutionPhase{
		"code": phases.code, "tests": phases.tests, "rebase": phases.rebase, "integration": phases.integration,
	}
	seen := map[string]bool{}
	for _, want := range spec.phases {
		seen[want.Stage] = true
	}
	for stage, rows := range byStage {
		expected := 0
		for _, want := range spec.phases {
			if want.Stage == stage {
				expected++
			}
		}
		if len(rows) != expected {
			return fmt.Errorf("%s %s-phase history does not match the authorized lifecycle", spec.taskID, stage)
		}
	}
	index := map[string]int{}
	for _, want := range spec.phases {
		rows := byStage[want.Stage]
		i := index[want.Stage]
		phase := rows[i]
		index[want.Stage] = i + 1
		if phase.ProjectID != config.GTWProjectID || phase.TaskID != spec.taskID || phase.ExecutionRevision != want.Revision || phase.Stage != want.Stage || phase.Status != want.Status || phase.Head != want.Head || phase.Branch != spec.branch || phase.TaskRevisionSHA256 == "" || phase.EventKind != want.EventKind || phase.Decision != want.Decision || (len(phase.Comment) == 0) != want.CommentEmpty || phase.CreatedAt.IsZero() {
			return fmt.Errorf("%s %s phase %d does not match the authorized lifecycle", spec.taskID, want.Stage, want.Revision)
		}
	}
	return nil
}

func validateTSK660Phases(phases taskPreExecutionPhases) error {
	return validateReconcilePhases(tsk660ReconcileSpec, phases)
}

func validateReconcileState(spec taskReconcileSpec, task model.TaskAuthoring, state model.TaskExecutionState, found bool, phases taskPreExecutionPhases, phasesSHA256 string, verification taskPreExecutionVerificationProof, event sqlitestore.TaskLifecycleEvent, eventFound, resetFound bool, journals taskPreExecutionJournalEvidence) (taskPreExecutionPhaseEvidence, bool, error) {
	if task.ID != spec.taskID || task.ProjectID != config.GTWProjectID || task.Revision != spec.taskRevision {
		return taskPreExecutionPhaseEvidence{}, false, fmt.Errorf("%s authoring record does not match the authorized identity", spec.taskID)
	}
	if resetFound {
		return taskPreExecutionPhaseEvidence{}, false, fmt.Errorf("%s carries reset evidence the historical lifecycle never produced", spec.taskID)
	}
	if err := validateReconcilePhases(spec, phases); err != nil {
		return taskPreExecutionPhaseEvidence{}, false, err
	}
	if !found {
		return taskPreExecutionPhaseEvidence{}, false, fmt.Errorf("%s has no durable execution state to reconcile", spec.taskID)
	}
	if err := model.ValidateTaskExecutionState(state); err != nil {
		return taskPreExecutionPhaseEvidence{}, false, err
	}
	if state.TaskID != spec.taskID || state.ProjectID != config.GTWProjectID || state.TaskRevision != spec.taskRevision || state.TaskRevisionSHA256 != task.RevisionSHA256 || state.Stage != spec.stage || state.Worktree != spec.worktree || state.BaseHead != spec.mainBase || state.Head != spec.stateHead || state.Branch != spec.branch || state.Agent != config.GTWWorkerAgentID {
		return taskPreExecutionPhaseEvidence{}, false, fmt.Errorf("%s execution state is not the exact authorized lifecycle", spec.taskID)
	}
	switch {
	case !eventFound:
		if task.Status != model.TaskAuthoringPlanned || state.Status != model.TaskExecutionIntegrated || state.ExecutionRevision != spec.integratedRevision {
			return taskPreExecutionPhaseEvidence{}, false, fmt.Errorf("%s without completion evidence must be planned with the exact integrated state", spec.taskID)
		}
		return taskPreExecutionPhaseEvidence{}, false, nil
	default:
		if task.Status != model.TaskAuthoringDone || state.Status != model.TaskExecutionDone || state.ExecutionRevision != spec.completedRevision || !state.UpdatedAt.Equal(event.RecordedAt.UTC()) {
			return taskPreExecutionPhaseEvidence{}, false, fmt.Errorf("%s completion evidence does not bind the reconciled state", spec.taskID)
		}
		if event.EventKind != sqlitestore.TaskLifecycleEventKindComplete || event.ProjectID != config.GTWProjectID || event.TaskID != spec.taskID || event.Revision != int64(spec.taskRevision) || event.FromStatus != model.TaskAuthoringPlanned || event.ToStatus != model.TaskAuthoringDone {
			return taskPreExecutionPhaseEvidence{}, false, fmt.Errorf("%s completion event is not the authorized reconciliation", spec.taskID)
		}
		var evidence taskPreExecutionPhaseEvidence
		if err := decodeStrict(event.Contract, &evidence); err != nil {
			return taskPreExecutionPhaseEvidence{}, false, fmt.Errorf("%s completion contract is malformed: %w", spec.taskID, err)
		}
		canonical, err := json.Marshal(evidence)
		if err != nil || string(canonical) != string(event.Contract) {
			return taskPreExecutionPhaseEvidence{}, false, fmt.Errorf("%s completion contract is not canonical", spec.taskID)
		}
		if !reconcileEvidenceValid(spec, evidence, task, verification, phasesSHA256, journals) {
			return taskPreExecutionPhaseEvidence{}, false, fmt.Errorf("%s reconciliation contract conflicts with current authority", spec.taskID)
		}
		return evidence, true, nil
	}
}

func validateTSK660State(task model.TaskAuthoring, state model.TaskExecutionState, found bool, phases taskPreExecutionPhases, phasesSHA256 string, verification taskPreExecutionVerificationProof, event sqlitestore.TaskLifecycleEvent, eventFound, resetFound bool, journals taskPreExecutionJournalEvidence) (taskPreExecutionPhaseEvidence, bool, error) {
	return validateReconcileState(tsk660ReconcileSpec, task, state, found, phases, phasesSHA256, verification, event, eventFound, resetFound, journals)
}

func reconcileEvidenceValid(spec taskReconcileSpec, evidence taskPreExecutionPhaseEvidence, task model.TaskAuthoring, verification taskPreExecutionVerificationProof, phasesSHA256 string, journals taskPreExecutionJournalEvidence) bool {
	return evidence.SchemaVersion == 1 && evidence.Kind == spec.kind && evidence.TaskID == spec.taskID && evidence.TaskRevision == spec.taskRevision && evidence.TaskRevisionSHA256 == task.RevisionSHA256 && evidence.PlannerAuthorization == journals.PlannerKey && evidence.PlannerEvidenceSHA256 == journals.PlannerDigest && evidence.LeadReviewEvidence == journals.LeadKey && evidence.LeadReviewSHA256 == journals.LeadDigest && slices.Equal(evidence.LeadGateEvidence, journals.GateKeys) && slices.Equal(evidence.LeadGateEvidenceSHA256, journals.GateDigests) && evidence.ImplementationCommit == spec.implCommit && evidence.ImplementationTree == spec.implTree && evidence.MainBase == spec.mainBase && model.ValidateCommitSHA(evidence.CanonicalMainHead) == nil && evidence.ExecutionRevision == spec.integratedRevision && evidence.PhasesSHA256 == phasesSHA256 && evidence.ReceiptsSHA256 == verification.ReceiptsSHA256 && evidence.LegacyVerificationOperation == verification.OperationID && evidence.LegacyVerificationSHA256 == verification.ReceiptSHA256 && evidence.LegacyVerificationCandidate == verification.CandidateHead && !evidence.ExecutionStateMinted && !evidence.PhaseMinted && !evidence.VerificationReceiptMinted
}

func tsk660EvidenceValid(evidence taskPreExecutionPhaseEvidence, task model.TaskAuthoring, verification taskPreExecutionVerificationProof, phasesSHA256 string, journals taskPreExecutionJournalEvidence) bool {
	return reconcileEvidenceValid(tsk660ReconcileSpec, evidence, task, verification, phasesSHA256, journals)
}

func reconcileCompletionContract(spec taskReconcileSpec, task model.TaskAuthoring, journals taskPreExecutionJournalEvidence, verification taskPreExecutionVerificationProof, phasesSHA256, canonicalMain string) ([]byte, error) {
	evidence := taskPreExecutionPhaseEvidence{
		SchemaVersion:               1,
		Kind:                        spec.kind,
		TaskID:                      spec.taskID,
		TaskRevision:                task.Revision,
		TaskRevisionSHA256:          task.RevisionSHA256,
		PlannerAuthorization:        journals.PlannerKey,
		PlannerEvidenceSHA256:       journals.PlannerDigest,
		LeadReviewEvidence:          journals.LeadKey,
		LeadReviewSHA256:            journals.LeadDigest,
		LeadGateEvidence:            journals.GateKeys,
		LeadGateEvidenceSHA256:      journals.GateDigests,
		ImplementationCommit:        spec.implCommit,
		ImplementationTree:          spec.implTree,
		MainBase:                    spec.mainBase,
		CanonicalMainHead:           canonicalMain,
		ExecutionRevision:           spec.integratedRevision,
		PhasesSHA256:                phasesSHA256,
		ReceiptsSHA256:              verification.ReceiptsSHA256,
		LegacyVerificationOperation: verification.OperationID,
		LegacyVerificationSHA256:    verification.ReceiptSHA256,
		LegacyVerificationCandidate: verification.CandidateHead,
	}
	return json.Marshal(evidence)
}

func tsk660CompletionContract(task model.TaskAuthoring, journals taskPreExecutionJournalEvidence, verification taskPreExecutionVerificationProof, phasesSHA256, canonicalMain string) ([]byte, error) {
	return reconcileCompletionContract(tsk660ReconcileSpec, task, journals, verification, phasesSHA256, canonicalMain)
}

func (s *Service) proveLandedSource(ctx context.Context, spec taskReconcileSpec, project config.ProjectConfig) (string, error) {
	canonicalMain, err := s.Git.RemoteBranchHead(ctx, project, "main")
	if err != nil {
		return "", err
	}
	if err := s.Git.MaterializeRemoteBranchObjects(ctx, project, "main"); err != nil {
		return "", err
	}
	tree, parents, err := s.Git.InspectTaskIntegrationCommit(ctx, project, spec.implCommit)
	if err != nil {
		return "", err
	}
	if tree != spec.implTree || len(parents) != 1 || parents[0] != spec.mainBase || model.ValidateCommitSHA(canonicalMain) != nil {
		return "", fmt.Errorf("%s implementation commit identity does not match the authorized base and tree", spec.taskID)
	}
	landed, err := s.Git.IsAncestor(ctx, project.Root, spec.implCommit, canonicalMain)
	if err != nil {
		return "", err
	}
	if !landed {
		return "", fmt.Errorf("%s implementation commit %s is not an ancestor of canonical main", spec.taskID, spec.implCommit[:8])
	}
	finalMain, err := s.Git.RemoteBranchHead(ctx, project, "main")
	if err != nil {
		return "", err
	}
	if finalMain != canonicalMain {
		return "", fmt.Errorf("canonical main moved during %s source proof; retry is required", spec.taskID)
	}
	return canonicalMain, nil
}

func (s *Service) proveRecordedMain(ctx context.Context, spec taskReconcileSpec, project config.ProjectConfig, recordedMain, canonicalMain string) error {
	if model.ValidateCommitSHA(recordedMain) != nil || model.ValidateCommitSHA(canonicalMain) != nil {
		return fmt.Errorf("recorded pre-execution reconciliation head is invalid")
	}
	candidateAncestor, err := s.Git.IsAncestor(ctx, project.Root, spec.implCommit, recordedMain)
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

func sameJournalEvidence(left, right taskPreExecutionJournalEvidence) bool {
	return left.PlannerKey == right.PlannerKey && left.PlannerDigest == right.PlannerDigest && left.LeadKey == right.LeadKey && left.LeadDigest == right.LeadDigest && slices.Equal(left.GateKeys, right.GateKeys) && slices.Equal(left.GateDigests, right.GateDigests)
}

func sameTSK660JournalEvidence(left, right taskPreExecutionJournalEvidence) bool {
	return sameJournalEvidence(left, right)
}

func journalEntryDigest(entry model.JournalEntry) (string, error) {
	raw, err := json.Marshal(entry)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

func tsk660JournalDigest(entry model.JournalEntry) (string, error) {
	return journalEntryDigest(entry)
}

func reconcilePublicResult(spec taskReconcileSpec, state model.TaskExecutionState, journals taskPreExecutionJournalEvidence, alreadyReconciled bool) DebugTaskBootstrapReconciliationResult {
	var evidence []string
	if journals.LeadKey != "" {
		evidence = append(evidence, journals.LeadKey)
	}
	evidence = append(evidence, journals.PlannerKey)
	evidence = append(evidence, journals.GateKeys...)
	return DebugTaskBootstrapReconciliationResult{
		Key:               spec.taskID,
		Status:            model.TaskExecutionIntegrated,
		ExecutionRevision: state.ExecutionRevision,
		IntegrationHead:   spec.implCommit[:8],
		Evidence:          evidence,
		AlreadyReconciled: alreadyReconciled,
	}
}

func tsk660PublicResult(state model.TaskExecutionState, journals taskPreExecutionJournalEvidence, alreadyReconciled bool) DebugTaskBootstrapReconciliationResult {
	return reconcilePublicResult(tsk660ReconcileSpec, state, journals, alreadyReconciled)
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
