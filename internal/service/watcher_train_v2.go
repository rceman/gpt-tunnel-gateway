package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
	"github.com/rceman/gpt-tunnel-gateway/internal/watcher"
)

func (s *Service) watcherStatusTrainV2(ctx context.Context, projectID string) (model.WatcherStatus, error) {
	local, err := s.projectConfig(projectID)
	if err != nil {
		return model.WatcherStatus{}, err
	}
	settings := local.Watcher.Effective()
	state, err := watcher.LoadObservation(s.Config.StateDir, projectID)
	if err != nil {
		return model.WatcherStatus{}, err
	}
	supervisor, err := watcher.LoadSupervisor(s.Config.StateDir, projectID)
	if err != nil {
		return model.WatcherStatus{}, err
	}
	status := model.WatcherStatus{
		SchemaVersion: model.WatcherStatusSchemaVersion, ProjectID: projectID, Mode: settings.Mode, CadenceSeconds: settings.CadenceSeconds, TailLines: settings.TailLines, NudgeEnabled: settings.NudgeEnabled,
		TrainID: state.TrainID, TrainItemPosition: state.TrainItemPosition, TrainAgentID: state.TrainAgentID, ActiveTaskID: state.TaskID, TargetSession: state.SessionKey,
		LastTickAt: state.LastTickAt, LastUsefulAt: state.LastUsefulAt, LastError: state.LastError, Desired: supervisor.Desired, Runtime: supervisor.Runtime, InstanceID: supervisor.InstanceID, LeaseID: supervisor.LeaseID, LastNudgeAt: supervisor.LastNudgeAt, RestartCount: supervisor.RestartCount,
	}
	if settings.AgentID != "" {
		if binding, bindingErr := s.resolveWatcherBinding(projectID); bindingErr != nil {
			status.LastError = bindingErr.Error()
		} else {
			status.WatcherSession = binding.SessionKey
		}
	}
	if active, found, runErr := s.trainV2ActiveAttempt(ctx, projectID); runErr != nil {
		status.LastError = runErr.Error()
	} else if found {
		status.TargetSession, status.AttemptStatus = active.Attempt.AirelaySessionKey, active.Attempt.Status
	}
	if guide, guideErr := s.WatcherGuideRead(ctx, projectID); guideErr == nil {
		status.GuideRevision = guide.Revision
	}
	if status.LastTickAt.IsZero() {
		status.LastTickAt = s.watcherNow()
	}
	if err := model.ValidateWatcherStatus(status); err != nil {
		return model.WatcherStatus{}, err
	}
	return status, nil
}

type activeTrainAttempt struct {
	Train   model.TrainV2
	Start   model.TrainV2StartRecord
	Runtime trainv2.RuntimeBinding
	Item    model.TrainV2Item
	Attempt model.TrainV2Attempt
}

func (s *Service) trainV2ActiveAttempt(ctx context.Context, projectID string) (activeTrainAttempt, bool, error) {
	trains, err := s.TrainV2List(ctx, TrainV2ListInput{
		ProjectID: projectID,
		Limit:     model.MaxTrainV2Items,
	})
	if err != nil {
		return activeTrainAttempt{}, false, err
	}
	for _, train := range trains.Trains {
		if train.Status != model.TrainV2Running && train.Status != model.TrainV2Paused && train.Status != model.TrainV2Blocked {
			continue
		}
		runtime, err := trainv2.ReadRuntime(s.Config.StateDir, projectID, train.ID)
		if err != nil {
			continue
		}
		itemPosition, attemptNumber, taskID, ok := trainv2.ActiveAttemptIdentity(train)
		if !ok || itemPosition < 0 || itemPosition >= len(train.Items) || attemptNumber == 0 {
			continue
		}
		item := train.Items[itemPosition]
		if item.TaskID != taskID || attemptNumber > uint64(len(item.Attempts)) {
			continue
		}
		attempt := item.Attempts[attemptNumber-1]
		if attempt.Status != model.TrainV2AttemptRunning {
			continue
		}
		policy, policyErr := s.ProjectWorkflowPolicyRead(ctx, projectID)
		if policyErr != nil {
			continue
		}
		project, projectErr := s.ProjectRead(ctx, projectID)
		if projectErr != nil {
			continue
		}
		start := trainv2.DeriveStartRecord(train, item, attempt, policy, project, attempt.StartedAt)
		if _, err := watcher.BindTrainAttempt(train, start, runtime); err != nil {
			return activeTrainAttempt{}, false, err
		}
		return activeTrainAttempt{
			Train:   train,
			Start:   start,
			Runtime: runtime,
			Item:    item,
			Attempt: attempt,
		}, true, nil
	}
	return activeTrainAttempt{}, false, nil
}

