package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/tailcursor"
	"github.com/rceman/gpt-tunnel-gateway/internal/watcher"
)

type WatcherObserveInput struct {
	ProjectID string `json:"project_id"`
	Lines     int    `json:"lines,omitempty"`
}

var (
	watcherWorkingLine = regexp.MustCompile(`^Working \([^\r\n]*\)$`)
	watcherWaitingLine = regexp.MustCompile(`^Waiting for background terminal \([^\r\n]*\)$`)
)

func (s *Service) watcherNow() time.Time {
	if s.clock != nil {
		return s.clock().UTC()
	}
	return time.Now().UTC()
}

func watcherSettings(local config.ProjectConfig) config.WatcherSettings {
	return local.Watcher.Effective()
}

func watcherActiveStatus(status string) bool {
	switch status {
	case "created", "dispatching", "dispatched", "awaiting_result", "cancel_requested":
		return true
	default:
		return false
	}
}

func watcherDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func normalizeWatcherLine(line string) string {
	line = strings.TrimRight(line, "\r")
	if watcherWorkingLine.MatchString(line) {
		return "Working (...)"
	}
	if watcherWaitingLine.MatchString(line) {
		return "Waiting for background terminal (...)"
	}
	return line
}

func normalizeWatcherLines(lines []string) []string {
	result := make([]string, 0, len(lines))
	for _, line := range lines {
		result = append(result, normalizeWatcherLine(line))
	}
	return result
}

func watcherSnapshotDigest(lines []string) string {
	return watcherDigest(strings.Join(lines, "\x00"))
}

func (s *Service) WatcherObserve(ctx context.Context, in WatcherObserveInput) (model.WatcherObservation, error) {
	if err := model.ValidateProjectIdentifier(in.ProjectID); err != nil {
		return model.WatcherObservation{}, err
	}
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

	plan, err := s.PlanRead(ctx, in.ProjectID)
	if err != nil {
		return model.WatcherObservation{}, err
	}
	observation := model.WatcherObservation{
		SchemaVersion: model.WatcherObservationSchemaVersion,
		ProjectID:     in.ProjectID,
		Lines:         lines,
		ObservedAt:    now,
	}
	identityChanged := state.TaskID != plan.ActiveTaskID || state.RunID != plan.ActiveRunID
	observation.IdentityChanged = identityChanged
	state.ProjectID = in.ProjectID
	state.TaskID = plan.ActiveTaskID
	state.RunID = plan.ActiveRunID
	state.LastTickAt = now
	state.LastError = ""
	if identityChanged {
		state.SessionKey = ""
		state.SnapshotDigest = ""
		state.SeenDigests = []string{}
		state.LastTail = ""
	}

	if plan.ActiveTaskID == "" {
		state.TaskID, state.RunID, state.SessionKey = "", "", ""
		if err := watcher.SaveObservation(s.Config.StateDir, state); err != nil {
			return model.WatcherObservation{}, err
		}
		return observation, nil
	}
	var task model.Task
	if err := s.Hub.ReadJSON(ctx, s.taskPath(in.ProjectID, plan.ActiveTaskID), &task); err != nil {
		state.LastError = err.Error()
		_ = watcher.SaveObservation(s.Config.StateDir, state)
		return model.WatcherObservation{}, fmt.Errorf("read active watcher task: %w", err)
	}
	if task.ProjectID != in.ProjectID || task.ID != plan.ActiveTaskID {
		return model.WatcherObservation{}, fmt.Errorf("active watcher task identity mismatch")
	}
	observation.TaskID = task.ID
	if plan.ActiveRunID == "" {
		if err := watcher.SaveObservation(s.Config.StateDir, state); err != nil {
			return model.WatcherObservation{}, err
		}
		return observation, nil
	}

	var run model.Run
	if err := s.Hub.ReadJSON(ctx, s.runPath(in.ProjectID, plan.ActiveRunID), &run); err != nil {
		state.LastError = err.Error()
		_ = watcher.SaveObservation(s.Config.StateDir, state)
		return model.WatcherObservation{}, fmt.Errorf("read active watcher run: %w", err)
	}
	if run.ProjectID != in.ProjectID || run.TaskID != task.ID || run.ID != plan.ActiveRunID || run.SessionKey == "" {
		return model.WatcherObservation{}, fmt.Errorf("active watcher run identity mismatch")
	}
	observation.RunID, observation.TargetSession, observation.RunStatus = run.ID, run.SessionKey, run.Status
	state.SessionKey = run.SessionKey
	if !watcherActiveStatus(run.Status) {
		observation.Terminal = true
		if err := watcher.SaveObservation(s.Config.StateDir, state); err != nil {
			return model.WatcherObservation{}, err
		}
		return observation, nil
	}

	result, err := s.Airelay.TailSnapshot(ctx, run.SessionKey, tailcursor.MaxSnapshotLines)
	if err != nil {
		state.LastError = err.Error()
		_ = watcher.SaveObservation(s.Config.StateDir, state)
		return model.WatcherObservation{}, fmt.Errorf("read watcher agent tail: %w", err)
	}
	snapshot := normalizeWatcherLines(agentSnapshotLines(result.Stdout))
	if len(snapshot) > lines {
		snapshot = snapshot[len(snapshot)-lines:]
	}
	observation.SnapshotDigest = watcherSnapshotDigest(snapshot)
	seen := make(map[string]bool, len(state.SeenDigests))
	for _, digest := range state.SeenDigests {
		seen[digest] = true
	}
	newLines := make([]string, 0, len(snapshot))
	newDigests := make([]string, 0, len(snapshot))
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
	retention := settings.SeenRetention
	if len(state.SeenDigests) > retention {
		state.SeenDigests = state.SeenDigests[len(state.SeenDigests)-retention:]
	}
	state.SnapshotDigest = observation.SnapshotDigest
	state.Cursor = observation.SnapshotDigest
	state.LastTail = strings.Join(newLines, "\n")
	if len(newLines) > 0 {
		state.LastUsefulAt = now
	}
	observation.Tail = state.LastTail
	observation.NewDigests = newDigests
	observation.Useful = len(newLines) > 0
	if err := watcher.SaveObservation(s.Config.StateDir, state); err != nil {
		return model.WatcherObservation{}, err
	}
	return observation, nil
}
