package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/rceman/gpt-tunnel-gateway/internal/entity"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

type taskCompleteLegacyAcceptanceInput struct {
	Criterion int      `json:"criterion"`
	Evidence  []string `json:"evidence"`
}

type TaskCompleteInput struct {
	ProjectID string `json:"project_id,omitempty"`
	Key       string `json:"key"`
	Mode      string `json:"mode"`
	Reason    string `json:"reason"`
	Review    string `json:"review"`
}

type TaskCompleteOutput struct {
	Key      string `json:"key"`
	Status   string `json:"status"`
	Revision int    `json:"revision"`
}

type taskCompletionContract struct {
	SchemaVersion               int                                 `json:"schema_version"`
	Mode                        string                              `json:"mode"`
	Reason                      string                              `json:"reason"`
	Review                      string                              `json:"review,omitempty"`
	TaskRevision                int                                 `json:"task_revision"`
	TaskRevisionSHA256          string                              `json:"task_revision_sha256"`
	AcceptanceCriteria          []string                            `json:"acceptance_criteria,omitempty"`
	IntegrationHead             string                              `json:"integration_head,omitempty"`
	VerificationOperationID     string                              `json:"verification_operation_id,omitempty"`
	VerificationAttemptRevision int                                 `json:"verification_attempt_revision,omitempty"`
	Acceptance                  []taskCompleteLegacyAcceptanceInput `json:"acceptance,omitempty"`
}

func validateTaskCompleteInput(in TaskCompleteInput) error {
	if err := model.ValidateProjectIdentifier(in.ProjectID); err != nil {
		return err
	}
	if err := model.ValidateCanonicalTaskID(in.Key); err != nil {
		return err
	}
	switch in.Mode {
	case "integrated", "non_code", "historical":
	default:
		return fmt.Errorf("invalid Task completion mode %q", in.Mode)
	}
	if strings.ContainsRune(in.Reason, 0) || utf8.RuneCountInString(in.Reason) < 1 || utf8.RuneCountInString(in.Reason) > 1024 {
		return fmt.Errorf("invalid Task completion reason")
	}
	if _, _, err := model.ParseJournalID(in.Review); err != nil {
		return fmt.Errorf("invalid Task completion review Journal identifier %q", in.Review)
	}
	return nil
}

