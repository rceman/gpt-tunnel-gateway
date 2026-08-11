package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
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
		TrainID: state.TrainID, TrainItemPosition: state.TrainItemPosition, TrainAgentID: state.TrainAgentID, ActiveTaskID: state.TaskID, ActiveRunID: state.RunID, TargetSession: state.SessionKey,
		LastTickAt: state.LastTickAt, LastUsefulAt: state.LastUsefulAt, LastError: state.LastError, Desired: supervisor.Desired, Runtime: supervisor.Runtime, InstanceID: supervisor.InstanceID, LeaseID: supervisor.LeaseID, LastNudgeAt: supervisor.LastNudgeAt, RestartCount: supervisor.RestartCount,
	}
	if settings.AgentID != "" {
		if binding, bindingErr := s.resolveWatcherBinding(projectID); bindingErr != nil {
			status.LastError = bindingErr.Error()
		} else {
			status.WatcherSession = binding.SessionKey
		}
	}
	if run, found, runErr := s.trainV2ActiveRun(ctx, projectID); runErr != nil {
		status.LastError = runErr.Error()
	} else if found {
		status.TargetSession, status.RunStatus = run.SessionKey, run.Status
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

func (s *Service) trainV2ActiveRun(ctx context.Context, projectID string) (model.Run, bool, error) {
	runs, err := s.RunList(ctx, projectID)
	if err != nil {
		return model.Run{}, false, err
	}
	for _, run := range runs {
		if !run.Historical && run.TrainID != "" && watcherActiveStatus(run.Status) {
			return run, true, nil
		}
	}
	return model.Run{}, false, nil
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
	run, found, err := s.trainV2ActiveRun(ctx, in.ProjectID)
	if err != nil {
		return model.WatcherObservation{}, err
	}
	if !found {
		changed := state.TrainID != "" || state.TaskID != "" || state.RunID != ""
		state.TrainID, state.TrainItemPosition, state.TrainAgentID = "", 0, ""
		state.TaskID, state.RunID, state.SessionKey = "", "", ""
		state.LastTickAt, state.LastError = now, ""
		state.LastTail, state.SeenDigests, state.SnapshotDigest, state.Cursor = "", []string{}, "", ""
		if err := watcher.SaveObservation(s.Config.StateDir, state); err != nil {
			return model.WatcherObservation{}, err
		}
		return model.WatcherObservation{SchemaVersion: model.WatcherObservationSchemaVersion, ProjectID: in.ProjectID, Lines: lines, IdentityChanged: changed, ObservedAt: now}, nil
	}
	train, err := s.TrainV2Read(ctx, in.ProjectID, run.TrainID)
	if err != nil {
		return model.WatcherObservation{}, err
	}
	var start model.TrainV2StartRecord
	startPath := hub.ProtocolRoot + "/projects/" + in.ProjectID + "/train-v2-starts/" + run.TrainID + ".json"
	if err := s.Hub.ReadJSON(ctx, startPath, &start); err != nil {
		return model.WatcherObservation{}, fmt.Errorf("read train watcher start: %w", err)
	}
	runtime, err := trainv2.ReadRuntime(s.Config.StateDir, in.ProjectID, run.TrainID)
	if err != nil {
		return model.WatcherObservation{}, fmt.Errorf("read train watcher runtime: %w", err)
	}
	binding, err := watcher.BindTrainRun(train, start, runtime, run)
	if err != nil {
		state.LastError = err.Error()
		_ = watcher.SaveObservation(s.Config.StateDir, state)
		return model.WatcherObservation{}, err
	}
	identityChanged := state.TrainID != binding.TrainID || state.TaskID != binding.TaskID || state.RunID != binding.RunID
	state.TrainID, state.TrainItemPosition, state.TrainAgentID = binding.TrainID, binding.ItemPosition, binding.AgentID
	state.TaskID, state.RunID, state.SessionKey = binding.TaskID, binding.RunID, binding.SessionKey
	state.LastTickAt, state.LastError = now, ""
	if identityChanged {
		state.SeenDigests, state.SnapshotDigest, state.Cursor, state.LastTail = []string{}, "", "", ""
	}
	observation := model.WatcherObservation{SchemaVersion: model.WatcherObservationSchemaVersion, ProjectID: in.ProjectID, TrainID: binding.TrainID, TrainItemPosition: binding.ItemPosition, TrainAgentID: binding.AgentID, TaskID: binding.TaskID, RunID: binding.RunID, TargetSession: binding.SessionKey, RunStatus: run.Status, IdentityChanged: identityChanged, Lines: lines, ObservedAt: now}
	if !watcherActiveStatus(run.Status) {
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
