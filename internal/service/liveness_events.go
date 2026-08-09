package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s *Service) appendOperationalEvent(event model.RunOperationalEvent) (retErr error) {
	if err := model.ValidateRunOperationalEvent(event); err != nil {
		return err
	}
	if err := fsutil.EnsureDir(s.localRunDir(event.RunID), 0o700); err != nil {
		return err
	}
	lock, err := lockfile.Acquire(filepath.Join(s.Config.StateDir, "locks"), eventLockName(event.RunID))
	if err != nil {
		return err
	}
	defer func() {
		if err := lock.Release(); err != nil {
			retErr = errors.Join(retErr, err)
		}
	}()
	current, err := s.readOperationalEvents(event.RunID)
	if err != nil {
		return err
	}
	if len(current) >= maxOperationalEvents {
		return fmt.Errorf("operational event count exceeds bound")
	}
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	line := make([]byte, len(data)+1)
	copy(line, data)
	line[len(data)] = '\n'
	path := s.operationalEventPath(event.RunID)
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if len(existing) > maxOperationalEventFile || len(existing)+len(line) > maxOperationalEventFile {
		return fmt.Errorf("operational event log exceeds bound")
	}
	if info, statErr := os.Lstat(path); statErr == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("operational event log is not a regular file")
		}
	} else if !os.IsNotExist(statErr) {
		return statErr
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	closeWith := func(cause error) error {
		return errors.Join(cause, f.Close())
	}
	if err := f.Chmod(0o600); err != nil {
		return closeWith(err)
	}
	n, err := f.Write(line)
	if err != nil {
		return closeWith(err)
	}
	if n != len(line) {
		return closeWith(io.ErrShortWrite)
	}
	if err := f.Sync(); err != nil {
		return closeWith(err)
	}
	return f.Close()
}

func newOperationalEvent(run model.Run, eventType, compactionID, tail, message string, exitCode int, resultingState string) (model.RunOperationalEvent, error) {
	id, err := model.NewID()
	if err != nil {
		return model.RunOperationalEvent{}, err
	}
	event := model.RunOperationalEvent{
		SchemaVersion:     model.OperationalEventSchemaVersion,
		ID:                id,
		EventType:         eventType,
		RunID:             run.ID,
		TaskID:            run.TaskID,
		ProjectID:         run.ProjectID,
		CompactionEventID: compactionID,
		OccurredAt:        time.Now().UTC(),
		ExitCode:          exitCode,
		ResultingState:    resultingState,
	}
	if tail != "" {
		event.TailDigest = digestText(tail)
	}
	if message != "" {
		event.MessageDigest = digestText(message)
	}
	return event, nil
}

func latestEvent(events []model.RunOperationalEvent, eventType, compactionID string) *model.RunOperationalEvent {
	var found *model.RunOperationalEvent
	for i := range events {
		if events[i].EventType != eventType || (compactionID != "" && events[i].CompactionEventID != compactionID) {
			continue
		}
		if found == nil || events[i].OccurredAt.After(found.OccurredAt) {
			copy := events[i]
			found = &copy
		}
	}
	return found
}

func isCompactionLine(line string) (started, completed bool) {
	lower := strings.ToLower(strings.TrimSpace(line))
	if !strings.Contains(lower, "compact") && !strings.Contains(lower, "context window") {
		return false, false
	}
	if strings.Contains(lower, "low context") || strings.Contains(lower, "% left") || strings.Contains(lower, "context remaining") {
		return false, false
	}
	started = strings.Contains(lower, "compacting") || strings.Contains(lower, "compaction started") || strings.Contains(lower, "context compaction started")
	completed = strings.Contains(lower, "compacted") || strings.Contains(lower, "compaction complete") || strings.Contains(lower, "compaction completed") || strings.Contains(lower, "context window compressed")
	return started, completed
}

func isCompactionAcknowledgement(line string) bool {
	lower := strings.ToLower(strings.TrimSpace(line))
	if lower == "" {
		return true
	}
	if _, completed := isCompactionLine(lower); completed {
		return true
	}
	for _, prefix := range []string{"ack", "acknowledged", "continuing", "resuming", "resume", "context restored", "context recovery"} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

func compactionEventID(runID, marker string) string {
	return digestText(runID + "\x00" + strings.ToLower(strings.TrimSpace(marker)))
}

func hasExplicitQuestion(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && questionRE.MatchString(line) {
			return true
		}
	}
	return false
}