func (s *Service) TaskComplete(ctx context.Context, in TaskCompleteInput, actor string) (TaskCompleteOutput, error) {
	if utf8.RuneCountInString(actor) < 1 {
		return TaskCompleteOutput{}, fmt.Errorf("Task completion requires an actor")
	}
	if err := validateTaskCompleteInput(in); err != nil {
		return TaskCompleteOutput{}, err
	}
	if err := s.requireLocalTaskAuthoring(ctx, in.ProjectID); err != nil {
		return TaskCompleteOutput{}, err
	}
	s.taskExecutionMu.Lock()
	defer s.taskExecutionMu.Unlock()
	entityRow, err := s.Durability.ReadSharedTask(ctx, in.Key)
	if err != nil {
		return TaskCompleteOutput{}, err
	}
	var task model.TaskAuthoring
	if err := json.Unmarshal(entityRow.Payload, &task); err != nil {
		return TaskCompleteOutput{}, err
	}
	if task.ProjectID != in.ProjectID || task.ID != in.Key || entityRow.Revision != int64(task.Revision) {
		return TaskCompleteOutput{}, fmt.Errorf("shared task ownership mismatch")
	}
	if err := model.ValidateTaskAuthoring(task); err != nil {
		return TaskCompleteOutput{}, err
	}
	if task.Status == model.TaskAuthoringArchived {
		return TaskCompleteOutput{}, fmt.Errorf("archived Task cannot be completed")
	}
	if len(task.AcceptanceCriteria) < 1 || len(task.AcceptanceCriteria) > 128 {
		return TaskCompleteOutput{}, fmt.Errorf("Task has no completable acceptance criteria")
	}
	identifiers, err := s.ProjectIdentifiersRead(ctx, in.ProjectID)
	if err != nil {
		return TaskCompleteOutput{}, err
	}
	if task.Status == model.TaskAuthoringDone {
		if err := s.taskCompleteReviewProof(ctx, in, task, identifiers.ProjectCode); err != nil {
			return TaskCompleteOutput{}, err
		}
	}
	state, hasExecution, err := s.Durability.ReadTaskExecutionState(ctx, in.ProjectID, in.Key)
	if err != nil {
		return TaskCompleteOutput{}, err
	}
	phases, err := s.Durability.ReadTaskExecutionPhases(ctx, in.ProjectID, in.Key, "integration")
	if err != nil {
		return TaskCompleteOutput{}, err
	}
	event, found, err := s.Durability.ReadTaskCompletionEvent(ctx, in.ProjectID, in.Key)
	if err != nil {
		return TaskCompleteOutput{}, err
	}
	if task.Status == model.TaskAuthoringDone {
		if !found {
			return TaskCompleteOutput{}, fmt.Errorf("completed Task lacks its completion lifecycle event; evidence reconciliation is required")
		}
		return s.taskCompleteReplay(in, task, state, hasExecution, phases, event)
	}
	if found {
		return TaskCompleteOutput{}, fmt.Errorf("Task completion lifecycle event exists without a completed Task; evidence reconciliation is required")
	}
	if task.Status != model.TaskAuthoringPlanned && task.Status != model.TaskAuthoringReady {
		return TaskCompleteOutput{}, fmt.Errorf("Task is not completable from status %q", task.Status)
	}
	var integrationHead string
	var verification taskCompleteIntegratedEvidence
	var historicalEnvelope taskExecutionHistoricalPhaseEvidence
	switch in.Mode {
	case "non_code":
		if hasExecution || len(phases) != 0 {
			return TaskCompleteOutput{}, fmt.Errorf("non_code completion requires no Task execution or integration history")
		}
	case "integrated":
		verification, err = s.taskCompleteIntegratedProof(ctx, task, state, hasExecution, phases)
		if err != nil {
			return TaskCompleteOutput{}, err
		}
		integrationHead = verification.Head
	case "historical":
		integrationHead, historicalEnvelope, err = s.taskCompleteHistoricalProof(ctx, in, task, state, hasExecution, phases)
		if err != nil {
			return TaskCompleteOutput{}, err
		}
	}
	contract := taskCompletionContract{
		SchemaVersion:               1,
		Mode:                        in.Mode,
		Reason:                      in.Reason,
		Review:                      in.Review,
		TaskRevision:                task.Revision,
		TaskRevisionSHA256:          task.RevisionSHA256,
		AcceptanceCriteria:          append([]string(nil), task.AcceptanceCriteria...),
		IntegrationHead:             integrationHead,
		VerificationOperationID:     verification.OperationID,
		VerificationAttemptRevision: verification.AttemptRevision,
	}
	contractJSON, err := json.Marshal(contract)
	if err != nil {
		return TaskCompleteOutput{}, err
	}
	contractSHA := sha256.Sum256(contractJSON)
	contractSHA256 := hex.EncodeToString(contractSHA[:])
	opSum := sha256.Sum256(append([]byte(in.ProjectID+"\x00"+in.Key+"\x00"), contractJSON...))
	operationID := "task-complete-" + hex.EncodeToString(opSum[:])

	if err := s.taskCompleteReviewProof(ctx, in, task, identifiers.ProjectCode); err != nil {
		return TaskCompleteOutput{}, err
	}

	if in.Mode == "historical" {
		if err := s.taskCompleteHistoricalEvidenceProof(ctx, in.ProjectID, identifiers.ProjectCode, in.Key, integrationHead, historicalEnvelope); err != nil {
			return TaskCompleteOutput{}, err
		}
	}

	if in.Mode != "non_code" {
		project, err := s.EffectiveProjectConfig(in.ProjectID)
		if err != nil {
			return TaskCompleteOutput{}, err
		}
		branch := strings.TrimPrefix(project.DefaultBranch, "refs/heads/")
		canonical, err := s.Git.RemoteBranchHead(ctx, project, branch)
		if err != nil {
			return TaskCompleteOutput{}, err
		}
		present, err := s.taskIntegrationCommitOnCanonical(ctx, project, branch, integrationHead, canonical)
		if err != nil {
			return TaskCompleteOutput{}, err
		}
		if !present {
			return TaskCompleteOutput{}, fmt.Errorf("Task integration commit is not on canonical main")
		}
	}
	now := s.durableNow()
	if in.Mode != "non_code" && len(phases) == 1 && now.Before(phases[0].CreatedAt) {
		return TaskCompleteOutput{}, fmt.Errorf("Task completion evidence timestamp ordering conflicts with the recorded contract; evidence reconciliation is required")
	}
	finalTask := task
	finalTask.Status = model.TaskAuthoringDone
	finalTask.UpdatedAt = now
	finalTask.ReadySeal = nil
	if err := model.ValidateTaskAuthoring(finalTask); err != nil {
		return TaskCompleteOutput{}, err
	}
	payload, err := json.Marshal(finalTask)
	if err != nil {
		return TaskCompleteOutput{}, err
	}
	req := sqlitestore.CommitTaskCompletionRequest{
		OperationID: operationID, ProjectID: in.ProjectID, TaskID: in.Key,
		Revision: entityRow.Revision, PreviousTaskPayload: entityRow.Payload, TaskPayload: payload,
		FromStatus: task.Status, Actor: actor, Reason: in.Reason,
		Contract: contractJSON, ContractSHA256: contractSHA256, RecordedAt: now,
	}
	if in.Mode != "non_code" {
		prev := state
		final := state
		final.Status = model.TaskExecutionDone
		final.ExecutionRevision++
		final.UpdatedAt = now
		req.PreviousExecution = &prev
		req.FinalExecution = &final
	}
	if err := s.Durability.CommitTaskCompletion(ctx, req); err != nil {

		if out, ok, _ := s.taskCompleteCommittedReplay(ctx, in); ok {
			return out, nil
		}
		return TaskCompleteOutput{}, err
	}
	return TaskCompleteOutput{
		Key:      in.Key,
		Status:   model.TaskAuthoringDone,
		Revision: task.Revision,
	}, nil
}