func (s *Service) watcherObserveTrainV2(ctx context.Context, in WatcherObserveInput) (model.WatcherObservation, error) {
	local, err := s.projectConfig(in.ProjectID)
	if err != nil {
		return model.WatcherObservation{}, err
	}
	settings := watcherSettings(local)
	lines := in.Lines
	if lines == 0 {
		lines = settings.TailLines
	}
	if lines < 1 || lines > model.WatcherMaxTailLines {
		return model.WatcherObservation{}, fmt.Errorf("invalid watcher tail bounds")
	}
	now := s.watcherNow()
	state, err := watcher.LoadObservation(s.Config.StateDir, in.ProjectID)
	if err != nil {
		return model.WatcherObservation{}, err
	}
	active, found, err := s.trainV2ActiveAttempt(ctx, in.ProjectID)
	if err != nil {
		return model.WatcherObservation{}, err
	}
	if !found {
		changed := state.TrainID != "" || state.TaskID != ""
		state.TrainID, state.TrainItemPosition, state.TrainAgentID = "", 0, ""
		state.TaskID, state.SessionKey = "", ""
		state.LastTickAt, state.LastError = now, ""
		state.LastTail, state.SeenDigests, state.SnapshotDigest, state.Cursor = "", []string{}, "", ""
		if err := watcher.SaveObservation(s.Config.StateDir, state); err != nil {
			return model.WatcherObservation{}, err
		}
		return model.WatcherObservation{SchemaVersion: model.WatcherObservationSchemaVersion, ProjectID: in.ProjectID, Lines: lines, IdentityChanged: changed, ObservedAt: now}, nil
	}
	train, start, runtime, attempt := active.Train, active.Start, active.Runtime, active.Attempt
	binding, err := watcher.BindTrainAttempt(train, start, runtime)
	if err != nil {
		state.LastError = err.Error()
		_ = watcher.SaveObservation(s.Config.StateDir, state)
		return model.WatcherObservation{}, err
	}
	identityChanged := state.TrainID != binding.TrainID || state.TaskID != binding.TaskID || state.TrainItemPosition != binding.ItemPosition
	state.TrainID, state.TrainItemPosition, state.TrainAgentID = binding.TrainID, binding.ItemPosition, binding.AgentID
	state.TaskID, state.SessionKey = binding.TaskID, binding.SessionKey
	state.LastTickAt, state.LastError = now, ""
	if identityChanged {
		state.SeenDigests, state.SnapshotDigest, state.Cursor, state.LastTail = []string{}, "", "", ""
	}
	observation := model.WatcherObservation{SchemaVersion: model.WatcherObservationSchemaVersion, ProjectID: in.ProjectID, TrainID: binding.TrainID, TrainItemPosition: binding.ItemPosition, TrainAgentID: binding.AgentID, TaskID: binding.TaskID, TargetSession: binding.SessionKey, AttemptStatus: attempt.Status, IdentityChanged: identityChanged, Lines: lines, ObservedAt: now}
	if attempt.Status != model.TrainV2AttemptRunning {
		observation.Terminal = true
		if err := watcher.SaveObservation(s.Config.StateDir, state); err != nil {
			return model.WatcherObservation{}, err
		}
		return observation, nil
	}
	result, err := s.Airelay.TailSnapshot(ctx, binding.SessionKey, lines)
	if err != nil {
		state.LastError = err.Error()
		_ = watcher.SaveObservation(s.Config.StateDir, state)
		return model.WatcherObservation{}, fmt.Errorf("read train watcher agent tail: %w", err)
	}
	snapshot := normalizeWatcherLines(agentSnapshotLines(result.Stdout))
	if len(snapshot) > lines {
		snapshot = snapshot[len(snapshot)-lines:]
	}
	seen := make(map[string]bool, len(state.SeenDigests))
	for _, digest := range state.SeenDigests {
		seen[digest] = true
	}
	newLines, newDigests := make([]string, 0, len(snapshot)), make([]string, 0, len(snapshot))
	for _, line := range snapshot {
		digest := watcherDigest(line)
		if seen[digest] {
			continue
		}
		seen[digest] = true
		newLines = append(newLines, line)
		newDigests = append(newDigests, digest)
	}
	state.SeenDigests = append(state.SeenDigests, newDigests...)
	if len(state.SeenDigests) > settings.SeenRetention {
		state.SeenDigests = state.SeenDigests[len(state.SeenDigests)-settings.SeenRetention:]
	}
	state.SnapshotDigest = watcherSnapshotDigest(snapshot)
	state.Cursor, state.LastTail = state.SnapshotDigest, strings.Join(newLines, "\n")
	if len(newLines) > 0 {
		state.LastUsefulAt = now
	}
	observation.Tail, observation.NewDigests, observation.SnapshotDigest = state.LastTail, newDigests, state.SnapshotDigest
	observation.Useful = len(newLines) > 0
	if err := watcher.SaveObservation(s.Config.StateDir, state); err != nil {
		return model.WatcherObservation{}, err
	}
	return observation, nil
}
