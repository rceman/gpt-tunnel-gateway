package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

type TaskExecutionResetInput struct {
	ProjectID string
	Key       string
	Reason    string
}

type TaskExecutionResetOutput struct {
	Key               string `json:"key"`
	Status            string `json:"status"`
	ExecutionRevision int    `json:"execution_revision"`
}

type taskExecutionResetEvidence struct {
	SchemaVersion      int    `json:"schema_version"`
	ProjectID          string `json:"project_id"`
	TaskID             string `json:"task_id"`
	TaskRevision       int    `json:"task_revision"`
	TaskRevisionSHA256 string `json:"task_revision_sha256"`
	ExecutionRevision  int    `json:"execution_revision"`
	Status             string `json:"status"`
	Stage              string `json:"stage"`
	Worktree           string `json:"worktree"`
	BaseHead           string `json:"base_head"`
	Head               string `json:"head"`
	Branch             string `json:"branch"`
	Agent              string `json:"agent"`
	Reason             string `json:"reason"`
}

func validateTaskExecutionResetInput(in TaskExecutionResetInput) (string, error) {
	reason, err := validateTaskExecutionParkInput(in.ProjectID, in.Key, in.Reason)
	if err != nil {
		return "", err
	}
	if strings.ContainsRune(reason, 0) {
		return "", fmt.Errorf("Task reset reason is invalid")
	}
	return reason, nil
}