func (s *Service) taskCompleteReplay(in TaskCompleteInput, task model.TaskAuthoring, state model.TaskExecutionState, hasExecution bool, phases []sqlitestore.TaskExecutionPhase, event sqlitestore.TaskLifecycleEvent) (TaskCompleteOutput, error) {
	var stored taskCompletionContract
	if err := decodeStrict(event.Contract, &stored); err != nil {
		return TaskCompleteOutput{}, fmt.Errorf("Task completion contract is corrupt: %w", err)
	}
	canonical, err := json.Marshal(stored)
	if err != nil {
		return TaskCompleteOutput{}, err
	}
	if !bytes.Equal(event.Contract, canonical) {
		return TaskCompleteOutput{}, fmt.Errorf("Task completion contract is not canonical; evidence reconciliation is required")
	}
	current := taskCompletionContract{
		SchemaVersion:      1,
		Mode:               in.Mode,
		Reason:             in.Reason,
		Review:             in.Review,
		TaskRevision:       task.Revision,
		TaskRevisionSHA256: task.RevisionSHA256,
		AcceptanceCriteria: append([]string(nil), task.AcceptanceCriteria...),
	}
	legacyContract := stored.Review == "" && len(stored.Acceptance) > 0
	if stored.SchemaVersion != 1 || stored.TaskRevision != current.TaskRevision || stored.TaskRevisionSHA256 != current.TaskRevisionSHA256 || stored.Mode != current.Mode || stored.Reason != current.Reason || (!legacyContract && (stored.Review != current.Review || !slices.Equal(stored.AcceptanceCriteria, current.AcceptanceCriteria))) {
		return TaskCompleteOutput{}, fmt.Errorf("Task completion request conflicts with the recorded completion contract; evidence reconciliation is required")
	}
	expectedOp := sha256.Sum256(append([]byte(in.ProjectID+"\x00"+in.Key+"\x00"), canonical...))
	if event.ProjectID != in.ProjectID || event.TaskID != in.Key || event.Revision != int64(task.Revision) || event.EventKind != sqlitestore.TaskLifecycleEventKindComplete ||
		(event.FromStatus != model.TaskAuthoringPlanned && event.FromStatus != model.TaskAuthoringReady) || event.ToStatus != model.TaskAuthoringDone ||
		event.Reason != in.Reason || event.OperationID != "task-complete-"+hex.EncodeToString(expectedOp[:]) || !task.UpdatedAt.Equal(event.RecordedAt) {
		return TaskCompleteOutput{}, fmt.Errorf("Task completion lifecycle event conflicts with the recorded contract; evidence reconciliation is required")
	}
	if in.Mode == "non_code" {
		if stored.IntegrationHead != "" || stored.VerificationOperationID != "" || stored.VerificationAttemptRevision != 0 || hasExecution || len(phases) != 0 {
			return TaskCompleteOutput{}, fmt.Errorf("Task completion execution evidence conflicts with the recorded contract; evidence reconciliation is required")
		}
		return TaskCompleteOutput{
			Key:      in.Key,
			Status:   model.TaskAuthoringDone,
			Revision: task.Revision,
		}, nil
	}
	if model.ValidateCommitSHA(stored.IntegrationHead) != nil {
		return TaskCompleteOutput{}, fmt.Errorf("Task completion contract does not carry a valid integration commit; evidence reconciliation is required")
	}
	phase := sqlitestore.TaskExecutionPhase{}
	if len(phases) == 1 {
		phase = phases[0]
	}
	if !hasExecution || state.Status != model.TaskExecutionDone || state.TaskRevision != task.Revision || state.TaskRevisionSHA256 != task.RevisionSHA256 ||
		len(phases) != 1 || phase.Head != stored.IntegrationHead ||
		phase.ProjectID != in.ProjectID || phase.TaskID != in.Key || phase.Status != model.TaskExecutionIntegrated || phase.Stage != "integration" || phase.EventKind != "integration" || phase.Decision != "accept" ||
		phase.TaskRevisionSHA256 != state.TaskRevisionSHA256 || phase.Branch != state.Branch || phase.ExecutionRevision+1 != state.ExecutionRevision {
		return TaskCompleteOutput{}, fmt.Errorf("Task completion execution evidence conflicts with the recorded contract; evidence reconciliation is required")
	}
	if event.RecordedAt.Before(phase.CreatedAt) || !state.UpdatedAt.Equal(event.RecordedAt) {
		return TaskCompleteOutput{}, fmt.Errorf("Task completion evidence timestamp ordering conflicts with the recorded contract; evidence reconciliation is required")
	}
	switch in.Mode {
	case "integrated":
		if strings.HasPrefix(phase.Comment, taskExecutionHistoricalPhasePrefix) ||
			model.ValidateObjectIdentifier(stored.VerificationOperationID) != nil || stored.VerificationAttemptRevision < 1 {
			return TaskCompleteOutput{}, fmt.Errorf("Task completion phase evidence conflicts with the recorded contract; evidence reconciliation is required")
		}
	case "historical":
		if stored.VerificationOperationID != "" || stored.VerificationAttemptRevision != 0 {
			return TaskCompleteOutput{}, fmt.Errorf("Task completion phase evidence conflicts with the recorded contract; evidence reconciliation is required")
		}
		var envelope taskExecutionHistoricalPhaseEvidence
		if !strings.HasPrefix(phase.Comment, taskExecutionHistoricalPhasePrefix) || decodeStrict([]byte(strings.TrimPrefix(phase.Comment, taskExecutionHistoricalPhasePrefix)), &envelope) != nil ||
			envelope.SchemaVersion != 1 || envelope.Mode != "historical" || (envelope.Profile != "legacy" && envelope.Profile != "bootstrap_full") ||
			envelope.IntegrationHead != phase.Head || envelope.IntegrationHead != stored.IntegrationHead {
			return TaskCompleteOutput{}, fmt.Errorf("Task completion phase evidence conflicts with the recorded contract; evidence reconciliation is required")
		}
		if _, _, err := model.ParseJournalID(envelope.Evidence); err != nil {
			return TaskCompleteOutput{}, fmt.Errorf("Task completion phase evidence conflicts with the recorded contract; evidence reconciliation is required")
		}
		if envelope.Profile == "legacy" && (envelope.CandidateHead != "" || envelope.MainBase != "") {
			return TaskCompleteOutput{}, fmt.Errorf("Task completion phase evidence conflicts with the recorded contract; evidence reconciliation is required")
		}
		if envelope.Profile == "bootstrap_full" && (model.ValidateCommitSHA(envelope.CandidateHead) != nil || model.ValidateCommitSHA(envelope.MainBase) != nil) {
			return TaskCompleteOutput{}, fmt.Errorf("Task completion phase evidence conflicts with the recorded contract; evidence reconciliation is required")
		}
	}
	return TaskCompleteOutput{
		Key:      in.Key,
		Status:   model.TaskAuthoringDone,
		Revision: task.Revision,
	}, nil
}

