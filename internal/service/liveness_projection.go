package service

import (
	"context"
	"fmt"
)

func (s *Service) RunResume(ctx context.Context, id string) (RunResumeResult, error) {
	return s.runResume(ctx, id, false)
}

func (s *Service) runResume(ctx context.Context, id string, automatic bool) (RunResumeResult, error) {
	run, err := s.findRun(ctx, id)
	if err != nil {
		return RunResumeResult{}, err
	}
	if err := requireCanonicalRun(run); err != nil {
		return RunResumeResult{}, err
	}
	if err := s.ensureRunOwned(run); err != nil {
		return RunResumeResult{}, err
	}
	if !operationalActiveRun(run) {
		return RunResumeResult{}, fmt.Errorf("run is not active")
	}
	task, err := s.findTask(ctx, run.TaskID)
	if err != nil {
		return RunResumeResult{}, err
	}
	local, err := s.projectConfig(run.ProjectID)
	if err != nil {
		return RunResumeResult{}, err
	}
	if run.SessionKey == "" {
		return RunResumeResult{}, fmt.Errorf("run has no resolved agent session")
	}
	lock, err := s.acquireSessionSendLock(run.SessionKey)
	if err != nil {
		return RunResumeResult{}, fmt.Errorf("agent session resume is already in progress")
	}
	defer func() { _ = lock.Release() }()
	return s.resumeRunLocked(ctx, run, task, local, automatic)
}
