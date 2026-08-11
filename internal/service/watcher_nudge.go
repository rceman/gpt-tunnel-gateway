package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/watcher"
)

type WatcherNudgeInput struct {
	ProjectID string `json:"project_id"`
	Text      string `json:"text"`
}

func (s *Service) WatcherNudge(ctx context.Context, in WatcherNudgeInput) (model.WatcherNudgeReceipt, error) {
	if err := model.ValidateProjectIdentifier(in.ProjectID); err != nil {
		return model.WatcherNudgeReceipt{}, err
	}
	local, err := s.projectConfig(in.ProjectID)
	if err != nil {
		return model.WatcherNudgeReceipt{}, err
	}
	if !local.Watcher.Effective().NudgeEnabled {
		return model.WatcherNudgeReceipt{}, fmt.Errorf("watcher nudges are disabled for project %q", in.ProjectID)
	}
	if strings.TrimSpace(in.Text) == "" || len([]byte(in.Text)) > s.Airelay.MaxMessageBytes || strings.ContainsRune(in.Text, 0) {
		return model.WatcherNudgeReceipt{}, fmt.Errorf("invalid watcher nudge text")
	}
	plan, err := s.PlanRead(ctx, in.ProjectID)
	if err != nil {
		return model.WatcherNudgeReceipt{}, err
	}
	if plan.ActiveTaskID == "" || plan.ActiveRunID == "" {
		return model.WatcherNudgeReceipt{}, fmt.Errorf("watcher nudge requires an active task and run")
	}
	var run model.Run
	if err := s.Hub.ReadJSON(ctx, s.runPath(in.ProjectID, plan.ActiveRunID), &run); err != nil {
		return model.WatcherNudgeReceipt{}, err
	}
	if run.ProjectID != in.ProjectID || run.TaskID != plan.ActiveTaskID || run.ID != plan.ActiveRunID || run.SessionKey == "" {
		return model.WatcherNudgeReceipt{}, fmt.Errorf("active watcher run identity is stale")
	}
	if !watcherActiveStatus(run.Status) {
		return model.WatcherNudgeReceipt{}, fmt.Errorf("watcher nudge cannot target terminal run")
	}
	lock, err := s.acquireSessionSendLock(run.SessionKey)
	if err != nil {
		return model.WatcherNudgeReceipt{}, fmt.Errorf("watcher nudge is already in progress")
	}
	defer func() { _ = lock.Release() }()
	result, sendErr := s.Airelay.Prompt(ctx, run.SessionKey, in.Text)
	receipt := model.WatcherNudgeReceipt{
		SchemaVersion: model.WatcherObservationSchemaVersion,
		ProjectID:     in.ProjectID,
		TaskID:        run.TaskID,
		RunID:         run.ID,
		Delivered:     sendErr == nil,
		ExitCode:      result.ExitCode,
		StartedAt:     result.StartedAt,
		FinishedAt:    result.FinishedAt,
	}
	if sendErr != nil {
		receipt.Error = sendErr.Error()
	}
	if supervisor, stateErr := watcher.LoadSupervisor(s.Config.StateDir, in.ProjectID); stateErr == nil {
		supervisor.LastNudgeAt = receipt.FinishedAt
		_ = watcher.SaveSupervisor(s.Config.StateDir, supervisor)
	}
	if receipt.StartedAt.IsZero() {
		return model.WatcherNudgeReceipt{}, fmt.Errorf("watcher nudge produced no delivery receipt")
	}
	if sendErr != nil {
		return receipt, sendErr
	}
	return receipt, nil
}