func (s *Service) taskCompleteCommittedReplay(ctx context.Context, in TaskCompleteInput) (TaskCompleteOutput, bool, error) {
	event, found, err := s.Durability.ReadTaskCompletionEvent(ctx, in.ProjectID, in.Key)
	if err != nil || !found {
		return TaskCompleteOutput{}, false, err
	}
	entityRow, err := s.Durability.ReadSharedTask(ctx, in.Key)
	if err != nil {
		return TaskCompleteOutput{}, false, err
	}
	var task model.TaskAuthoring
	if err := json.Unmarshal(entityRow.Payload, &task); err != nil || task.Status != model.TaskAuthoringDone || task.ProjectID != in.ProjectID || entityRow.Revision != int64(task.Revision) || model.ValidateTaskAuthoring(task) != nil {
		return TaskCompleteOutput{}, false, nil
	}
	state, hasExecution, err := s.Durability.ReadTaskExecutionState(ctx, in.ProjectID, in.Key)
	if err != nil {
		return TaskCompleteOutput{}, false, err
	}
	phases, err := s.Durability.ReadTaskExecutionPhases(ctx, in.ProjectID, in.Key, "integration")
	if err != nil {
		return TaskCompleteOutput{}, false, err
	}
	out, err := s.taskCompleteReplay(in, task, state, hasExecution, phases, event)
	if err != nil {
		return TaskCompleteOutput{}, false, nil
	}
	return out, true, nil
}

