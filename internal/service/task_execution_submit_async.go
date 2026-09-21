package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

const taskExecutionSubmitKind = "task-execution-submit"

type taskExecutionSubmitInput struct {
	Stage  string `json:"stage"`
	TaskID string `json:"task"`
}

type taskExecutionSubmitCapture struct {
	TaskID            string `json:"task_id"`
	Stage             string `json:"stage"`
	ExecutionRevision int    `json:"execution_revision"`
}

// TaskExecutionSubmitReceipt is the durable admission and outcome projection
// for the runtime-bound Agent-CLI submission transport. Result is present only
// after the submission transition is proven durable.
type TaskExecutionSubmitReceipt struct {
	OperationID string                     `json:"operation_id"`
	Status      string                     `json:"status"`
	Stage       string                     `json:"stage"`
	Result      *TaskExecutionPublicOutput `json:"result,omitempty"`
	Error       string                     `json:"error,omitempty"`
	CreatedAt   time.Time                  `json:"created_at"`
	UpdatedAt   time.Time                  `json:"updated_at"`
}

func taskExecutionSubmitReceipt(operation durableMutationOperation) TaskExecutionSubmitReceipt {
	receipt := TaskExecutionSubmitReceipt{
		OperationID: operation.OperationID,
		Status:      operation.Status,
		Error:       operation.Error,
		CreatedAt:   operation.CreatedAt,
		UpdatedAt:   operation.UpdatedAt,
	}
	var input taskExecutionSubmitInput
	if err := json.Unmarshal(operation.Input, &input); err == nil {
		receipt.Stage = input.Stage
	}
	if operation.Status != "completed" || len(operation.Result) == 0 {
		return receipt
	}
	var result TaskExecutionPublicOutput
	if err := json.Unmarshal(operation.Result, &result); err != nil {
		receipt.Status = "failed"
		receipt.Error = "invalid durable Task submission result"
		receipt.Result = nil
		return receipt
	}
	receipt.Result = &result
	return receipt
}

// TaskExecutionSubmitAsync admits or reconciles the calling Worker's durable
// submission operation for its current actionable Task.
func (s *Service) TaskExecutionSubmitAsync(ctx context.Context, projectID, stage string) (TaskExecutionSubmitReceipt, error) {
	if err := model.ValidateProjectIdentifier(projectID); err != nil {
		return TaskExecutionSubmitReceipt{}, err
	}
	if stage != "code" && stage != "rebase" {
		return TaskExecutionSubmitReceipt{}, fmt.Errorf("unsupported Task submission stage %q", stage)
	}
	sessionID := AgentSessionID(ctx)
	if sessionID == "" {
		return TaskExecutionSubmitReceipt{}, fmt.Errorf("Task submission requires an active Worker Session")
	}
	projectCode, err := s.localOperationProjectCode(ctx, projectID)
	if err != nil {
		return TaskExecutionSubmitReceipt{}, err
	}
	key, resolveErr := s.resolveTaskExecutionTaskForAgent(ctx, projectID)
	if resolveErr != nil {
		if !errors.Is(resolveErr, errNoCurrentTask) {
			return TaskExecutionSubmitReceipt{}, resolveErr
		}
		latest, found, findErr := s.findLatestTaskExecutionSubmitOperation(ctx, projectID, projectCode, sessionID, stage)
		if findErr != nil {
			return TaskExecutionSubmitReceipt{}, findErr
		}
		if !found {
			return TaskExecutionSubmitReceipt{}, resolveErr
		}
		if latest.Status == "accepted" || latest.Status == "running" {
			return taskExecutionSubmitReceipt(latest), nil
		}
		reconciled, _, reconcileErr := s.reconcileTaskExecutionSubmitOperation(ctx, latest)
		if reconcileErr != nil {
			return TaskExecutionSubmitReceipt{}, reconcileErr
		}
		return taskExecutionSubmitReceipt(reconciled), nil
	}
	state, foundState, err := s.Durability.ReadTaskExecutionState(ctx, projectID, key)
	if err != nil {
		return TaskExecutionSubmitReceipt{}, err
	}
	if !foundState {
		return TaskExecutionSubmitReceipt{}, fmt.Errorf("Task has not been dispatched")
	}
	input := taskExecutionSubmitInput{
		Stage:  stage,
		TaskID: key,
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return TaskExecutionSubmitReceipt{}, err
	}
	latest, found, err := s.findLatestEquivalentDurableMutation(ctx, taskExecutionSubmitKind, projectID, projectCode, sessionID, raw)
	if err != nil {
		return TaskExecutionSubmitReceipt{}, err
	}
	if found {
		if latest.Status == "accepted" || latest.Status == "running" {
			return taskExecutionSubmitReceipt(latest), nil
		}
		reconciled, landed, err := s.reconcileTaskExecutionSubmitOperation(ctx, latest)
		if err != nil {
			return TaskExecutionSubmitReceipt{}, err
		}
		latest = reconciled
		if landed {
			return taskExecutionSubmitReceipt(latest), nil
		}
		if latest.Status == "outcome_unknown" {
			return taskExecutionSubmitReceipt(latest), nil
		}
	}
	operation, err := s.enqueueRepeatableAgentMutation(ctx, taskExecutionSubmitKind, projectID, input)
	if err != nil {
		return TaskExecutionSubmitReceipt{}, err
	}
	capture := taskExecutionSubmitCapture{
		TaskID:            key,
		Stage:             stage,
		ExecutionRevision: state.ExecutionRevision,
	}
	captureRaw, err := json.Marshal(capture)
	if err != nil {
		return TaskExecutionSubmitReceipt{}, err
	}
	s.durableMutationMu.Lock()
	fresh, readErr := s.readDurableMutation(operation.OperationID)
	if readErr == nil && fresh.CapturedState == "" {
		fresh.CapturedState = string(captureRaw)
		fresh.UpdatedAt = time.Now().UTC()
		if writeErr := s.writeDurableMutation(fresh); writeErr != nil {
			s.durableMutationMu.Unlock()
			return TaskExecutionSubmitReceipt{}, writeErr
		}
		operation = fresh
	}
	s.durableMutationMu.Unlock()
	if readErr != nil {
		return TaskExecutionSubmitReceipt{}, readErr
	}
	return taskExecutionSubmitReceipt(operation), nil
}

