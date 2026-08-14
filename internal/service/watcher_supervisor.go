package service

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/watcher"
)

func watcherLockName(projectID string) string { return "watcher-" + projectID }

func (s *Service) withWatcherLock(projectID string, fn func() error) error {
	lock, err := lockfile.Acquire(filepath.Join(s.Config.StateDir, "locks"), watcherLockName(projectID))
	if err != nil {
		return fmt.Errorf("watcher operation is already in progress")
	}
	defer func() { _ = lock.Release() }()
	return fn()
}

func (s *Service) WatcherStart(ctx context.Context, projectID string) (model.WatcherSupervisorState, error) {
	local, err := s.projectConfig(projectID)
	if err != nil {
		return model.WatcherSupervisorState{}, err
	}
	if local.Watcher.Effective().Mode == "disabled" {
		return model.WatcherSupervisorState{}, fmt.Errorf("watcher is disabled for project %q", projectID)
	}
	binding, err := s.resolveWatcherBinding(projectID)
	if err != nil {
		return model.WatcherSupervisorState{}, err
	}
	var state model.WatcherSupervisorState
	err = s.withWatcherLock(projectID, func() error {
		var loadErr error
		state, loadErr = watcher.LoadSupervisor(s.Config.StateDir, projectID)
		if loadErr != nil {
			return loadErr
		}
		if state.Desired == "running" {
			return nil
		}
		instance, idErr := model.NewID()
		if idErr != nil {
			return idErr
		}
		now := s.watcherNow()
		state = model.WatcherSupervisorState{
			SchemaVersion:  model.WatcherStatusSchemaVersion,
			ProjectID:      projectID,
			Desired:        "running",
			Runtime:        "starting",
			InstanceID:     instance,
			LeaseID:        instance,
			WatcherAgentID: binding.AgentID,
			WatcherSession: binding.SessionKey,
			StartedAt:      now,
			RestartCount:   state.RestartCount,
		}
		return watcher.SaveSupervisor(s.Config.StateDir, state)
	})
	return state, err
}

func (s *Service) WatcherStop(ctx context.Context, projectID string) (model.WatcherSupervisorState, error) {
	if _, err := s.projectConfig(projectID); err != nil {
		return model.WatcherSupervisorState{}, err
	}
	var state model.WatcherSupervisorState
	err := s.withWatcherLock(projectID, func() error {
		var loadErr error
		state, loadErr = watcher.LoadSupervisor(s.Config.StateDir, projectID)
		if loadErr != nil {
			return loadErr
		}
		state.Desired = "stopped"
		state.Runtime = "stopped"
		state.LastError = ""
		return watcher.SaveSupervisor(s.Config.StateDir, state)
	})
	return state, err
}

func (s *Service) watcherSupervisorTickLocked(ctx context.Context, projectID string) error {
	state, err := watcher.LoadSupervisor(s.Config.StateDir, projectID)
	if err != nil {
		return err
	}
	if state.Desired != "running" {
		return nil
	}
	local, err := s.projectConfig(projectID)
	if err != nil {
		return err
	}
	settings := local.Watcher.Effective()
	binding, bindingErr := s.resolveWatcherBinding(projectID)
	if bindingErr != nil {
		state.Runtime = "degraded"
		state.LastError = bindingErr.Error()
		return watcher.SaveSupervisor(s.Config.StateDir, state)
	}
	state.WatcherAgentID = binding.AgentID
	state.WatcherSession = binding.SessionKey
	observation, observeErr := s.WatcherObserve(ctx, WatcherObserveInput{ProjectID: projectID})
	state.LastTickAt = s.watcherNow()
	if observeErr != nil {
		state.Runtime = "degraded"
		state.LastError = observeErr.Error()
		return watcher.SaveSupervisor(s.Config.StateDir, state)
	}
	state.Runtime = "running"
	state.LastError = observation.Error
	state.ActiveTaskID = observation.TaskID
	state.TargetSession = observation.TargetSession
	state.TrainID = observation.TrainID
	state.TrainItemPosition = observation.TrainItemPosition
	state.TrainAgentID = observation.TrainAgentID
	if observation.Useful {
		state.LastUsefulAt = observation.ObservedAt
		if nudged, nudgeErr := s.promptWatcherIfIdle(ctx, projectID, observation, settings, binding, state.LastNudgeAt); nudgeErr != nil {
			state.LastError = nudgeErr.Error()
		} else if nudged {
			state.LastNudgeAt = s.watcherNow()
		}
	}
	return watcher.SaveSupervisor(s.Config.StateDir, state)
}