func (s *Service) taskCompleteReviewProof(ctx context.Context, in TaskCompleteInput, task model.TaskAuthoring, projectCode string) error {
	if entry, err := s.readSharedJournalEntry(ctx, in.Review); err == nil {
		return s.taskCompleteCanonicalReviewProof(ctx, in, task, projectCode, entry)
	}
	registry := s.entityRegistry(in.ProjectID)
	type admitted struct {
		record entity.Record
		event  model.OperatorJournalEvent
	}
	var review model.OperatorJournalEvent
	record, err := registry.ReadInto(ctx, entity.JournalFamily, in.Review, &review)
	if err != nil {
		return fmt.Errorf("Task completion review Journal record: %w", err)
	}
	if _, err := validateOperatorEventPathIdentity(record.Path, s.operatorEventsPrefix(in.ProjectID), review, in.ProjectID, projectCode); err != nil {
		return fmt.Errorf("Task completion review Journal record: %w", err)
	}
	if err := s.taskCompleteEvidenceAuthority(review, in.ProjectID, projectCode); err != nil {
		return err
	}
	if review.Kind != model.OperatorTaskReview {
		return fmt.Errorf("Task completion review is not a task review Journal event")
	}
	if !slices.Contains(review.References.Tasks, task.ID) {
		return fmt.Errorf("Task completion review does not reference the Task")
	}
	cache := map[string]admitted{in.Review: {record: record, event: review}}
	records, err := registry.ListRecords(ctx, entity.Query{Family: entity.JournalFamily})
	if err != nil {
		return err
	}
	eventsPrefix := s.operatorEventsPrefix(in.ProjectID)
	stable := false
	for _, rec := range records {
		var other model.OperatorJournalEvent
		if err := decodeStrict(rec.Bytes, &other); err != nil {
			return fmt.Errorf("invalid Journal record %s: %w", rec.Path, err)
		}
		if _, err := validateOperatorEventPathIdentity(rec.Path, eventsPrefix, other, in.ProjectID, projectCode); err != nil {
			return fmt.Errorf("invalid Journal record %s: %w", rec.Path, err)
		}
		if entry, ok := cache[rec.ID]; ok {
			if !bytes.Equal(entry.record.Bytes, rec.Bytes) {
				return fmt.Errorf("Task completion review Journal record changed during admission")
			}
			stable = true
		}
		if other.SupersedesEventID == in.Review {
			return fmt.Errorf("Task completion review Journal record is superseded")
		}
	}
	if !stable {
		return fmt.Errorf("Task completion review Journal record disappeared during admission")
	}
	return nil
}

