package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

type TaskExecutionBlockInput struct {
	ProjectID string
	Key       string
	Reason    string
}

type TaskExecutionResumeInput struct {
	ProjectID string
	Key       string
	Reason    string
}

const taskExecutionBlockReasonLimit = 1024

func (s *Service) TaskExecutionBlock(ctx context.Context, in TaskExecutionBlockInput) (TaskExecutionPublicOutput, error) {
	reason, err := validateTaskExecutionParkInput(in.ProjectID, in.Key, in.Reason)
	if err != nil {
		return TaskExecutionPublicOutput{}, err
	}
	s.durableMutationMu.Lock()
	defer s.durableMutationMu.Unlock()
	s.taskExecutionMu.Lock()
	defer s.taskExecutionMu.Unlock()
	state, found, err := s.readExecutionForMutation(ctx, in.ProjectID, in.Key)
	if err != nil || !found {
		if err != nil {
			return TaskExecutionPublicOutput{}, err
		}
		return TaskExecutionPublicOutput{}, fmt.Errorf("Task has no execution state")
	}
	if state.Status == model.TaskExecutionBlocked {
		phase, phaseErr := s.taskExecutionBlockedPhase(ctx, state)
		if phaseErr != nil {
			return TaskExecutionPublicOutput{}, phaseErr
		}
		if phase.Comment != reason {
			return TaskExecutionPublicOutput{}, fmt.Errorf("conflicting Task block mutation")
		}
		out := taskExecutionPublicOutput(state)
		out.Reason = reason
		return out, nil
	}
	if !model.IsTaskExecutionAgentActionable(state.Status, state.Stage) {
		return TaskExecutionPublicOutput{}, fmt.Errorf("Task execution is not at a Worker-actionable block boundary")
	}
	if err := s.ensureNoInFlightWorkerTurn(ctx, state); err != nil {
		return TaskExecutionPublicOutput{}, err
	}
	expectedStatus := state.Status
	state.Status = model.TaskExecutionBlocked
	state.ExecutionRevision++
	state.UpdatedAt = time.Now().UTC()
	phase := sqlitestore.TaskExecutionPhase{
		TaskID:             state.TaskID,
		ProjectID:          state.ProjectID,
		ExecutionRevision:  state.ExecutionRevision,
		Stage:              state.Stage,
		Status:             state.Status,
		Head:               state.Head,
		Branch:             state.Branch,
		TaskRevisionSHA256: state.TaskRevisionSHA256,
		EventKind:          "block",
		Decision:           expectedStatus,
		Comment:            reason,
		CreatedAt:          state.UpdatedAt,
	}
	if err := s.Durability.TransitionTaskExecutionState(ctx, state, state.ExecutionRevision-1, phase); err != nil {
		return TaskExecutionPublicOutput{}, err
	}
	out := taskExecutionPublicOutput(state)
	out.Reason = reason
	return out, nil
}

func (s *Service) TaskExecutionResume(ctx context.Context, in TaskExecutionResumeInput) (TaskExecutionPublicOutput, error) {
	reason, err := validateTaskExecutionParkInput(in.ProjectID, in.Key, in.Reason)
	if err != nil {
		return TaskExecutionPublicOutput{}, err
	}
	s.durableMutationMu.Lock()
	defer s.durableMutationMu.Unlock()
	s.taskExecutionMu.Lock()
	defer s.taskExecutionMu.Unlock()
	state, found, err := s.readExecutionForMutation(ctx, in.ProjectID, in.Key)
	if err != nil || !found {
		if err != nil {
			return TaskExecutionPublicOutput{}, err
		}
		return TaskExecutionPublicOutput{}, fmt.Errorf("Task has no execution state")
	}
	phase, phaseErr := s.latestTaskExecutionPhase(ctx, state)
	if phaseErr != nil {
		return TaskExecutionPublicOutput{}, phaseErr
	}
	if state.Status != model.TaskExecutionBlocked {
		if phase.EventKind == "resume" && phase.Comment == reason && phase.Status == state.Status && phase.Decision == state.Status {
			out := taskExecutionPublicOutput(state)
			out.Reason = reason
			return out, nil
		}
		return TaskExecutionPublicOutput{}, fmt.Errorf("Task execution is not blocked")
	}
	if phase.EventKind != "block" || phase.Status != model.TaskExecutionBlocked || phase.Decision == "" || !model.IsTaskExecutionAgentActionable(phase.Decision, phase.Stage) || phase.Stage != state.Stage || phase.Head != state.Head || phase.Branch != state.Branch {
		return TaskExecutionPublicOutput{}, fmt.Errorf("Task blocked state has invalid resume authority")
	}
	if err := s.ensureNoInFlightWorkerTurn(ctx, state); err != nil {
		return TaskExecutionPublicOutput{}, err
	}
	if err := s.ensureWorkerActionableSlot(ctx, state.ProjectID, state.Agent, state.TaskID); err != nil {
		return TaskExecutionPublicOutput{}, err
	}
	state.Status = phase.Decision
	state.ExecutionRevision++
	state.UpdatedAt = time.Now().UTC()
	resumePhase := sqlitestore.TaskExecutionPhase{
		TaskID:             state.TaskID,
		ProjectID:          state.ProjectID,
		ExecutionRevision:  state.ExecutionRevision,
		Stage:              state.Stage,
		Status:             state.Status,
		Head:               state.Head,
		Branch:             state.Branch,
		TaskRevisionSHA256: state.TaskRevisionSHA256,
		EventKind:          "resume",
		Decision:           state.Status,
		Comment:            reason,
		CreatedAt:          state.UpdatedAt,
	}
	if err := s.Durability.TransitionTaskExecutionState(ctx, state, state.ExecutionRevision-1, resumePhase); err != nil {
		return TaskExecutionPublicOutput{}, err
	}
	out := taskExecutionPublicOutput(state)
	out.Reason = reason
	return out, nil
}