func (s *Service) TaskExecutionReset(ctx context.Context, in TaskExecutionResetInput) (TaskExecutionResetOutput, error) {
	if err := authority.RequirePlanner(ctx); err != nil {
		return TaskExecutionResetOutput{}, err
	}
	reason, err := validateTaskExecutionResetInput(in)
	if err != nil {
		return TaskExecutionResetOutput{}, err
	}
	if s.Durability == nil {
		return TaskExecutionResetOutput{}, fmt.Errorf("shared durability is unavailable")
	}
	s.durableMutationMu.Lock()
	defer s.durableMutationMu.Unlock()
	s.taskExecutionMu.Lock()
	defer s.taskExecutionMu.Unlock()
	if _, active := s.taskExecutionVerifyInFlight[in.Key]; active {
		return TaskExecutionResetOutput{}, fmt.Errorf("Task verification is in flight")
	}

	state, found, err := s.Durability.ReadTaskExecutionState(ctx, in.ProjectID, in.Key)
	if err != nil {
		return TaskExecutionResetOutput{}, err
	}
	if !found {
		for _, stage := range []string{"code", "tests", "rebase", "integration"} {
			phases, phaseErr := s.Durability.ReadTaskExecutionPhases(ctx, in.ProjectID, in.Key, stage)
			if phaseErr != nil {
				return TaskExecutionResetOutput{}, phaseErr
			}
			if len(phases) > 0 {
				return TaskExecutionResetOutput{}, fmt.Errorf("Task execution phase history exists without current execution state")
			}
		}
		return TaskExecutionResetOutput{}, fmt.Errorf("Task has no execution state to reset")
	}
	if state.Status == model.TaskExecutionAbandoned {
		return s.replayTaskExecutionReset(ctx, state, reason)
	}

	var evidence taskExecutionResetEvidence
	if state.Status == model.TaskExecutionResetting {
		state, found, err = s.readExecutionForMutation(ctx, in.ProjectID, in.Key)
		if err != nil || !found {
			if err != nil {
				return TaskExecutionResetOutput{}, err
			}
			return TaskExecutionResetOutput{}, fmt.Errorf("Task execution reset authority is missing")
		}
		phase, phaseFound, phaseErr := s.Durability.ReadLatestTaskExecutionResetPhase(ctx, in.ProjectID, in.Key)
		if phaseErr != nil {
			return TaskExecutionResetOutput{}, phaseErr
		}
		if !phaseFound || phase.EventKind != "reset_start" || phase.ExecutionRevision != state.ExecutionRevision {
			return TaskExecutionResetOutput{}, fmt.Errorf("Task execution reset has no matching durable start evidence")
		}
		evidence, err = validateTaskExecutionResetPhase(phase)
		if err != nil || !taskExecutionResetEvidenceBindsState(evidence, state, model.TaskExecutionResetting) {
			return TaskExecutionResetOutput{}, fmt.Errorf("Task execution reset start evidence does not match its state")
		}
		if evidence.Reason != reason {
			return TaskExecutionResetOutput{}, fmt.Errorf("conflicting Task reset mutation")
		}
	} else {
		state, found, err = s.readExecutionForMutation(ctx, in.ProjectID, in.Key)
		if err != nil || !found {
			if err != nil {
				return TaskExecutionResetOutput{}, err
			}
			return TaskExecutionResetOutput{}, fmt.Errorf("Task has no execution state to reset")
		}
		if !taskExecutionResettableStatus(state.Status) || state.Stage != "code" {
			return TaskExecutionResetOutput{}, fmt.Errorf("Task execution is not at a resettable nonterminal boundary")
		}
		if phase, phaseFound, phaseErr := s.Durability.ReadLatestTaskExecutionResetPhase(ctx, in.ProjectID, in.Key); phaseErr != nil {
			return TaskExecutionResetOutput{}, phaseErr
		} else if phaseFound && phase.EventKind == "reset_start" {
			return TaskExecutionResetOutput{}, fmt.Errorf("Task execution has an incomplete prior reset")
		}
		evidence = taskExecutionResetEvidenceFromState(state, reason)
		if err := validateTaskExecutionResetEvidence(evidence); err != nil {
			return TaskExecutionResetOutput{}, err
		}
		if _, err := s.taskAuthoringReadForExecution(ctx, in.ProjectID, in.Key); err != nil {
			return TaskExecutionResetOutput{}, err
		}
		if err := s.ensureTaskExecutionResetSafe(ctx, state, evidence, false); err != nil {
			return TaskExecutionResetOutput{}, err
		}
		state, err = s.beginTaskExecutionReset(ctx, state, evidence)
		if err != nil {
			return TaskExecutionResetOutput{}, err
		}
	}

	if _, err := s.taskAuthoringReadForExecution(ctx, in.ProjectID, in.Key); err != nil {
		return TaskExecutionResetOutput{}, err
	}
	if err := s.ensureTaskExecutionResetSafe(ctx, state, evidence, true); err != nil {
		return TaskExecutionResetOutput{}, err
	}
	project, err := s.EffectiveProjectConfig(in.ProjectID)
	if err != nil {
		return TaskExecutionResetOutput{}, err
	}
	if err := s.Git.RemoveTaskWorktreeForReset(ctx, project, s.Config.StateDir, in.ProjectID, in.Key, evidence.Head, evidence.Branch); err != nil {
		return TaskExecutionResetOutput{}, fmt.Errorf("remove safe Task reset lane: %w", err)
	}

	state.Status = model.TaskExecutionAbandoned
	state.ExecutionRevision++
	state.UpdatedAt = s.durableNow()
	comment, err := json.Marshal(evidence)
	if err != nil {
		return TaskExecutionResetOutput{}, err
	}
	phase := sqlitestore.TaskExecutionPhase{
		TaskID:             state.TaskID,
		ProjectID:          state.ProjectID,
		ExecutionRevision:  state.ExecutionRevision,
		Stage:              state.Stage,
		Status:             state.Status,
		Head:               state.Head,
		Branch:             state.Branch,
		TaskRevisionSHA256: state.TaskRevisionSHA256,
		EventKind:          "reset",
		Decision:           evidence.Status,
		Comment:            string(comment),
		CreatedAt:          state.UpdatedAt,
	}
	if err := s.Durability.TransitionTaskExecutionState(ctx, state, state.ExecutionRevision-1, phase); err != nil {
		return TaskExecutionResetOutput{}, err
	}
	return TaskExecutionResetOutput{
		Key:               in.Key,
		Status:            model.TaskExecutionPlanned,
		ExecutionRevision: state.ExecutionRevision,
	}, nil
}