func (s *Service) taskCompleteCanonicalReviewProof(ctx context.Context, in TaskCompleteInput, task model.TaskAuthoring, projectCode string, entry model.JournalEntry) error {
	if entry.ProjectID != in.ProjectID {
		return fmt.Errorf("Task completion review Journal entry is outside the project")
	}
	if entry.Stream != model.JournalStreamPlannerNotes {
		return fmt.Errorf("Task completion review Journal entry is not a planner-notes record")
	}
	if entry.Role != durableSession.RolePlanner {
		return fmt.Errorf("Task completion review Journal entry lacks Planner provenance")
	}
	session, err := durableSession.NewStoreWithDurability(s.Durability).Get(entry.SessionID)
	if err != nil {
		return fmt.Errorf("Task completion evidence Planner Session authority could not be read: %w", err)
	}
	if session.Role != durableSession.RolePlanner || session.Status != durableSession.StatusActive || session.ProjectID != in.ProjectID || session.ProjectCode != projectCode {
		return fmt.Errorf("Task completion evidence does not carry durable active Planner Session authority for this project")
	}
	var data struct {
		References []string `json:"references"`
	}
	if err := json.Unmarshal(entry.Data, &data); err != nil {
		return fmt.Errorf("Task completion review Journal entry data: %w", err)
	}
	if !slices.Contains(data.References, task.ID) {
		return fmt.Errorf("Task completion review does not reference the Task")
	}
	return nil
}

func (s *Service) taskCompleteEvidenceAuthority(event model.OperatorJournalEvent, projectID, projectCode string) error {
	if event.SessionID == nil {
		return fmt.Errorf("Task completion evidence lacks durable Planner Session authority")
	}
	session, err := durableSession.NewStoreWithDurability(s.Durability).Get(*event.SessionID)
	if err != nil {
		return fmt.Errorf("Task completion evidence Planner Session authority could not be read: %w", err)
	}
	if session.Role != durableSession.RolePlanner || session.Status != durableSession.StatusActive || session.ProjectID != projectID || session.ProjectCode != projectCode {
		return fmt.Errorf("Task completion evidence does not carry durable active Planner Session authority for this project")
	}
	if session.CreatedAt.After(event.RecordedAt) || session.StartedAt.After(event.RecordedAt) {
		return fmt.Errorf("Task completion evidence Planner Session postdates the Journal record")
	}
	return nil
}

type taskCompleteIntegratedEvidence struct {
	Head            string
	OperationID     string
	AttemptRevision int
}

