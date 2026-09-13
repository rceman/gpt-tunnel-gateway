package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/entity"
	"github.com/rceman/gpt-tunnel-gateway/internal/gitx"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

const taskExecutionHistoricalPhasePrefix = "historical-integration:"

// taskExecutionHistoricalPhaseEvidence is the private durable envelope stored
// in the integration phase comment; it is never projected publicly.
type taskExecutionHistoricalPhaseEvidence struct {
	SchemaVersion   int    `json:"schema_version"`
	Mode            string `json:"mode"`
	Profile         string `json:"profile"`
	Evidence        string `json:"evidence"`
	IntegrationHead string `json:"integration_head"`
	CandidateHead   string `json:"candidate_head,omitempty"`
	MainBase        string `json:"main_base,omitempty"`
	Comment         string `json:"comment,omitempty"`
}

const taskExecutionHistoricalFullGatesPrefix = "task_execution_full_gates:"

// taskExecutionHistoricalFullGates is the strict private Journal fact
// envelope recording the full-gate proof bound to the candidate head.
type taskExecutionHistoricalFullGates struct {
	SchemaVersion      int                          `json:"schema_version"`
	TaskRevisionSHA256 string                       `json:"task_revision_sha256"`
	CandidateHead      string                       `json:"candidate_head"`
	CandidateTree      string                       `json:"candidate_tree"`
	GateProfileSHA256  string                       `json:"gate_profile_sha256"`
	Gates              []model.CompletionGateResult `json:"gates"`
}

func taskExecutionHistoricalPhaseComment(in TaskExecutionIntegrateInput) (string, error) {
	h := in.Historical
	raw, err := json.Marshal(taskExecutionHistoricalPhaseEvidence{
		SchemaVersion:   1,
		Mode:            "historical",
		Profile:         h.Profile,
		Evidence:        h.Evidence,
		IntegrationHead: h.IntegrationHead,
		CandidateHead:   h.CandidateHead,
		MainBase:        h.MainBase,
		Comment:         strings.TrimSpace(in.Comment),
	})
	if err != nil {
		return "", err
	}
	return taskExecutionHistoricalPhasePrefix + string(raw), nil
}