func taskExecutionResettableStatus(status string) bool {
	return status == model.TaskExecutionDispatched || status == model.TaskExecutionInProgress || status == model.TaskExecutionBlocked
}

func taskExecutionResetEvidenceFromState(state model.TaskExecutionState, reason string) taskExecutionResetEvidence {
	return taskExecutionResetEvidence{
		SchemaVersion:      1,
		ProjectID:          state.ProjectID,
		TaskID:             state.TaskID,
		TaskRevision:       state.TaskRevision,
		TaskRevisionSHA256: state.TaskRevisionSHA256,
		ExecutionRevision:  state.ExecutionRevision,
		Status:             state.Status,
		Stage:              state.Stage,
		Worktree:           state.Worktree,
		BaseHead:           state.BaseHead,
		Head:               state.Head,
		Branch:             state.Branch,
		Agent:              state.Agent,
		Reason:             reason,
	}
}

func validateTaskExecutionResetEvidence(evidence taskExecutionResetEvidence) error {
	if evidence.SchemaVersion != 1 || model.ValidateProjectIdentifier(evidence.ProjectID) != nil || model.ValidateCanonicalTaskID(evidence.TaskID) != nil || evidence.TaskRevision < 1 || model.ValidateSHA256(evidence.TaskRevisionSHA256) != nil || evidence.ExecutionRevision < 1 || !taskExecutionResettableStatus(evidence.Status) || evidence.Stage != "code" || model.ValidateCommitSHA(evidence.BaseHead) != nil || evidence.Head != evidence.BaseHead || model.ValidateBranch(evidence.Branch) != nil || !strings.HasPrefix(evidence.Branch, "task/"+evidence.TaskID+"-") || evidence.Worktree != taskExecutionWorktree(evidence.TaskID, strings.ToLower(evidence.Head[:8])) || model.ValidateObjectIdentifier(evidence.Agent) != nil || evidence.Reason == "" || strings.TrimSpace(evidence.Reason) != evidence.Reason || strings.ContainsRune(evidence.Reason, 0) || utf8.RuneCountInString(evidence.Reason) > taskExecutionBlockReasonLimit {
		return fmt.Errorf("invalid Task execution reset evidence")
	}
	return nil
}

func decodeTaskExecutionResetEvidence(comment string) (taskExecutionResetEvidence, error) {
	var evidence taskExecutionResetEvidence
	if err := json.Unmarshal([]byte(comment), &evidence); err != nil {
		return taskExecutionResetEvidence{}, fmt.Errorf("invalid Task execution reset evidence")
	}
	canonical, err := json.Marshal(evidence)
	if err != nil || string(canonical) != comment {
		return taskExecutionResetEvidence{}, fmt.Errorf("noncanonical Task execution reset evidence")
	}
	if err := validateTaskExecutionResetEvidence(evidence); err != nil {
		return taskExecutionResetEvidence{}, err
	}
	return evidence, nil
}

func validateTaskExecutionResetPhase(phase sqlitestore.TaskExecutionPhase) (taskExecutionResetEvidence, error) {
	evidence, err := decodeTaskExecutionResetEvidence(phase.Comment)
	if err != nil {
		return taskExecutionResetEvidence{}, err
	}
	wantRevision := evidence.ExecutionRevision + 1
	wantStatus := model.TaskExecutionResetting
	if phase.EventKind == "reset" {
		wantRevision++
		wantStatus = model.TaskExecutionAbandoned
	}
	if (phase.EventKind != "reset_start" && phase.EventKind != "reset") || phase.ProjectID != evidence.ProjectID || phase.TaskID != evidence.TaskID || phase.ExecutionRevision != wantRevision || phase.Stage != "code" || phase.Status != wantStatus || phase.Head != evidence.Head || phase.Branch != evidence.Branch || phase.TaskRevisionSHA256 != evidence.TaskRevisionSHA256 || phase.Decision != evidence.Status {
		return taskExecutionResetEvidence{}, fmt.Errorf("Task execution reset phase does not match its evidence")
	}
	return evidence, nil
}