func (s *Service) taskCompleteIntegratedProof(ctx context.Context, task model.TaskAuthoring, state model.TaskExecutionState, hasExecution bool, phases []sqlitestore.TaskExecutionPhase) (taskCompleteIntegratedEvidence, error) {
	if !hasExecution || state.Status != model.TaskExecutionIntegrated {
		return taskCompleteIntegratedEvidence{}, fmt.Errorf("integrated completion requires an integrated Task execution")
	}
	if len(phases) != 1 {
		return taskCompleteIntegratedEvidence{}, fmt.Errorf("integrated completion requires exactly one integration phase")
	}
	phase := phases[0]
	if phase.ProjectID != task.ProjectID || phase.TaskID != task.ID || phase.Status != model.TaskExecutionIntegrated || phase.Stage != "integration" || phase.EventKind != "integration" || phase.Decision != "accept" || phase.TaskRevisionSHA256 != state.TaskRevisionSHA256 || phase.Branch != state.Branch || phase.ExecutionRevision != state.ExecutionRevision {
		return taskCompleteIntegratedEvidence{}, fmt.Errorf("integration phase does not bind the integrated execution state")
	}
	if state.TaskRevision != task.Revision || state.TaskRevisionSHA256 != task.RevisionSHA256 {
		return taskCompleteIntegratedEvidence{}, fmt.Errorf("integrated execution does not bind the current Task revision")
	}
	if strings.HasPrefix(phase.Comment, taskExecutionHistoricalPhasePrefix) {
		return taskCompleteIntegratedEvidence{}, fmt.Errorf("integrated completion rejects a historical integration phase")
	}
	receipt, current, reason, err := s.taskExecutionVerificationProofCurrent(ctx, state)
	if err != nil {
		return taskCompleteIntegratedEvidence{}, err
	}
	if !current {
		return taskCompleteIntegratedEvidence{}, fmt.Errorf("integrated completion requires a current immutable Task verification: %s", reason)
	}
	if receipt.CompletedAt.After(phase.CreatedAt) {
		return taskCompleteIntegratedEvidence{}, fmt.Errorf("integrated Task verification postdates the integration phase")
	}
	if err := model.ValidateServerGateEvidence(receipt.Gates); err != nil {
		return taskCompleteIntegratedEvidence{}, fmt.Errorf("Task verification gate evidence is malformed: %w", err)
	}
	if len(receipt.Gates) == 0 {
		return taskCompleteIntegratedEvidence{}, fmt.Errorf("Task verification gate evidence is empty")
	}
	seen := map[string]bool{}
	for _, gate := range receipt.Gates {
		if seen[gate.ID] {
			return taskCompleteIntegratedEvidence{}, fmt.Errorf("Task verification has duplicate gate evidence")
		}
		seen[gate.ID] = true
		if gate.Execution != "executed" || gate.ExitCode != 0 || gate.TreeID != receipt.CandidateTree || model.ValidateSHA256(gate.ContractDigest) != nil || model.ValidateSHA256(gate.ReceiptDigest) != nil {
			return taskCompleteIntegratedEvidence{}, fmt.Errorf("Task verification does not prove a passing tree-bound executed gate")
		}
	}
	project, err := s.EffectiveProjectConfig(task.ProjectID)
	if err != nil {
		return taskCompleteIntegratedEvidence{}, err
	}
	tree, parents, err := s.Git.InspectTaskIntegrationCommit(ctx, project, phase.Head)
	if err != nil {
		return taskCompleteIntegratedEvidence{}, err
	}
	if len(parents) != 1 || parents[0] != receipt.BaseHead {
		return taskCompleteIntegratedEvidence{}, fmt.Errorf("Task integration commit is not an exact child of the verified base")
	}
	if tree != receipt.CandidateTree {
		return taskCompleteIntegratedEvidence{}, fmt.Errorf("Task integration commit tree does not match the verified candidate tree")
	}
	return taskCompleteIntegratedEvidence{
		Head:            phase.Head,
		OperationID:     receipt.OperationID,
		AttemptRevision: receipt.AttemptRevision,
	}, nil
}

func (s *Service) taskCompleteHistoricalProof(ctx context.Context, in TaskCompleteInput, task model.TaskAuthoring, state model.TaskExecutionState, hasExecution bool, phases []sqlitestore.TaskExecutionPhase) (string, taskExecutionHistoricalPhaseEvidence, error) {
	if !hasExecution || state.Status != model.TaskExecutionIntegrated {
		return "", taskExecutionHistoricalPhaseEvidence{}, fmt.Errorf("historical completion requires an integrated Task execution")
	}
	if len(phases) != 1 {
		return "", taskExecutionHistoricalPhaseEvidence{}, fmt.Errorf("historical completion requires exactly one integration phase")
	}
	phase := phases[0]
	if phase.ProjectID != task.ProjectID || phase.TaskID != task.ID || phase.Status != model.TaskExecutionIntegrated || phase.Stage != "integration" || phase.EventKind != "integration" || phase.Decision != "accept" || phase.TaskRevisionSHA256 != state.TaskRevisionSHA256 || phase.Branch != state.Branch || phase.ExecutionRevision != state.ExecutionRevision {
		return "", taskExecutionHistoricalPhaseEvidence{}, fmt.Errorf("integration phase does not bind the integrated execution state")
	}
	if state.TaskRevision != task.Revision || state.TaskRevisionSHA256 != task.RevisionSHA256 {
		return "", taskExecutionHistoricalPhaseEvidence{}, fmt.Errorf("integrated execution does not bind the current Task revision")
	}
	if !strings.HasPrefix(phase.Comment, taskExecutionHistoricalPhasePrefix) {
		return "", taskExecutionHistoricalPhaseEvidence{}, fmt.Errorf("historical completion rejects an ordinary integration phase")
	}
	var envelope taskExecutionHistoricalPhaseEvidence
	if err := decodeStrict([]byte(strings.TrimPrefix(phase.Comment, taskExecutionHistoricalPhasePrefix)), &envelope); err != nil {
		return "", taskExecutionHistoricalPhaseEvidence{}, fmt.Errorf("historical integration phase evidence is malformed: %w", err)
	}
	if envelope.SchemaVersion != 1 || envelope.Mode != "historical" || envelope.IntegrationHead != phase.Head {
		return "", taskExecutionHistoricalPhaseEvidence{}, fmt.Errorf("historical integration phase evidence does not bind the phase")
	}
	switch envelope.Profile {
	case "legacy":
		if envelope.CandidateHead != "" || envelope.MainBase != "" {
			return "", taskExecutionHistoricalPhaseEvidence{}, fmt.Errorf("historical legacy phase evidence must not carry candidate/base")
		}
	case "bootstrap_full":
		if model.ValidateCommitSHA(envelope.CandidateHead) != nil || model.ValidateCommitSHA(envelope.MainBase) != nil {
			return "", taskExecutionHistoricalPhaseEvidence{}, fmt.Errorf("historical bootstrap phase evidence must carry valid candidate/base")
		}
	default:
		return "", taskExecutionHistoricalPhaseEvidence{}, fmt.Errorf("historical integration phase evidence has an unknown profile")
	}
	code, _, err := model.ParseJournalID(envelope.Evidence)
	if err != nil {
		return "", taskExecutionHistoricalPhaseEvidence{}, fmt.Errorf("historical integration phase evidence identifier is invalid")
	}
	identifiers, err := s.ProjectIdentifiersRead(ctx, in.ProjectID)
	if err != nil {
		return "", taskExecutionHistoricalPhaseEvidence{}, err
	}
	if code != identifiers.ProjectCode {
		return "", taskExecutionHistoricalPhaseEvidence{}, fmt.Errorf("historical integration phase evidence does not bind this project")
	}
	return phase.Head, envelope, nil
}