// taskExecutionHistoricalIntegrate recognizes an integration commit that
// already landed on canonical main through immutable Journal evidence backed
// by a durable active Planner session. It performs read-only Git proofs,
// never publishes, never reconciles, and never writes a verification receipt;
// the only mutation is the atomic state+phase transition to integrated.
func (s *Service) taskExecutionHistoricalIntegrate(ctx context.Context, in TaskExecutionIntegrateInput, project config.ProjectConfig, state model.TaskExecutionState) (TaskExecutionPublicOutput, error) {
	h := in.Historical
	envelopeComment, err := taskExecutionHistoricalPhaseComment(in)
	if err != nil {
		return TaskExecutionPublicOutput{}, err
	}
	phases, err := s.Durability.ReadTaskExecutionPhases(ctx, in.ProjectID, in.Key, "integration")
	if err != nil {
		return TaskExecutionPublicOutput{}, err
	}
	if state.Status == model.TaskExecutionIntegrated {
		if len(phases) != 1 || phases[0].Comment != envelopeComment || phases[0].Head != h.IntegrationHead || phases[0].Status != model.TaskExecutionIntegrated || phases[0].Stage != "integration" || phases[0].EventKind != "integration" || phases[0].Decision != "accept" || phases[0].TaskRevisionSHA256 != state.TaskRevisionSHA256 || phases[0].Branch != state.Branch || phases[0].ExecutionRevision != state.ExecutionRevision {
			return TaskExecutionPublicOutput{}, fmt.Errorf("recorded Task integration evidence conflicts with the historical recognition request; explicit evidence reconciliation is required")
		}
		return taskExecutionPublicOutput(state), nil
	}
	switch h.Profile {
	case "legacy":
		if state.Status != model.TaskExecutionReadyForVerification && state.Status != model.TaskExecutionVerified {
			return TaskExecutionPublicOutput{}, fmt.Errorf("Task is not ready for historical integration recognition")
		}
	case "bootstrap_full":
		if state.Status != model.TaskExecutionDispatched && state.Status != model.TaskExecutionReadyForVerification && state.Status != model.TaskExecutionVerified {
			return TaskExecutionPublicOutput{}, fmt.Errorf("Task is not ready for historical integration recognition")
		}
	}
	if len(phases) != 0 {
		return TaskExecutionPublicOutput{}, fmt.Errorf("Task records integration phase evidence without an integrated state; explicit evidence reconciliation is required")
	}
	identifiers, err := s.ProjectIdentifiersRead(ctx, in.ProjectID)
	if err != nil {
		return TaskExecutionPublicOutput{}, err
	}
	code, _, err := model.ParseJournalID(h.Evidence)
	if err != nil || code != identifiers.ProjectCode {
		return TaskExecutionPublicOutput{}, fmt.Errorf("historical evidence does not bind this project")
	}
	var event model.OperatorJournalEvent
	record, err := s.entityRegistry(in.ProjectID).ReadInto(ctx, entity.JournalFamily, h.Evidence, &event)
	if err != nil {
		return TaskExecutionPublicOutput{}, fmt.Errorf("historical evidence Journal record: %w", err)
	}
	if _, err := validateOperatorEventPathIdentity(record.Path, s.operatorEventsPrefix(in.ProjectID), event, in.ProjectID, identifiers.ProjectCode); err != nil {
		return TaskExecutionPublicOutput{}, fmt.Errorf("historical evidence Journal record: %w", err)
	}
	if event.SessionID == nil {
		return TaskExecutionPublicOutput{}, fmt.Errorf("historical evidence lacks durable Planner Session authority")
	}
	session, err := durableSession.NewStoreWithDurability(s.Durability).Get(*event.SessionID)
	// The session must demonstrably exist when the canonical Journal event was
	// recorded: a later-created session cannot retroactively grant authority.
	if err != nil || session.Role != durableSession.RolePlanner || session.Status != durableSession.StatusActive || session.ProjectID != in.ProjectID || session.ProjectCode != identifiers.ProjectCode || session.CreatedAt.After(event.RecordedAt) || session.StartedAt.After(event.RecordedAt) {
		return TaskExecutionPublicOutput{}, fmt.Errorf("historical evidence does not carry durable active Planner Session authority for this project")
	}
	if h.Profile == "bootstrap_full" && event.Kind != model.OperatorTaskReview {
		return TaskExecutionPublicOutput{}, fmt.Errorf("historical bootstrap evidence is not a task review Journal event")
	}
	if !slices.Contains(event.References.Tasks, in.Key) {
		return TaskExecutionPublicOutput{}, fmt.Errorf("historical evidence does not reference the Task")
	}
	if !slices.Contains(event.References.Commits, h.IntegrationHead) {
		return TaskExecutionPublicOutput{}, fmt.Errorf("historical evidence does not reference the integration commit")
	}
	if h.Profile == "bootstrap_full" && (!slices.Contains(event.References.Commits, h.CandidateHead) || !slices.Contains(event.References.Commits, h.MainBase)) {
		return TaskExecutionPublicOutput{}, fmt.Errorf("historical evidence does not reference the candidate and base commits")
	}
	records, err := s.entityRegistry(in.ProjectID).ListRecords(ctx, entity.Query{Family: entity.JournalFamily})
	if err != nil {
		return TaskExecutionPublicOutput{}, err
	}
	eventsPrefix := s.operatorEventsPrefix(in.ProjectID)
	evidenceStable := false
	for _, rec := range records {
		var other model.OperatorJournalEvent
		if err := decodeStrict(rec.Bytes, &other); err != nil {
			return TaskExecutionPublicOutput{}, fmt.Errorf("invalid Journal record %s: %w", rec.Path, err)
		}
		if _, err := validateOperatorEventPathIdentity(rec.Path, eventsPrefix, other, in.ProjectID, identifiers.ProjectCode); err != nil {
			return TaskExecutionPublicOutput{}, fmt.Errorf("invalid Journal record %s: %w", rec.Path, err)
		}
		if rec.ID == record.ID {
			if !bytes.Equal(rec.Bytes, record.Bytes) {
				return TaskExecutionPublicOutput{}, fmt.Errorf("historical evidence Journal record changed during admission")
			}
			evidenceStable = true
		}
		if other.SupersedesEventID == event.ID {
			return TaskExecutionPublicOutput{}, fmt.Errorf("historical evidence Journal record is superseded")
		}
	}
	if !evidenceStable {
		return TaskExecutionPublicOutput{}, fmt.Errorf("historical evidence Journal record disappeared during admission")
	}
	branch := strings.TrimPrefix(project.DefaultBranch, "refs/heads/")
	canonical, err := s.Git.RemoteBranchHead(ctx, project, branch)
	if err != nil {
		return TaskExecutionPublicOutput{}, err
	}
	if err := s.Git.MaterializeRemoteBranchObjects(ctx, project, branch); err != nil {
		return TaskExecutionPublicOutput{}, err
	}
	reread, err := s.Git.RemoteBranchHead(ctx, project, branch)
	if err != nil {
		return TaskExecutionPublicOutput{}, err
	}
	if reread != canonical {
		return TaskExecutionPublicOutput{}, fmt.Errorf("canonical remote head moved during historical recognition; retry is required")
	}
	if h.IntegrationHead != canonical {
		ancestor, err := s.Git.IsAncestor(ctx, project.Root, h.IntegrationHead, canonical)
		if err != nil {
			return TaskExecutionPublicOutput{}, err
		}
		if !ancestor {
			return TaskExecutionPublicOutput{}, fmt.Errorf("historical integration commit is not on canonical main")
		}
	}
	var bootstrapSnapshot verificationGateSnapshot
	if h.Profile == "bootstrap_full" {
		err = s.taskExecutionHistoricalBootstrapProof(ctx, in.ProjectID, state, h, event, &bootstrapSnapshot)
		if err != nil {
			return TaskExecutionPublicOutput{}, err
		}
	}
	if s.taskIntegrationFaultHook != nil {
		if err := s.taskIntegrationFaultHook(ctx, "historical_proven"); err != nil {
			return TaskExecutionPublicOutput{}, err
		}
	}
	finalCanonical, err := s.Git.RemoteBranchHead(ctx, project, branch)
	if err != nil {
		return TaskExecutionPublicOutput{}, err
	}
	if finalCanonical != canonical {
		return TaskExecutionPublicOutput{}, fmt.Errorf("canonical remote head moved before historical integration transition")
	}
	if h.Profile == "bootstrap_full" {
		_, finalSnapshot, err := s.taskExecutionHistoricalBootstrapLaneSnapshot(ctx, project, in.ProjectID, state, h)
		if err != nil {
			return TaskExecutionPublicOutput{}, err
		}
		if finalSnapshot.branch != bootstrapSnapshot.branch || finalSnapshot.head != bootstrapSnapshot.head || finalSnapshot.tree != bootstrapSnapshot.tree || !finalSnapshot.clean {
			return TaskExecutionPublicOutput{}, fmt.Errorf("assigned Task lane changed after historical bootstrap proof")
		}
	}
	state.Status = model.TaskExecutionIntegrated
	state.ExecutionRevision++
	state.UpdatedAt = s.durableNow()
	phase := sqlitestore.TaskExecutionPhase{
		TaskID: state.TaskID, ProjectID: state.ProjectID, ExecutionRevision: state.ExecutionRevision,
		Stage: "integration", Status: model.TaskExecutionIntegrated, Head: h.IntegrationHead,
		Branch: state.Branch, TaskRevisionSHA256: state.TaskRevisionSHA256,
		EventKind: "integration", Decision: "accept", Comment: envelopeComment, CreatedAt: state.UpdatedAt,
	}
	if err := s.Durability.TransitionTaskExecutionState(ctx, state, state.ExecutionRevision-1, phase); err != nil {
		return TaskExecutionPublicOutput{}, fmt.Errorf("Task historical integration state remains pending; retry is required: %w", err)
	}
	return taskExecutionPublicOutput(state), nil
}