func taskExecutionResetEvidenceBindsState(evidence taskExecutionResetEvidence, state model.TaskExecutionState, status string) bool {
	wantRevision := evidence.ExecutionRevision
	if status == model.TaskExecutionResetting {
		wantRevision++
	} else if status == model.TaskExecutionAbandoned {
		wantRevision += 2
	}
	return state.ProjectID == evidence.ProjectID && state.TaskID == evidence.TaskID && state.TaskRevision == evidence.TaskRevision && state.TaskRevisionSHA256 == evidence.TaskRevisionSHA256 && state.ExecutionRevision == wantRevision && state.Status == status && state.Stage == evidence.Stage && state.Worktree == evidence.Worktree && state.BaseHead == evidence.BaseHead && state.Head == evidence.Head && state.Branch == evidence.Branch && state.Agent == evidence.Agent
}

func (s *Service) beginTaskExecutionReset(ctx context.Context, state model.TaskExecutionState, evidence taskExecutionResetEvidence) (model.TaskExecutionState, error) {
	comment, err := json.Marshal(evidence)
	if err != nil {
		return model.TaskExecutionState{}, err
	}
	state.Status = model.TaskExecutionResetting
	state.ExecutionRevision++
	state.UpdatedAt = s.durableNow()
	phase := sqlitestore.TaskExecutionPhase{
		TaskID:             state.TaskID,
		ProjectID:          state.ProjectID,
		ExecutionRevision:  state.ExecutionRevision,
		Stage:              state.Stage,
		Status:             state.Status,
		Head:               state.Head,
		Branch:             state.Branch,
		TaskRevisionSHA256: state.TaskRevisionSHA256,
		EventKind:          "reset_start",
		Decision:           evidence.Status,
		Comment:            string(comment),
		CreatedAt:          state.UpdatedAt,
	}
	if err := s.Durability.TransitionTaskExecutionState(ctx, state, state.ExecutionRevision-1, phase); err != nil {
		return model.TaskExecutionState{}, err
	}
	return state, nil
}

func (s *Service) replayTaskExecutionReset(ctx context.Context, state model.TaskExecutionState, reason string) (TaskExecutionResetOutput, error) {
	phase, found, err := s.Durability.ReadLatestTaskExecutionResetPhase(ctx, state.ProjectID, state.TaskID)
	if err != nil {
		return TaskExecutionResetOutput{}, err
	}
	if !found || phase.EventKind != "reset" || phase.ExecutionRevision != state.ExecutionRevision {
		return TaskExecutionResetOutput{}, fmt.Errorf("Task execution has no matching completed reset receipt")
	}
	evidence, err := validateTaskExecutionResetPhase(phase)
	if err != nil || !taskExecutionResetEvidenceBindsState(evidence, state, model.TaskExecutionAbandoned) {
		return TaskExecutionResetOutput{}, fmt.Errorf("Task reset receipt does not match its retired execution")
	}
	if evidence.Reason != reason {
		return TaskExecutionResetOutput{}, fmt.Errorf("conflicting Task reset mutation")
	}
	return TaskExecutionResetOutput{
		Key:               state.TaskID,
		Status:            model.TaskExecutionPlanned,
		ExecutionRevision: state.ExecutionRevision,
	}, nil
}

func (s *Service) latestTaskExecutionResetRevision(ctx context.Context, projectID, taskID string) (int, error) {
	phase, found, err := s.Durability.ReadLatestTaskExecutionResetTerminalPhase(ctx, projectID, taskID)
	if err != nil {
		return 0, err
	}
	if !found {
		return 0, nil
	}
	if _, err := validateTaskExecutionResetPhase(phase); err != nil {
		return 0, err
	}
	return phase.ExecutionRevision, nil
}

