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
	tsk660TaskID                      = "GTW-TSK660"
	tsk660TaskRevision                = 6
	tsk660ExecutionRevision           = 1
	tsk660PhasePrefix                 = "task-pre-execution-reconciliation:"
	tsk660MainBase                    = "f09d0834d125362d63e1d12b361ad34c63e50d8d"
	tsk660ImplementationCommit        = "899abc90157ee5b4e8b6b27e4284e0c841ccbb17"
	tsk660ImplementationTree          = "e9073e7db4e6e8a876b1d88eed1ab1760b5ea4a0"
	tsk660ExecutionBranch             = "task/GTW-TSK660-task-implement-milestone-roadmap-membership-and-"
	tsk660ExecutionWorktree           = "WT-TSK660-f09d0834"
	tsk660PlannerJournalKey           = "GTW-JRN16"
	tsk660PlannerJournalSeq           = uint64(16)
	tsk660LeadJournalKey              = "GTW-JRN14"
	tsk660LeadJournalSeq              = uint64(14)
	tsk660LegacyVerificationOperation = "GTW-OPR3793"
	tsk660LegacyVerificationCandidate = "8a7ec281b349599d26f803bb8b3f1bea557d0570"
	tsk660LegacyVerificationReviewID  = int64(424)
)

type taskPreExecutionPhaseEvidence struct {
	SchemaVersion                    int      `json:"schema_version"`
	Kind                             string   `json:"kind"`
	TaskID                           string   `json:"task"`
	TaskRevision                     int      `json:"task_revision"`
	TaskRevisionSHA256               string   `json:"task_revision_sha256"`
	PlannerAuthorization             string   `json:"planner_authorization"`
	PlannerEvidenceSHA256            string   `json:"planner_evidence_sha256"`
	LeadReviewEvidence               string   `json:"lead_review_evidence"`
	LeadReviewSHA256                 string   `json:"lead_review_sha256"`
	LeadGateEvidence                 []string `json:"lead_gate_evidence"`
	LeadGateEvidenceSHA256           []string `json:"lead_gate_evidence_sha256"`
	ImplementationCommit             string   `json:"implementation_commit"`
	ImplementationTree               string   `json:"implementation_tree"`
	MainBase                         string   `json:"main_base"`
	CanonicalMainHead                string   `json:"canonical_main_head"`
	LegacyVerificationOperation      string   `json:"legacy_verification_operation"`
	LegacyVerificationSHA256         string   `json:"legacy_verification_sha256"`
	LegacyVerificationCandidate      string   `json:"legacy_verification_candidate"`
	NormalExecutionStateExisted      bool     `json:"normal_execution_state_existed"`
	NormalSubmitPhaseCreated         bool     `json:"normal_submit_phase_created"`
	NormalReviewPhaseCreated         bool     `json:"normal_review_phase_created"`
	NormalVerificationReceiptCreated bool     `json:"normal_verification_receipt_created"`
}