// taskExecutionHistoricalBootstrapLaneSnapshot captures the exact clean
// assigned Task lane bound to the historical candidate: only the server-owned
// worktree path, read-only status/tree capture, no mutation.
func (s *Service) taskExecutionHistoricalBootstrapLaneSnapshot(ctx context.Context, project config.ProjectConfig, projectID string, state model.TaskExecutionState, h *TaskExecutionHistoricalIntegrationInput) (config.ProjectConfig, verificationGateSnapshot, error) {
	lanePath, err := gitx.TaskWorktreePath(s.Config.StateDir, projectID, state.TaskID)
	if err != nil {
		return config.ProjectConfig{}, verificationGateSnapshot{}, err
	}
	lane := project
	lane.Root = lanePath
	snapshot, err := s.captureVerificationSnapshot(ctx, lane)
	if err != nil {
		return config.ProjectConfig{}, verificationGateSnapshot{}, err
	}
	if !snapshot.clean || snapshot.branch != state.Branch || snapshot.head != h.CandidateHead || model.ValidateCommitSHA(h.CandidateHead) != nil || model.ValidateCommitSHA(snapshot.tree) != nil {
		return config.ProjectConfig{}, verificationGateSnapshot{}, fmt.Errorf("historical bootstrap proof does not bind the exact clean assigned Task lane")
	}
	return lane, snapshot, nil
}