func (s *Service) findLatestTaskExecutionSubmitOperation(ctx context.Context, projectID, projectCode, sessionID, stage string) (durableMutationOperation, bool, error) {
	localOperations, err := s.Durability.ListLocalOperations(ctx, projectID)
	if err != nil {
		return durableMutationOperation{}, false, err
	}
	var latest durableMutationOperation
	found := false
	for _, local := range localOperations {
		if local.Kind != taskExecutionSubmitKind || local.AdmissionSessionID != sessionID || local.AdmissionInputSHA256 == "" {
			continue
		}
		if local.ProjectCode != projectCode {
			return durableMutationOperation{}, false, fmt.Errorf("Task submission Local operation %s project code mismatch", local.OperationID)
		}
		operation, readErr := s.readDurableMutation(local.OperationID)
		if readErr != nil {
			return durableMutationOperation{}, false, fmt.Errorf("Task submission Local operation %s is corrupt: %w", local.OperationID, readErr)
		}
		operationCode, operationNumber, parseErr := model.ParseOperationID(operation.OperationID)
		if parseErr != nil || operationCode != local.ProjectCode || operationNumber != local.OperationNumber || operation.OperationID != local.OperationID || operation.ProjectID != local.ProjectID || operation.Kind != local.Kind || operation.RequestSHA256 != local.MutationID {
			return durableMutationOperation{}, false, fmt.Errorf("Task submission Local operation %s identity mismatch", local.OperationID)
		}
		if operation.SessionID != sessionID {
			return durableMutationOperation{}, false, fmt.Errorf("Task submission Local operation %s session coordinate mismatch", local.OperationID)
		}
		if local.AdmissionInputSHA256 != durableMutationInputSHA256(operation.Input) {
			return durableMutationOperation{}, false, fmt.Errorf("Task submission Local operation %s input coordinate mismatch", local.OperationID)
		}
		var compactInput bytes.Buffer
		if err := json.Compact(&compactInput, operation.Input); err != nil {
			return durableMutationOperation{}, false, fmt.Errorf("Task submission Local operation %s input is corrupt: %w", local.OperationID, err)
		}
		if operation.RequestSHA256 != durableMutationDigest(taskExecutionSubmitKind, sessionID, compactInput.Bytes()) {
			return durableMutationOperation{}, false, fmt.Errorf("Task submission Local operation %s request identity mismatch", local.OperationID)
		}
		if !durableMutationKnownStatus(operation.Status) {
			return durableMutationOperation{}, false, fmt.Errorf("Task submission Local operation %s has invalid status", local.OperationID)
		}
		var input taskExecutionSubmitInput
		if err := json.Unmarshal(operation.Input, &input); err != nil {
			return durableMutationOperation{}, false, fmt.Errorf("Task submission Local operation %s input is corrupt: %w", local.OperationID, err)
		}
		if input.Stage != stage || input.TaskID == "" {
			continue
		}
		if err := model.ValidateTaskIDForProject(input.TaskID, projectCode); err != nil {
			return durableMutationOperation{}, false, fmt.Errorf("Task submission Local operation %s has invalid Task input: %w", local.OperationID, err)
		}
		if !found || durableMutationTurnAfter(operation, latest) {
			latest = operation
			found = true
		}
	}
	return latest, found, nil
}