type taskPreExecutionVerificationProof struct {
	OperationID   string
	CandidateHead string
	ReceiptSHA256 string
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
// GTW-TSK660, whose implementation commit landed on canonical main before the
// current Task execution lifecycle. The Task has no durable execution state,
// so this path mints the minimum integrated execution record plus one
// integration phase that explicitly encodes the historical pre-execution
// reconciliation evidence. It creates no Worker dispatch, submit, review, or
// verification receipts and mutates no repository or Shared/Hub state.
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

	task, err := s.readSharedTask(ctx, config.GTWProjectID, tsk660TaskID)
	if err != nil {
		return DebugTaskBootstrapReconciliationResult{}, err
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
	verification, err := s.readTSK660LegacyVerification(ctx, task)
	if err != nil {
		return DebugTaskBootstrapReconciliationResult{}, err
	}
	_, resetFound, err := s.Durability.ReadLatestTaskExecutionResetPhase(ctx, config.GTWProjectID, tsk660TaskID)
	if err != nil {
		return DebugTaskBootstrapReconciliationResult{}, err
	}
	existing, alreadyReconciled, err := validateTSK660State(task, state, found, phases, verification, resetFound, journals)
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

	comment, err := tsk660PhaseComment(task, journals, verification, canonicalMain)
	if err != nil {
		return DebugTaskBootstrapReconciliationResult{}, err
	}
	freshTask, err := s.readSharedTask(ctx, config.GTWProjectID, tsk660TaskID)
	if err != nil {
		return DebugTaskBootstrapReconciliationResult{}, err
	}
	if freshTask.Revision != task.Revision || freshTask.RevisionSHA256 != task.RevisionSHA256 || freshTask.Status != task.Status {
		return DebugTaskBootstrapReconciliationResult{}, fmt.Errorf("TSK660 authoring revision changed during pre-execution admission")
	}
	freshJournals, err := s.readTSK660JournalEvidence(ctx)
	if err != nil || !sameTSK660JournalEvidence(freshJournals, journals) {
		if err != nil {
			return DebugTaskBootstrapReconciliationResult{}, err
		}
		return DebugTaskBootstrapReconciliationResult{}, fmt.Errorf("TSK660 journal evidence changed during pre-execution admission")
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
	state = model.TaskExecutionState{
		TaskID: tsk660TaskID, ProjectID: config.GTWProjectID,
		TaskRevision: task.Revision, TaskRevisionSHA256: task.RevisionSHA256,
		Status: model.TaskExecutionIntegrated, Stage: "code",
		Worktree: tsk660ExecutionWorktree, BaseHead: tsk660MainBase, Head: tsk660MainBase,
		Branch: tsk660ExecutionBranch, Agent: config.GTWWorkerAgentID,
		ExecutionRevision: tsk660ExecutionRevision, UpdatedAt: now,
	}
	phase := sqlitestore.TaskExecutionPhase{
		TaskID: tsk660TaskID, ProjectID: config.GTWProjectID, ExecutionRevision: tsk660ExecutionRevision,
		Stage: "integration", Status: model.TaskExecutionIntegrated, Head: tsk660ImplementationCommit,
		Branch: state.Branch, TaskRevisionSHA256: state.TaskRevisionSHA256,
		EventKind: "integration", Decision: "accept", Comment: comment, CreatedAt: now,
	}
	if err := s.Durability.CreateTaskExecutionStateWithPhase(ctx, state, phase); err != nil {
		return DebugTaskBootstrapReconciliationResult{}, fmt.Errorf("TSK660 pre-execution reconciliation could not be durably recorded: %w", err)
	}
	return tsk660PublicResult(state, journals, false), nil
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
	}, nil
}

func validateTSK660State(task model.TaskAuthoring, state model.TaskExecutionState, found bool, phases taskPreExecutionPhases, verification taskPreExecutionVerificationProof, resetFound bool, journals taskPreExecutionJournalEvidence) (taskPreExecutionPhaseEvidence, bool, error) {
	if task.ID != tsk660TaskID || task.ProjectID != config.GTWProjectID || task.Revision != tsk660TaskRevision {
		return taskPreExecutionPhaseEvidence{}, false, fmt.Errorf("TSK660 authoring record does not match the authorized pre-execution identity")
	}
	if resetFound || len(phases.code) != 0 || len(phases.tests) != 0 || len(phases.rebase) != 0 {
		return taskPreExecutionPhaseEvidence{}, false, fmt.Errorf("TSK660 carries execution evidence the pre-execution landing never produced")
	}
	if !found {
		if task.Status != model.TaskAuthoringPlanned {
			return taskPreExecutionPhaseEvidence{}, false, fmt.Errorf("TSK660 has no execution state but its authoring record is not planned")
		}
		return taskPreExecutionPhaseEvidence{}, false, nil
	}
	if err := model.ValidateTaskExecutionState(state); err != nil {
		return taskPreExecutionPhaseEvidence{}, false, err
	}
	if task.Status != model.TaskAuthoringPlanned || state.TaskID != tsk660TaskID || state.ProjectID != config.GTWProjectID || state.TaskRevision != tsk660TaskRevision || state.TaskRevisionSHA256 != task.RevisionSHA256 || state.Status != model.TaskExecutionIntegrated || state.Stage != "code" || state.Worktree != tsk660ExecutionWorktree || state.BaseHead != tsk660MainBase || state.Head != tsk660MainBase || state.Branch != tsk660ExecutionBranch || state.Agent != config.GTWWorkerAgentID || state.ExecutionRevision != tsk660ExecutionRevision {
		return taskPreExecutionPhaseEvidence{}, false, fmt.Errorf("TSK660 execution state is not the exact authorized pre-execution reconciliation")
	}
	if len(phases.integration) != 1 {
		return taskPreExecutionPhaseEvidence{}, false, fmt.Errorf("TSK660 lacks the exact pre-execution reconciliation phase")
	}
	phase := phases.integration[0]
	if phase.ProjectID != config.GTWProjectID || phase.TaskID != tsk660TaskID || phase.ExecutionRevision != tsk660ExecutionRevision || phase.Stage != "integration" || phase.Status != model.TaskExecutionIntegrated || phase.Head != tsk660ImplementationCommit || phase.Branch != state.Branch || phase.TaskRevisionSHA256 != state.TaskRevisionSHA256 || phase.EventKind != "integration" || phase.Decision != "accept" || !phase.CreatedAt.Equal(state.UpdatedAt) || !strings.HasPrefix(phase.Comment, tsk660PhasePrefix) {
		return taskPreExecutionPhaseEvidence{}, false, fmt.Errorf("TSK660 integration phase is not the exact pre-execution reconciliation")
	}
	var evidence taskPreExecutionPhaseEvidence
	encoded := strings.TrimPrefix(phase.Comment, tsk660PhasePrefix)
	if err := decodeStrict([]byte(encoded), &evidence); err != nil {
		return taskPreExecutionPhaseEvidence{}, false, err
	}
	canonical, err := json.Marshal(evidence)
	if err != nil || string(canonical) != encoded {
		return taskPreExecutionPhaseEvidence{}, false, fmt.Errorf("TSK660 pre-execution phase envelope is not canonical")
	}
	if !tsk660PhaseEvidenceValid(evidence, task, verification, journals) {
		return taskPreExecutionPhaseEvidence{}, false, fmt.Errorf("TSK660 pre-execution reconciliation receipt conflicts with current authority")
	}
	return evidence, true, nil
}

func tsk660PhaseEvidenceValid(evidence taskPreExecutionPhaseEvidence, task model.TaskAuthoring, verification taskPreExecutionVerificationProof, journals taskPreExecutionJournalEvidence) bool {
	return evidence.SchemaVersion == 1 && evidence.Kind == "planner-authorized-pre-execution-reconciliation" && evidence.TaskID == tsk660TaskID && evidence.TaskRevision == tsk660TaskRevision && evidence.TaskRevisionSHA256 == task.RevisionSHA256 && evidence.PlannerAuthorization == journals.PlannerKey && evidence.PlannerEvidenceSHA256 == journals.PlannerDigest && evidence.LeadReviewEvidence == journals.LeadKey && evidence.LeadReviewSHA256 == journals.LeadDigest && slices.Equal(evidence.LeadGateEvidence, journals.GateKeys) && slices.Equal(evidence.LeadGateEvidenceSHA256, journals.GateDigests) && evidence.ImplementationCommit == tsk660ImplementationCommit && evidence.ImplementationTree == tsk660ImplementationTree && evidence.MainBase == tsk660MainBase && model.ValidateCommitSHA(evidence.CanonicalMainHead) == nil && evidence.LegacyVerificationOperation == verification.OperationID && evidence.LegacyVerificationSHA256 == verification.ReceiptSHA256 && evidence.LegacyVerificationCandidate == verification.CandidateHead && !evidence.NormalExecutionStateExisted && !evidence.NormalSubmitPhaseCreated && !evidence.NormalReviewPhaseCreated && !evidence.NormalVerificationReceiptCreated
}

func tsk660PhaseComment(task model.TaskAuthoring, journals taskPreExecutionJournalEvidence, verification taskPreExecutionVerificationProof, canonicalMain string) (string, error) {
	evidence := taskPreExecutionPhaseEvidence{
		SchemaVersion:               1,
		Kind:                        "planner-authorized-pre-execution-reconciliation",
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
		LegacyVerificationOperation: verification.OperationID,
		LegacyVerificationSHA256:    verification.ReceiptSHA256,
		LegacyVerificationCandidate: verification.CandidateHead,
	}
	raw, err := json.Marshal(evidence)
	if err != nil {
		return "", err
	}
	return tsk660PhasePrefix + string(raw), nil
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
