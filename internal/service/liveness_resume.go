package service

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s *Service) resumeRunLocked(ctx context.Context, run model.Run, task model.Task, local config.ProjectConfig, automatic bool) (RunResumeResult, error) {
	active, err := s.activeOperationalRunsForSession(ctx, run.SessionKey)
	if err != nil {
		return RunResumeResult{}, err
	}
	if active != 1 {
		return RunResumeResult{}, fmt.Errorf("resume requires exactly one active operational run for the session")
	}
	plan, err := s.PlanRead(ctx, run.ProjectID)
	if err != nil {
		return RunResumeResult{}, err
	}
	if plan.ActiveTaskID != task.ID || plan.ActiveRunID != run.ID {
		return RunResumeResult{}, fmt.Errorf("resume requires current plan ownership")
	}
	taskState, err := s.taskState(ctx, task)
	if err != nil {
		return RunResumeResult{}, err
	}
	if taskState.Status == "completed" || taskState.Status == "cancelled" || taskState.Status == "superseded" {
		return RunResumeResult{}, fmt.Errorf("resume requires a non-terminal task")
	}
	if run.CompletionPath != "" {
		if _, statErr := os.Stat(run.CompletionPath); statErr == nil {
			return RunResumeResult{}, fmt.Errorf("resume blocked while completion is pending finalization")
		}
	}
	wt, err := s.Git.WorktreeStatus(ctx, local)
	if err != nil {
		return RunResumeResult{}, err
	}
	if wt.Branch != task.Branch || worktreeHasConflict(wt.Porcelain) {
		return RunResumeResult{}, fmt.Errorf("resume requires the declared task branch and a non-conflicted worktree")
	}
	status, statusErr := s.Airelay.Status(ctx, run.SessionKey)
	if statusErr != nil && !status.ControllerReachable {
		return RunResumeResult{
			RunID:               run.ID,
			ControllerReachable: false,
			State:               model.AgentStateError,
			Error:               "controller_unreachable",
		}, fmt.Errorf("agent controller is unreachable")
	}
	if !status.ControllerReachable {
		return RunResumeResult{
			RunID:               run.ID,
			ControllerReachable: false,
			State:               model.AgentStateError,
			Error:               "controller_unreachable",
		}, fmt.Errorf("agent controller is unreachable")
	}
	events, err := s.readOperationalEvents(run.ID)
	if err != nil {
		return RunResumeResult{}, err
	}
	tail, tailErr := s.Airelay.Tail(ctx, run.SessionKey, progressTailLines)
	if tailErr != nil && strings.TrimSpace(tail.Stdout) == "" {
		if !hasDurableCompactionEvidence(run, events) {
			return RunResumeResult{}, fmt.Errorf("unable to observe bounded agent tail")
		}
	}
	observation := inspectCompaction(run, tail.Stdout, events)
	if observation.Started && !observation.Completed {
		if observation.EventID == "" {
			observation.EventID = compactionEventID(run.ID, observation.Marker)
		}
		started, e := newOperationalEvent(run, model.EventCompactionStarted, observation.EventID, tail.Stdout, "", status.ExitCode, model.AgentStateCompacting)
		if e != nil {
			return RunResumeResult{}, e
		}
		if latestEvent(events, model.EventCompactionStarted, observation.EventID) == nil {
			if err := s.appendOperationalEvent(started); err != nil {
				return RunResumeResult{}, err
			}
		}
		return RunResumeResult{}, fmt.Errorf("confirmed resumable compaction is required")
	}
	if !observation.Detected || observation.QuestionAfter || hasExplicitQuestion(tail.Stdout) {
		return RunResumeResult{}, fmt.Errorf("confirmed resumable compaction is required")
	}
	if warning := warningKind(status.CapacityWarnings); warning != "" {
		return RunResumeResult{}, fmt.Errorf("resume blocked by %s", warning)
	}
	if prior := latestEvent(events, model.EventResumeSent, observation.EventID); prior != nil {
		return RunResumeResult{}, fmt.Errorf("resume already sent for compaction event")
	}
	message := canonicalResumeMessage(task, run)
	if len(message) > s.Airelay.MaxMessageBytes {
		return RunResumeResult{}, fmt.Errorf("generated recovery message exceeds bound")
	}
	if completed := latestEvent(events, model.EventMeaningfulOutput, observation.EventID); completed != nil {
		return RunResumeResult{}, fmt.Errorf("resume event has already produced meaningful output")
	}
	observed, err := newOperationalEvent(run, model.EventCompactionObserved, observation.EventID, tail.Stdout, "", status.ExitCode, model.AgentStateCompactedIdle)
	if err != nil {
		return RunResumeResult{}, err
	}
	if latestEvent(events, model.EventCompactionObserved, observation.EventID) == nil {
		if err := s.appendOperationalEvent(observed); err != nil {
			return RunResumeResult{}, err
		}
	}
	compactionEvent, err := newOperationalEvent(run, model.EventCompactionCompleted, observation.EventID, tail.Stdout, "", status.ExitCode, model.AgentStateCompactedIdle)
	if err != nil {
		return RunResumeResult{}, err
	}
	if latestEvent(events, model.EventCompactionCompleted, observation.EventID) == nil {
		if err := s.appendOperationalEvent(compactionEvent); err != nil {
			return RunResumeResult{}, err
		}
	}
	reservation, err := newOperationalEvent(run, model.EventResumeSent, observation.EventID, tail.Stdout, message, -1, model.AgentStateCompactedResuming)
	if err != nil {
		return RunResumeResult{}, err
	}
	if err := s.appendOperationalEvent(reservation); err != nil {
		return RunResumeResult{}, err
	}
	result, promptErr := s.Airelay.Prompt(ctx, run.SessionKey, message)
	resultState := model.AgentStateCompactedResuming
	resumeResult := RunResumeResult{
		RunID:               run.ID,
		CompactionEventID:   observation.EventID,
		State:               resultState,
		Sent:                promptErr == nil,
		ExitCode:            result.ExitCode,
		ControllerReachable: status.ControllerReachable,
		MessageDigest:       digestText(message),
	}
	if promptErr != nil {
		failed, e := newOperationalEvent(run, model.EventResumeFailed, observation.EventID, "", message, result.ExitCode, model.AgentStateError)
		if e != nil {
			return resumeResult, e
		}
		if e := s.appendOperationalEvent(failed); e != nil {
			return resumeResult, e
		}
		resumeResult.Error = "resume delivery failed"
		return resumeResult, fmt.Errorf("resume delivery failed")
	}
	completedEvent, e := newOperationalEvent(run, model.EventResumeCompleted, observation.EventID, "", message, result.ExitCode, resultState)
	if e != nil {
		return resumeResult, e
	}
	if e := s.appendOperationalEvent(completedEvent); e != nil {
		return resumeResult, e
	}
	return resumeResult, nil
}

func hasDurableCompactionEvidence(run model.Run, events []model.RunOperationalEvent) bool {
	observation := inspectCompaction(run, "", events)
	return observation.Detected || observation.Started
}
