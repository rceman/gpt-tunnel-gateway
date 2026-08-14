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
	active, found, err := s.trainV2ActiveAttempt(ctx, in.ProjectID)
	if err != nil {
		return model.WatcherNudgeReceipt{}, err
	}
	if !found {
		return model.WatcherNudgeReceipt{}, fmt.Errorf("watcher nudge requires an active Train v2 Attempt")
	}
	if _, err := watcher.BindTrainAttempt(active.Train, active.Start, active.Runtime); err != nil {
		return model.WatcherNudgeReceipt{}, err
	}
	lock, err := s.acquireSessionSendLock(active.Attempt.AirelaySessionKey)
	if err != nil {
		return model.WatcherNudgeReceipt{}, fmt.Errorf("watcher nudge is already in progress")
	}
	defer func() { _ = lock.Release() }()
	result, sendErr := s.Airelay.PromptWithProvenance(ctx, active.Attempt.AirelaySessionKey, AgentSessionID(ctx), in.Text)
	receipt := model.WatcherNudgeReceipt{SchemaVersion: model.WatcherObservationSchemaVersion, ProjectID: in.ProjectID, TrainID: active.Train.ID, TaskID: active.Item.TaskID, ItemPosition: active.Item.Position, AttemptNumber: active.Attempt.Number, Delivered: sendErr == nil, ExitCode: result.ExitCode, StartedAt: result.StartedAt, FinishedAt: result.FinishedAt}
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
