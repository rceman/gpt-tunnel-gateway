package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
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

func watcherSnapshotDigest(lines []string) string { return watcherDigest(strings.Join(lines, "\x00")) }

func (s *Service) WatcherObserve(ctx context.Context, in WatcherObserveInput) (model.WatcherObservation, error) {
	if err := model.ValidateProjectIdentifier(in.ProjectID); err != nil {
		return model.WatcherObservation{}, err
	}
	enabled, err := s.TrainV2Enabled(ctx, in.ProjectID)
	if err != nil {
		return model.WatcherObservation{}, err
	}
	if !enabled {
		return model.WatcherObservation{}, errRunAuthorityRetired
	}
	return s.watcherObserveTrainV2(ctx, in)
}