func (s *Service) taskCompleteHistoricalEvidenceProof(ctx context.Context, projectID, projectCode, key, integrationHead string, envelope taskExecutionHistoricalPhaseEvidence) error {
	var event model.OperatorJournalEvent
	record, err := s.entityRegistry(projectID).ReadInto(ctx, entity.JournalFamily, envelope.Evidence, &event)
	if err != nil {
		return fmt.Errorf("historical integration evidence Journal record: %w", err)
	}
	if _, err := validateOperatorEventPathIdentity(record.Path, s.operatorEventsPrefix(projectID), event, projectID, projectCode); err != nil {
		return fmt.Errorf("historical integration evidence Journal record: %w", err)
	}
	if err := s.taskCompleteEvidenceAuthority(event, projectID, projectCode); err != nil {
		return err
	}
	if !slices.Contains(event.References.Tasks, key) || !slices.Contains(event.References.Commits, integrationHead) {
		return fmt.Errorf("historical integration evidence does not reference the Task and integration commit")
	}
	if envelope.Profile == "bootstrap_full" {
		if event.Kind != model.OperatorTaskReview || !slices.Contains(event.References.Commits, envelope.CandidateHead) || !slices.Contains(event.References.Commits, envelope.MainBase) {
			return fmt.Errorf("historical bootstrap evidence does not bind the candidate and base commits")
		}
	}
	records, err := s.entityRegistry(projectID).ListRecords(ctx, entity.Query{Family: entity.JournalFamily})
	if err != nil {
		return err
	}
	eventsPrefix := s.operatorEventsPrefix(projectID)
	stable := false
	for _, rec := range records {
		var other model.OperatorJournalEvent
		if err := decodeStrict(rec.Bytes, &other); err != nil {
			return fmt.Errorf("invalid Journal record %s: %w", rec.Path, err)
		}
		if _, err := validateOperatorEventPathIdentity(rec.Path, eventsPrefix, other, projectID, projectCode); err != nil {
			return fmt.Errorf("invalid Journal record %s: %w", rec.Path, err)
		}
		if rec.ID == record.ID {
			if !bytes.Equal(rec.Bytes, record.Bytes) {
				return fmt.Errorf("historical integration evidence Journal record changed during admission")
			}
			stable = true
		}
		if other.SupersedesEventID == event.ID {
			return fmt.Errorf("historical integration evidence Journal record is superseded")
		}
	}
	if !stable {
		return fmt.Errorf("historical integration evidence Journal record disappeared during admission")
	}
	return nil
}