func taskExecutionPhasesAfterRevision(phases []sqlitestore.TaskExecutionPhase, revision int) []sqlitestore.TaskExecutionPhase {
	filtered := make([]sqlitestore.TaskExecutionPhase, 0, len(phases))
	for _, phase := range phases {
		if phase.ExecutionRevision > revision {
			filtered = append(filtered, phase)
		}
	}
	return filtered
}

func (s *Service) ensureTaskExecutionResetSafe(ctx context.Context, state model.TaskExecutionState, evidence taskExecutionResetEvidence, allowMissingLane bool) error {
	if state.Stage != "code" || (state.Status != model.TaskExecutionResetting && !taskExecutionResettableStatus(state.Status)) || evidence.ProjectID != state.ProjectID || evidence.TaskID != state.TaskID {
		return fmt.Errorf("Task execution is not at a resettable nonterminal boundary")
	}
	if state.Status == model.TaskExecutionResetting {
		if !taskExecutionResetEvidenceBindsState(evidence, state, model.TaskExecutionResetting) {
			return fmt.Errorf("Task execution reset state does not match its durable start evidence")
		}
	} else if state.Status != evidence.Status || state.ExecutionRevision != evidence.ExecutionRevision || !taskExecutionResetEvidenceBindsState(evidence, state, state.Status) {
		return fmt.Errorf("Task execution changed before reset could be recorded")
	}
	if state.Status == model.TaskExecutionBlocked {
		if _, err := s.taskExecutionBlockedPhase(ctx, state); err != nil {
			return err
		}
	}
	if err := s.ensureNoInFlightWorkerTurn(ctx, state); err != nil {
		return err
	}
	if err := s.ensureTaskExecutionResetOperations(ctx, state); err != nil {
		return err
	}
	worker, err := s.ResolveProjectWorker(ctx, state.ProjectID)
	if err != nil || worker.Agent.AgentID != state.Agent || !worker.ControllerReachable || !strings.EqualFold(strings.TrimSpace(worker.RuntimeState), "idle") {
		return fmt.Errorf("attached Worker is unavailable or has in-flight work")
	}
	resetRevision, err := s.latestTaskExecutionResetRevision(ctx, state.ProjectID, state.TaskID)
	if err != nil {
		return fmt.Errorf("inspect prior Task reset history: %w", err)
	}
	for _, stage := range []string{"code", "tests", "rebase", "integration"} {
		phases, err := s.Durability.ReadTaskExecutionPhases(ctx, state.ProjectID, state.TaskID, stage)
		if err != nil {
			return fmt.Errorf("inspect Task reset history: %w", err)
		}
		var refreshPhases []sqlitestore.TaskExecutionPhase
		for _, phase := range phases {
			if phase.ExecutionRevision <= resetRevision {
				continue
			}
			if phase.Stage != state.Stage {
				return fmt.Errorf("Task execution has lifecycle evidence outside its current stage")
			}
			switch phase.EventKind {
			case "block", "resume", "refresh":
				refreshPhases = append(refreshPhases, phase)
			case "reset_start":
				found, phaseErr := validateTaskExecutionResetPhase(phase)
				if phaseErr != nil || found != evidence || state.Status != model.TaskExecutionResetting {
					return fmt.Errorf("Task reset history does not match the requested execution")
				}
			case "reset":
				return fmt.Errorf("Task reset history conflicts with its current execution state")
			default:
				return fmt.Errorf("Task execution has immutable submission, review, integration, or candidate evidence")
			}
		}
		if _, err := validateTaskExecutionRefreshChain(state, refreshPhases, resetRevision); err != nil {
			return err
		}
	}
	verification, found, err := s.Durability.ReadLatestTaskExecutionVerification(ctx, state.ProjectID, state.TaskID)
	if err != nil {
		return fmt.Errorf("inspect Task verification evidence: %w", err)
	}
	if found || verification.TaskID != "" {
		return fmt.Errorf("Task execution has immutable verification evidence")
	}
	completion, found, err := s.Durability.ReadTaskCompletionEvent(ctx, state.ProjectID, state.TaskID)
	if err != nil {
		return fmt.Errorf("inspect Task completion evidence: %w", err)
	}
	if found || completion.TaskID != "" {
		return fmt.Errorf("Task has immutable completion evidence")
	}
	lane, err := s.taskExecutionLane(state.ProjectID, state.TaskID, state)
	if err != nil {
		return err
	}
	info, err := os.Lstat(lane.Root)
	if os.IsNotExist(err) && allowMissingLane {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect canonical Task lane: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("canonical Task lane is not a managed directory")
	}
	actual, branch, clean, err := s.Git.CurrentHead(ctx, lane)
	if err != nil || !clean || branch != state.Branch || actual != state.Head || actual != state.BaseHead {
		return fmt.Errorf("Task lane is dirty, candidate-bearing, or mismatched")
	}
	rebasing, err := s.Git.TaskRebaseInProgress(ctx, lane)
	if err != nil || rebasing {
		return fmt.Errorf("Task lane is in an unsafe rebase state")
	}
	return nil
}