// TaskExecutionSubmitOperationStatus returns a Task submission operation only
// to the Worker Session that admitted it.
func (s *Service) TaskExecutionSubmitOperationStatus(ctx context.Context, operationID string) (TaskExecutionSubmitReceipt, error) {
	operation, err := s.readDurableMutation(operationID)
	if err != nil {
		return TaskExecutionSubmitReceipt{}, err
	}
	if operation.Kind != taskExecutionSubmitKind {
		return TaskExecutionSubmitReceipt{}, fmt.Errorf("operation is not a Task submission mutation")
	}
	if sessionID := AgentSessionID(ctx); sessionID == "" || operation.SessionID != sessionID {
		return TaskExecutionSubmitReceipt{}, fmt.Errorf("durable mutation session mismatch")
	}
	if operation.Status == "outcome_unknown" {
		reconciled, _, err := s.reconcileTaskExecutionSubmitOperation(ctx, operation)
		if err != nil {
			return TaskExecutionSubmitReceipt{}, err
		}
		operation = reconciled
	}
	return taskExecutionSubmitReceipt(operation), nil
}

func (s *Service) reconcileTaskExecutionSubmitOperation(ctx context.Context, operation durableMutationOperation) (durableMutationOperation, bool, error) {
	var capture taskExecutionSubmitCapture
	if len(operation.CapturedState) == 0 || json.Unmarshal([]byte(operation.CapturedState), &capture) != nil || capture.TaskID == "" || capture.ExecutionRevision < 1 {
		return operation, false, nil
	}
	phases, err := s.Durability.ReadTaskExecutionPhases(ctx, operation.ProjectID, capture.TaskID, capture.Stage)
	if err != nil {
		return operation, false, err
	}
	var submission *sqlitestore.TaskExecutionPhase
	for i := range phases {
		if phases[i].EventKind == "submission" && phases[i].ExecutionRevision == capture.ExecutionRevision+1 {
			submission = &phases[i]
			break
		}
	}
	state, found, err := s.Durability.ReadTaskExecutionState(ctx, operation.ProjectID, capture.TaskID)
	if err != nil || !found {
		return operation, false, err
	}
	if submission == nil {
		if state.Stage == capture.Stage && state.ExecutionRevision == capture.ExecutionRevision && (state.Status == model.TaskExecutionDispatched || state.Status == model.TaskExecutionChangesRequested) {
			operation.Status = "failed"
			operation.Error = "Task submission did not reach durable phase evidence"
			operation.RecoveryReason = "not-landed outcome proven; repeat is idempotent"
			operation.UpdatedAt = time.Now().UTC()
			if err := s.writeDurableMutation(operation); err != nil {
				return operation, false, err
			}
		}
		return operation, false, nil
	}
	result, err := json.Marshal(taskExecutionPublicOutput(state))
	if err != nil {
		return operation, false, err
	}
	operation.Status = "completed"
	operation.Result = result
	operation.Error = ""
	operation.RecoveryReason = "submission outcome reconciled from durable Task phase evidence"
	operation.UpdatedAt = time.Now().UTC()
	if err := s.writeDurableMutation(operation); err != nil {
		return operation, false, err
	}
	return operation, true, nil
}
