package service

import (
	"context"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s *Service) activeOperationalRunsForSession(ctx context.Context, session string) (int, error) {
	projects, err := s.ProjectList(ctx)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, project := range projects {
		runs, err := s.RunList(ctx, project.ID)
		if err != nil {
			return 0, err
		}
		for _, candidate := range runs {
			if candidate.SessionKey == session && operationalActiveRun(candidate) {
				count++
			}
		}
	}
	return count, nil
}

func meaningfulTailAfterResume(tail string) bool {
	for _, line := range strings.Split(tail, "\n") {
		line = strings.TrimSpace(line)
		if !meaningfulAgentLine(line) {
			continue
		}
		return true
	}
	return false
}

func (s *Service) observeResumeProgress(ctx context.Context, run model.Run, now time.Time) error {
	events, err := s.readOperationalEvents(run.ID)
	if err != nil {
		return err
	}
	resume := latestEvent(events, model.EventResumeSent, "")
	if resume == nil {
		return nil
	}
	tail, err := s.Airelay.Tail(ctx, run.SessionKey, progressTailLines)
	if err != nil && strings.TrimSpace(tail.Stdout) == "" {
		return nil
	}
	if digestText(tail.Stdout) != resume.TailDigest && meaningfulTailAfterResume(tail.Stdout) {
		meaningful, eventErr := newOperationalEvent(run, model.EventMeaningfulOutput, resume.CompactionEventID, tail.Stdout, "", tail.ExitCode, model.AgentStateRunning)
		if eventErr != nil {
			return eventErr
		}
		if latestEvent(events, model.EventMeaningfulOutput, resume.CompactionEventID) == nil {
			return s.appendOperationalEvent(meaningful)
		}
		return nil
	}
	if now.Sub(resume.OccurredAt) >= resumeObservationWindow && latestEvent(events, model.EventStalledAfterCompaction, resume.CompactionEventID) == nil {
		stalled, eventErr := newOperationalEvent(run, model.EventStalledAfterCompaction, resume.CompactionEventID, tail.Stdout, "", tail.ExitCode, model.AgentStateStalled)
		if eventErr != nil {
			return eventErr
		}
		return s.appendOperationalEvent(stalled)
	}
	return nil
}