// taskExecutionHistoricalBootstrapProof adds the bootstrap_full-only proofs:
// assigned candidate and frozen base binding, exact parent/tree equality, and
// the strict Journal full-gates fact envelope bound to the current gate
// profile. It returns the initially proven lane snapshot for the final
// post-proof recapture comparison.
func (s *Service) taskExecutionHistoricalBootstrapProof(ctx context.Context, projectID string, state model.TaskExecutionState, h *TaskExecutionHistoricalIntegrationInput, event model.OperatorJournalEvent, proven *verificationGateSnapshot) error {
	if h.MainBase != state.BaseHead {
		return fmt.Errorf("historical bootstrap proof does not bind the frozen base head")
	}
	project, err := s.EffectiveProjectConfig(projectID)
	if err != nil {
		return err
	}
	lane, snapshot, err := s.taskExecutionHistoricalBootstrapLaneSnapshot(ctx, project, projectID, state, h)
	if err != nil {
		return err
	}
	if state.Status == model.TaskExecutionReadyForVerification || state.Status == model.TaskExecutionVerified {
		if state.Head != h.CandidateHead {
			return fmt.Errorf("historical bootstrap proof does not bind the frozen candidate head")
		}
	}
	basedOn, err := s.Git.IsAncestor(ctx, lane.Root, state.BaseHead, h.CandidateHead)
	if err != nil {
		return err
	}
	if !basedOn {
		return fmt.Errorf("historical bootstrap proof candidate is not based on the assigned base")
	}
	tree, parents, err := s.Git.InspectTaskIntegrationCommit(ctx, project, h.IntegrationHead)
	if err != nil {
		return err
	}
	if len(parents) != 1 || parents[0] != h.MainBase {
		return fmt.Errorf("historical integration commit is not an exact child of the verified base")
	}
	candidateTree := snapshot.tree
	if tree != candidateTree {
		return fmt.Errorf("historical integration commit tree does not match the reviewed candidate tree")
	}
	required, profile, err := s.taskExecutionGateProfile(ctx, projectID)
	if err != nil {
		return err
	}
	var encoded string
	for _, fact := range event.Content.Facts {
		if strings.HasPrefix(fact, taskExecutionHistoricalFullGatesPrefix) {
			if encoded != "" {
				return fmt.Errorf("historical evidence carries duplicate full-gates facts")
			}
			encoded = fact
		}
	}
	if encoded == "" {
		return fmt.Errorf("historical evidence lacks the full-gates fact")
	}
	var gates taskExecutionHistoricalFullGates
	if err := decodeStrict([]byte(strings.TrimPrefix(encoded, taskExecutionHistoricalFullGatesPrefix)), &gates); err != nil {
		return fmt.Errorf("historical full-gates fact is malformed: %w", err)
	}
	if gates.SchemaVersion != 1 {
		return fmt.Errorf("historical full-gates fact has an unsupported schema version")
	}
	if gates.TaskRevisionSHA256 != state.TaskRevisionSHA256 || model.ValidateSHA256(gates.TaskRevisionSHA256) != nil {
		return fmt.Errorf("historical full-gates fact does not bind the Task revision digest")
	}
	if gates.CandidateHead != h.CandidateHead {
		return fmt.Errorf("historical full-gates fact does not bind the candidate head")
	}
	if gates.CandidateTree != candidateTree || model.ValidateCommitSHA(gates.CandidateTree) != nil {
		return fmt.Errorf("historical full-gates fact does not bind the candidate tree")
	}
	if gates.GateProfileSHA256 != profile || model.ValidateSHA256(gates.GateProfileSHA256) != nil {
		return fmt.Errorf("historical full-gates fact does not bind the current gate profile")
	}
	if err := model.ValidateServerGateEvidence(gates.Gates); err != nil {
		return fmt.Errorf("historical full-gates fact is malformed: %w", err)
	}
	if len(gates.Gates) != len(required) {
		return fmt.Errorf("historical full-gates fact does not cover the required gates")
	}
	seen := map[string]bool{}
	for _, gate := range gates.Gates {
		if !slices.Contains(required, gate.ID) || seen[gate.ID] {
			return fmt.Errorf("historical full-gates fact has unexpected gate evidence")
		}
		seen[gate.ID] = true
		if gate.Execution != "executed" || gate.ExitCode != 0 || gate.TreeID != candidateTree || model.ValidateSHA256(gate.ContractDigest) != nil || model.ValidateSHA256(gate.ReceiptDigest) != nil {
			return fmt.Errorf("historical full-gates fact does not prove a passing tree-bound executed gate")
		}
	}
	*proven = snapshot
	return nil
}