func (s *Service) promptWatcherIfIdle(ctx context.Context, projectID string, observation model.WatcherObservation, settings config.WatcherSettings, binding WatcherAgentBinding, lastNudge time.Time) (bool, error) {
	if !settings.NudgeEnabled || binding.SessionKey == "" || !observation.Useful || observation.Tail == "" {
		return false, nil
	}
	if !lastNudge.IsZero() && s.watcherNow().Sub(lastNudge) < time.Duration(settings.CadenceSeconds)*time.Second {
		return false, nil
	}
	status, err := s.Airelay.Status(ctx, binding.SessionKey)
	if err != nil || status.State != "idle" {
		return false, nil
	}
	message := "Watcher observation for " + projectID + ": " + strings.TrimSpace(observation.Tail)
	if len([]byte(message)) > s.Airelay.MaxMessageBytes {
		message = string([]byte(message)[:s.Airelay.MaxMessageBytes])
	}
	lock, err := s.acquireSessionSendLock(binding.SessionKey)
	if err != nil {
		return false, nil
	}
	defer func() { _ = lock.Release() }()
	if _, err := s.Airelay.PromptWithProvenance(ctx, binding.SessionKey, AgentSessionID(ctx), message); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Service) WatcherSupervisorTick(ctx context.Context, projectID string) error {
	return s.withWatcherLock(projectID, func() error {
		return s.watcherSupervisorTickLocked(ctx, projectID)
	})
}

// RunWatcherSupervisors is the Gateway-owned scheduler. It is intentionally
// one process-level loop and has no cron/Python fallback. It reconciles
// desired state after restart and ticks only projects explicitly started.
func (s *Service) RunWatcherSupervisors(ctx context.Context) {
	lock, err := lockfile.Acquire(filepath.Join(s.Config.StateDir, "locks"), "watcher-scheduler")
	if err != nil {
		return
	}
	defer func() { _ = lock.Release() }()
	schedulerID, err := model.NewID()
	if err != nil {
		return
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	s.runWatcherSupervisors(ctx, ticker.C, schedulerID)
}

func (s *Service) runWatcherSupervisors(ctx context.Context, ticks <-chan time.Time, schedulerID string) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticks:
			ids, idsErr := s.EffectiveProjectIDs()
			if idsErr != nil {
				continue
			}
			for _, projectID := range ids {
				local, localErr := s.projectConfig(projectID)
				if localErr != nil || local.Watcher.Effective().Mode == "disabled" {
					continue
				}
				cadence := time.Duration(local.Watcher.Effective().CadenceSeconds) * time.Second
				_ = s.withWatcherLock(projectID, func() error {
					state, stateErr := watcher.LoadSupervisor(s.Config.StateDir, projectID)
					if stateErr != nil || state.Desired != "running" {
						return nil
					}
					if state.LeaseID != schedulerID {
						if state.LeaseID != "" {
							state.RestartCount++
						}
						state.LeaseID = schedulerID
						state.Runtime = "starting"
						if err := watcher.SaveSupervisor(s.Config.StateDir, state); err != nil {
							return err
						}
					}
					if !state.LastTickAt.IsZero() && s.watcherNow().Sub(state.LastTickAt) < cadence {
						return nil
					}
					return s.watcherSupervisorTickLocked(ctx, projectID)
				})
			}
		}
	}
}
