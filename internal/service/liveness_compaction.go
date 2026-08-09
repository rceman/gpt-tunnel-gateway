package service

import (
	"context"
	"os"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func inspectCompaction(run model.Run, tail string, events []model.RunOperationalEvent) compactionObservation {
	lines := strings.Split(strings.TrimRight(tail, "\r\n"), "\n")
	lastIndex := -1
	started, completed := false, false
	marker := ""
	for i, line := range lines {
		st, done := isCompactionLine(line)
		if st || done {
			lastIndex = i
			started, completed = st, done
			marker = strings.TrimSpace(line)
		}
	}
	if lastIndex >= 0 && completed {
		eventID := compactionEventID(run.ID, marker)
		meaningful, question := false, false
		for _, line := range lines[lastIndex+1:] {
			if strings.TrimSpace(line) == "" || isStaticAgentFooterLine(line) || isCompactionAcknowledgement(line) {
				continue
			}
			if questionRE.MatchString(line) {
				question = true
			}
			meaningful = true
		}
		return compactionObservation{
			Detected:        !meaningful && !question,
			Completed:       true,
			EventID:         eventID,
			Marker:          marker,
			MeaningfulAfter: meaningful,
			QuestionAfter:   question,
			TailDigest:      digestText(tail),
		}
	}
	if lastIndex >= 0 && started {
		return compactionObservation{
			Started:    true,
			Marker:     marker,
			TailDigest: digestText(tail),
		}
	}
	// A restart may lose the tail marker.  The durable event log remains the
	// only source used to continue a previously observed compaction event.
	var completedEvent *model.RunOperationalEvent
	for i := range events {
		if events[i].EventType == model.EventCompactionCompleted || events[i].EventType == model.EventResumeSent || events[i].EventType == model.EventResumeCompleted || events[i].EventType == model.EventStalledAfterCompaction {
			if completedEvent == nil || events[i].OccurredAt.After(completedEvent.OccurredAt) {
				copy := events[i]
				completedEvent = &copy
			}
		}
	}
	if completedEvent != nil && completedEvent.CompactionEventID != "" {
		return compactionObservation{
			Detected:   true,
			Completed:  true,
			EventID:    completedEvent.CompactionEventID,
			TailDigest: digestText(tail),
		}
	}
	return compactionObservation{TailDigest: digestText(tail)}
}

func (s *Service) latestProgress(ctx context.Context, projectID string) (progressEvidence, error) {
	local, err := s.projectConfig(projectID)
	if err != nil {
		return progressEvidence{}, err
	}
	plan, err := s.PlanRead(ctx, projectID)
	if err != nil {
		return progressEvidence{}, err
	}
	tasks, err := s.TaskList(ctx, projectID)
	if err != nil {
		return progressEvidence{}, err
	}
	runs, err := s.RunList(ctx, projectID)
	if err != nil {
		return progressEvidence{}, err
	}
	evidence := progressEvidence{}
	if len(tasks) > 0 {
		latestTask := tasks[0].Task
		latestState := tasks[0].State
		evidence.LatestTask = &latestTask
		evidence.LatestTaskState = &latestState
	}
	if len(runs) > 0 {
		run := runs[0]
		evidence.LatestRun = &run
	}
	active := []model.Run{}
	for _, run := range runs {
		if operationalActiveRun(run) {
			active = append(active, run)
		}
	}
	if plan.ActiveRunID != "" {
		for i := range active {
			if active[i].ID == plan.ActiveRunID {
				evidence.ActiveRun = &active[i]
				break
			}
		}
	}
	if evidence.ActiveRun == nil && len(active) == 1 {
		evidence.ActiveRun = &active[0]
	}
	if evidence.ActiveRun != nil {
		for i := range tasks {
			if tasks[i].Task.ID == evidence.ActiveRun.TaskID {
				task := tasks[i].Task
				state := tasks[i].State
				evidence.ActiveTask = &task
				evidence.TaskState = &state
				break
			}
		}
		evidence.Events, err = s.readOperationalEvents(evidence.ActiveRun.ID)
		if err != nil {
			return progressEvidence{}, err
		}
		if evidence.ActiveRun.CompletionPath != "" {
			if _, statErr := os.Stat(evidence.ActiveRun.CompletionPath); statErr == nil {
				evidence.Completion = true
			}
		}
		status, statusErr := s.Airelay.Status(ctx, local.AirelaySessionKey)
		evidence.Status, evidence.StatusError = status, statusErr
		tail, tailErr := s.Airelay.Tail(ctx, local.AirelaySessionKey, progressTailLines)
		evidence.Tail, evidence.TailError = tail.Stdout, tailErr
		evidence.Compaction = inspectCompaction(*evidence.ActiveRun, evidence.Tail, evidence.Events)
	} else {
		status, statusErr := s.Airelay.Status(ctx, local.AirelaySessionKey)
		evidence.Status, evidence.StatusError = status, statusErr
		tail, tailErr := s.Airelay.Tail(ctx, local.AirelaySessionKey, progressTailLines)
		evidence.Tail, evidence.TailError = tail.Stdout, tailErr
	}
	return evidence, nil
}

func progressSummary(task *model.Task, state *model.TaskState) *ProgressTask {
	if task == nil || state == nil {
		return nil
	}
	return &ProgressTask{
		ID:        task.ID,
		Title:     task.Title,
		Status:    state.Status,
		CreatedAt: task.CreatedAt,
	}
}

func progressRunSummary(run *model.Run) *ProgressRun {
	if run == nil {
		return nil
	}
	return &ProgressRun{
		ID:           run.ID,
		TaskID:       run.TaskID,
		Status:       run.Status,
		Branch:       run.Branch,
		BaseRevision: run.BaseRevision,
		CreatedAt:    run.CreatedAt,
		DispatchedAt: run.DispatchedAt,
		FinishedAt:   run.FinishedAt,
	}
}

func warningKind(warnings []string) string {
	for _, warning := range warnings {
		lower := strings.ToLower(warning)
		if strings.Contains(lower, "rate limited") || strings.Contains(lower, "rate-limited") || strings.Contains(lower, "too many requests") || strings.Contains(lower, "rate limit exceeded") || strings.Contains(lower, "rate limit unavailable") || strings.Contains(lower, "rate limit rejected") {
			return model.AgentStateRateLimited
		}
	}
	for _, warning := range warnings {
		lower := strings.ToLower(warning)
		if explicitCapacityUnavailable(lower) {
			return model.AgentStateCapacityBlocked
		}
	}
	return ""
}

func explicitCapacityUnavailable(warning string) bool {
	for _, exhausted := range []string{
		"capacity unavailable", "capacity exhausted", "capacity blocked", "capacity rejected", "capacity full", "no capacity",
		"quota exhausted", "quota exceeded", "quota unavailable", "quota blocked",
		"weekly limit exhausted", "weekly limit exceeded", "weekly limit unavailable", "weekly limit blocked",
	} {
		if strings.Contains(warning, exhausted) {
			return true
		}
	}
	return strings.Contains(warning, "0%") && (strings.Contains(warning, "quota") || strings.Contains(warning, "limit") || strings.Contains(warning, "capacity"))
}