func validateTaskExecutionParkInput(projectID, key, reason string) (string, error) {
	if err := model.ValidateProjectIdentifier(projectID); err != nil {
		return "", err
	}
	if err := model.ValidateCanonicalTaskID(key); err != nil {
		return "", err
	}
	reason = strings.TrimSpace(reason)
	if reason == "" || utf8.RuneCountInString(reason) > taskExecutionBlockReasonLimit {
		return "", fmt.Errorf("Task block/resume reason is required and bounded")
	}
	return reason, nil
}

func (s *Service) taskExecutionBlockedReason(ctx context.Context, state model.TaskExecutionState) (string, error) {
	phase, err := s.taskExecutionBlockedPhase(ctx, state)
	if err != nil {
		return "", err
	}
	return phase.Comment, nil
}

func (s *Service) taskExecutionBlockedPhase(ctx context.Context, state model.TaskExecutionState) (sqlitestore.TaskExecutionPhase, error) {
	phase, err := s.latestTaskExecutionPhase(ctx, state)
	if err != nil {
		return sqlitestore.TaskExecutionPhase{}, err
	}
	if phase.EventKind != "block" || phase.Status != model.TaskExecutionBlocked || phase.Stage != state.Stage || phase.Head != state.Head || phase.Branch != state.Branch || phase.Decision == "" || !model.IsTaskExecutionAgentActionable(phase.Decision, phase.Stage) || strings.TrimSpace(phase.Comment) == "" {
		return sqlitestore.TaskExecutionPhase{}, fmt.Errorf("Task blocked state has no valid durable block evidence")
	}
	return phase, nil
}

func (s *Service) latestTaskExecutionPhase(ctx context.Context, state model.TaskExecutionState) (sqlitestore.TaskExecutionPhase, error) {
	if s.Durability == nil {
		return sqlitestore.TaskExecutionPhase{}, fmt.Errorf("shared durability is unavailable")
	}
	phase, found, err := s.Durability.ReadLatestTaskExecutionPhase(ctx, state.ProjectID, state.TaskID, state.Stage)
	if err != nil {
		return sqlitestore.TaskExecutionPhase{}, err
	}
	wantRevision := state.ExecutionRevision - 1
	if state.Status == model.TaskExecutionBlocked || phase.EventKind == "resume" {
		wantRevision = state.ExecutionRevision
	}
	if !found || phase.ProjectID != state.ProjectID || phase.TaskID != state.TaskID || phase.ExecutionRevision != wantRevision || phase.TaskRevisionSHA256 != state.TaskRevisionSHA256 {
		return sqlitestore.TaskExecutionPhase{}, fmt.Errorf("Task execution phase history is not consistent with the durable state")
	}
	return phase, nil
}

func (s *Service) ensureNoInFlightWorkerTurn(ctx context.Context, state model.TaskExecutionState) error {
	operations, err := s.Durability.ListLocalOperationTurnSummaries(ctx, state.ProjectID)
	if err != nil {
		return fmt.Errorf("inspect Worker turn authority: %w", err)
	}
	for _, local := range operations {
		if local.Kind != "agent-prompt" && local.Kind != "agent-interrupt" && local.Kind != "agent-recover" {
			continue
		}
		if local.Status != "accepted" && local.Status != "running" && local.Status != "outcome_unknown" {
			continue
		}
		operation, readErr := s.readDurableMutation(local.OperationID)
		if readErr != nil || operation.ProjectID != state.ProjectID || operation.Kind != local.Kind || operation.Status != local.Status {
			return fmt.Errorf("Worker turn authority is unavailable; block/resume fails closed")
		}
		target, targetKnown, targetErr := durableWorkerTurnTarget(operation)
		if targetErr != nil {
			return fmt.Errorf("Worker turn authority is unavailable; block/resume fails closed: %w", targetErr)
		}
		if !targetKnown || target == "" || target == state.Agent {
			return fmt.Errorf("Worker turn is in flight or outcome is unknown; block/resume fails closed")
		}
	}
	return nil
}

func durableWorkerTurnTarget(operation durableMutationOperation) (string, bool, error) {
	switch operation.Kind {
	case "agent-prompt":
		var input AgentPromptInput
		if err := json.Unmarshal(operation.Input, &input); err != nil {
			return "", true, err
		}
		return input.AgentID, true, nil
	case "agent-interrupt":
		var input AgentInterruptInput
		if err := json.Unmarshal(operation.Input, &input); err != nil {
			return "", true, err
		}
		return input.AgentID, true, nil
	case "agent-recover":
		var input AgentRecoverInput
		if err := json.Unmarshal(operation.Input, &input); err != nil {
			return "", true, err
		}
		return input.AgentID, true, nil
	default:
		return "", false, nil
	}
}
