package service

import (
	"context"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func normalIdleTail(tail string) bool {
	if strings.TrimSpace(tail) == "" || hasExplicitQuestion(tail) {
		return false
	}
	lower := strings.ToLower(tail)
	return strings.Contains(lower, "idle") || strings.Contains(lower, "ready") || strings.Contains(lower, "prompt") || strings.Contains(lower, "waiting")
}

func isStaticAgentFooterLine(line string) bool {
	lower := strings.ToLower(strings.TrimSpace(line))
	if lower == "" || lower == "ready" || lower == "idle" || lower == "waiting" || lower == "default prompt" || lower == "idle prompt ready" {
		return true
	}
	for _, prefix := range []string{
		"model:", "context:", "context window:", "workspace:", "working directory:", "cwd:",
		"prompt: default", "prompt: idle", "status: idle", "status: ready", "status: waiting",
		"state: idle", "state: ready", "state: waiting", "controller: reachable",
	} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

func meaningfulAgentLine(line string) bool {
	line = strings.TrimSpace(line)
	if line == "" || isStaticAgentFooterLine(line) || isCompactionAcknowledgement(line) {
		return false
	}
	if _, started := isCompactionLine(line); started {
		return false
	}
	if _, completed := isCompactionLine(line); completed {
		return false
	}
	return !hasExplicitQuestion(line)
}

func classifyProgress(e progressEvidence, activeCount int, now time.Time) (string, string, string) {
	if activeCount == 0 {
		if !e.Status.ControllerReachable {
			return model.AgentStateError, "CONTROLLER_UNREACHABLE", "inspect_controller"
		}
		if hasComponentError(e, "agent_status") {
			return model.AgentStateUnknown, "AGENT_STATUS_UNAVAILABLE", "inspect_agent_status"
		}
		if e.Status.State == "running" {
			return model.AgentStateRunning, "none", "wait_for_agent_progress"
		}
		if e.Status.State == "waiting" && hasExplicitQuestion(e.Tail) {
			return model.AgentStateWaitingForInput, "WAITING_FOR_AGENT_INPUT", "answer_agent_question"
		}
		if e.Status.State == "error" && !normalIdleTail(e.Tail) && !e.Status.ControllerReachable {
			return model.AgentStateError, "CONTROLLER_UNREACHABLE", "inspect_controller"
		}
		return model.AgentStateIdle, "none", "await_authorized_task"
	}
	if e.ActiveRun == nil {
		return model.AgentStateUnknown, "MULTIPLE_ACTIVE_RUNS", "repair_operational_state"
	}
	if !e.Status.ControllerReachable {
		return model.AgentStateError, "CONTROLLER_UNREACHABLE", "inspect_controller"
	}
	if hasComponentError(e, "agent_status", "agent_tail") {
		return model.AgentStateUnknown, "AGENT_PROGRESS_UNAVAILABLE", "inspect_agent_status"
	}
	if e.Completion {
		return model.AgentStateFinalizationPending, "FINALIZATION_PENDING", "run_finalize"
	}
	if hasExplicitQuestion(e.Tail) {
		return model.AgentStateWaitingForInput, "WAITING_FOR_AGENT_INPUT", "answer_agent_question"
	}
	if e.Compaction.Started && !e.Compaction.Completed {
		return model.AgentStateCompacting, "COMPACTION_IN_PROGRESS", "wait_for_compaction"
	}
	if e.Compaction.Detected && !e.Compaction.MeaningfulAfter {
		resume := latestEvent(e.Events, model.EventResumeSent, e.Compaction.EventID)
		meaningful := latestEvent(e.Events, model.EventMeaningfulOutput, e.Compaction.EventID)
		if meaningful != nil && (resume == nil || meaningful.OccurredAt.After(resume.OccurredAt)) {
			return model.AgentStateRunning, "none", "wait_for_agent_progress"
		}
		if resume != nil {
			if now.Sub(resume.OccurredAt) >= resumeObservationWindow {
				return model.AgentStateStalled, "STALLED_AFTER_COMPACTION", "review_stalled_after_compaction"
			}
			return model.AgentStateCompactedResuming, "COMPACTION_RESUME_PENDING", "wait_for_agent_progress"
		}
		return model.AgentStateCompactedIdle, "COMPACTION_RECOVERY_AVAILABLE", "run_resume"
	}
	if warning := warningKind(e.Status.CapacityWarnings); warning != "" {
		return warning, strings.ToUpper(warning), "wait_for_capacity"
	}
	if e.ActiveRun.Status == "awaiting_result" && e.Status.State == "waiting" {
		return model.AgentStateCompletionPending, "COMPLETION_PENDING", "inspect_completion_state"
	}
	if e.Status.State == "running" {
		return model.AgentStateRunning, "none", "wait_for_agent_progress"
	}
	if e.Status.State == "waiting" {
		return model.AgentStateWaitingForInput, "WAITING_FOR_AGENT_INPUT", "answer_agent_question"
	}
	if e.Status.State == "idle" || (e.Status.State == "error" && normalIdleTail(e.Tail)) {
		return model.AgentStateStalled, "AGENT_STALLED", "inspect_agent_progress"
	}
	if e.Status.State == "error" || e.StatusError != nil || e.TailError != nil {
		return model.AgentStateError, "CONTROLLER_UNREACHABLE", "inspect_controller"
	}
	return model.AgentStateUnknown, "UNKNOWN_AGENT_STATE", "inspect_agent_progress"
}

func lastMeaningfulActivity(run *model.Run, events []model.RunOperationalEvent) *time.Time {
	var latest *time.Time
	if run != nil {
		when := run.CreatedAt
		if run.DispatchedAt != nil {
			when = *run.DispatchedAt
		}
		latest = &when
	}
	for _, event := range events {
		if event.EventType != model.EventMeaningfulOutput && event.EventType != model.EventResumeCompleted {
			continue
		}
		when := event.OccurredAt
		if latest == nil || when.After(*latest) {
			latest = &when
		}
	}
	return latest
}

func (s *Service) projectProgress(ctx context.Context, projectID string) (ProjectProgress, error) {
	evidence, err := s.latestProgress(ctx, projectID)
	if err != nil {
		return ProjectProgress{}, err
	}
	runs, err := s.RunList(ctx, projectID)
	if err != nil {
		return ProjectProgress{}, err
	}
	activeCount := 0
	for _, run := range runs {
		if operationalActiveRun(run) {
			activeCount++
		}
	}
	now := time.Now().UTC()
	return progressSnapshot(evidence, activeCount, now), nil
}

// projectProgressFromInputs assembles the canonical snapshot from one bounded
// set of already-fetched components. It intentionally reports component error
// codes rather than raw command, path, or controller errors.
