package service

import (
	"context"
	"os"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/watcher"
)

func (s *Service) WatcherStatus(ctx context.Context, projectID string) (model.WatcherStatus, error) {
	local, err := s.projectConfig(projectID)
	if err != nil {
		return model.WatcherStatus{}, err
	}
	settings := local.Watcher.Effective()
	if enabled, enabledErr := s.TrainV2Enabled(ctx, projectID); enabledErr != nil {
		return model.WatcherStatus{}, enabledErr
	} else if enabled {
		return s.watcherStatusTrainV2(ctx, projectID)
	}
	plan, err := s.PlanRead(ctx, projectID)
	if err != nil {
		return model.WatcherStatus{}, err
	}
	state, err := watcher.LoadObservation(s.Config.StateDir, projectID)
	if err != nil && !os.IsNotExist(err) {
		return model.WatcherStatus{}, err
	}
	supervisor, err := watcher.LoadSupervisor(s.Config.StateDir, projectID)
	if err != nil {
		return model.WatcherStatus{}, err
	}
	status := model.WatcherStatus{
		SchemaVersion:  model.WatcherStatusSchemaVersion,
		ProjectID:      projectID,
		Mode:           settings.Mode,
		CadenceSeconds: settings.CadenceSeconds,
		TailLines:      settings.TailLines,
		NudgeEnabled:   settings.NudgeEnabled,
		WatcherAgentID: settings.AgentID,
		ActiveTaskID:   plan.ActiveTaskID,
		ActiveRunID:    plan.ActiveRunID,
		LastTickAt:     state.LastTickAt,
		LastUsefulAt:   state.LastUsefulAt,
		LastError:      state.LastError,
		Desired:        supervisor.Desired,
		Runtime:        supervisor.Runtime,
		InstanceID:     supervisor.InstanceID,
		LeaseID:        supervisor.LeaseID,
		LastNudgeAt:    supervisor.LastNudgeAt,
		RestartCount:   supervisor.RestartCount,
	}
	if settings.AgentID != "" {
		if binding, bindingErr := s.resolveWatcherBinding(projectID); bindingErr != nil {
			status.LastError = bindingErr.Error()
		} else {
			status.WatcherSession = binding.SessionKey
		}
	}
	if plan.ActiveRunID != "" {
		var run model.Run
		if readErr := s.Hub.ReadJSON(ctx, s.runPath(projectID, plan.ActiveRunID), &run); readErr == nil {
			status.TargetSession, status.RunStatus = run.SessionKey, run.Status
		}
	}
	if guide, guideErr := s.WatcherGuideRead(ctx, projectID); guideErr == nil {
		status.GuideRevision = guide.Revision
	}
	status.ObservationReset = state.TaskID != plan.ActiveTaskID || state.RunID != plan.ActiveRunID
	if status.LastTickAt.IsZero() {
		status.LastTickAt = s.watcherNow()
	}
	if err := model.ValidateWatcherStatus(status); err != nil {
		return model.WatcherStatus{}, err
	}
	return status, nil
}