func (s *Service) ensureTaskExecutionResetOperations(ctx context.Context, state model.TaskExecutionState) error {
	operations, err := s.Durability.ListLocalOperations(ctx, state.ProjectID)
	if err != nil {
		return fmt.Errorf("inspect Task execution operation authority: %w", err)
	}
	for _, local := range operations {
		if local.Kind != taskExecutionSubmitKind && local.Kind != "task-execution-test" && local.Kind != "task-execution-integrate" {
			continue
		}
		if local.Status != "accepted" && local.Status != "running" && local.Status != "outcome_unknown" {
			continue
		}
		operation, err := s.readDurableMutation(local.OperationID)
		if err != nil || operation.ProjectID != state.ProjectID || operation.Kind != local.Kind || operation.Status != local.Status {
			return fmt.Errorf("Task execution operation authority is unavailable")
		}
		matches := false
		switch local.Kind {
		case taskExecutionSubmitKind:
			var input taskExecutionSubmitInput
			if err := json.Unmarshal(operation.Input, &input); err != nil || input.TaskID == "" {
				return fmt.Errorf("Task submission operation authority is invalid")
			}
			matches = input.TaskID == state.TaskID && (input.ExecutionRevision == 0 || input.ExecutionRevision == state.ExecutionRevision || state.Status == model.TaskExecutionResetting && input.ExecutionRevision == state.ExecutionRevision-1)
		case "task-execution-test":
			var input TaskExecutionTestInput
			if err := json.Unmarshal(operation.Input, &input); err != nil || input.ProjectID != state.ProjectID || input.Key == "" {
				return fmt.Errorf("Task verification operation authority is invalid")
			}
			matches = input.Key == state.TaskID
		case "task-execution-integrate":
			var input TaskExecutionIntegrateInput
			if err := json.Unmarshal(operation.Input, &input); err != nil || input.ProjectID == "" || input.Key == "" {
				return fmt.Errorf("Task integration operation authority is invalid")
			}
			matches = input.ProjectID == state.ProjectID && input.Key == state.TaskID
		}
		if matches {
			return fmt.Errorf("Task execution has in-flight submission, verification, or integration work")
		}
	}
	return nil
}

func (s *Service) validateTaskExecutionResetTerminalState(ctx context.Context, state model.TaskExecutionState) error {
	phase, found, err := s.Durability.ReadLatestTaskExecutionResetPhase(ctx, state.ProjectID, state.TaskID)
	if err != nil {
		return err
	}
	if !found || phase.EventKind != "reset" || phase.ExecutionRevision != state.ExecutionRevision {
		return fmt.Errorf("Task execution has no matching completed reset receipt")
	}
	evidence, err := validateTaskExecutionResetPhase(phase)
	if err != nil || !taskExecutionResetEvidenceBindsState(evidence, state, model.TaskExecutionAbandoned) {
		return fmt.Errorf("Task reset receipt does not match its retired execution")
	}
	return nil
}
