package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
)

const (
	agentDefaultTailLines = 30
	agentMaxTailLines     = 200
)

type AgentTailResult struct {
	Lines            []string `json:"lines"`
	Count            int      `json:"count"`
	HasNewInfo       bool     `json:"has_new_info"`
	Overflow         bool     `json:"overflow"`
	HistoryTruncated bool     `json:"history_truncated"`
}

type AgentTailInput struct {
	Lines     int
	SessionID string
}

func agentSnapshotLines(text string) []string {
	text = strings.TrimRight(text, "\r\n")
	if text == "" {
		return []string{}
	}
	return strings.Split(text, "\n")
}

type agentTailSeen struct {
	SessionID  string   `json:"session_id"`
	ProjectID  string   `json:"project_id"`
	SessionKey string   `json:"session_key"`
	LastLines  []string `json:"last_lines,omitempty"`
	HasLast    bool     `json:"has_last"`
}

type agentSessionContextKey struct{}

func WithAgentSessionID(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, agentSessionContextKey{}, sessionID)
}

func AgentSessionID(ctx context.Context) string {
	value, _ := ctx.Value(agentSessionContextKey{}).(string)
	return value
}

func (s *Service) AgentTail(ctx context.Context, projectID string, lines int) (AgentTailResult, error) {
	return s.AgentTailPage(ctx, projectID, AgentTailInput{
		Lines: lines,
	})
}

func (s *Service) AgentTailPage(ctx context.Context, projectID string, input AgentTailInput) (AgentTailResult, error) {
	lines := input.Lines
	if lines == 0 {
		lines = agentDefaultTailLines
	}
	if lines < 1 || lines > agentMaxTailLines {
		return AgentTailResult{}, fmt.Errorf("invalid agent tail bounds")
	}
	session, err := s.resolveAgentTailSession(projectID)
	if err != nil {
		return AgentTailResult{}, err
	}
	tail, err := s.Airelay.TailSnapshot(ctx, session, lines)
	if err != nil {
		return AgentTailResult{}, err
	}
	statePath, lockName := s.agentTailStateLocation(input.SessionID, projectID, session)
	lock, err := lockfile.Acquire(filepath.Join(s.Config.StateDir, "locks"), lockName)
	if err != nil {
		return AgentTailResult{}, fmt.Errorf("agent tail observation is busy")
	}
	defer func() { _ = lock.Release() }()
	seen := agentTailSeen{}
	seen, err = readAgentTailSeen(statePath)
	if err != nil {
		return AgentTailResult{}, err
	}
	if seen.HasLast && (seen.SessionID != input.SessionID || seen.ProjectID != projectID || seen.SessionKey != session) {
		return AgentTailResult{}, fmt.Errorf("agent tail observation identity mismatch")
	}
	snapshot := agentSnapshotLines(tail.Stdout)
	selected, hasNew, historyTruncated := agentTailDelta(seen.LastLines, snapshot, seen.HasLast)
	if len(snapshot) > 0 || seen.HasLast {
		seen.SessionID = input.SessionID
		seen.ProjectID = projectID
		seen.SessionKey = session
		seen.LastLines = append([]string(nil), snapshot...)
		seen.HasLast = true
		if err := writeAgentTailSeen(statePath, seen); err != nil {
			return AgentTailResult{}, err
		}
	}
	linesOut := make([]string, 0, len(selected))
	for _, line := range selected {
		linesOut = append(linesOut, line)
	}
	return AgentTailResult{
		Lines:            linesOut,
		Count:            len(linesOut),
		HasNewInfo:       hasNew,
		Overflow:         false,
		HistoryTruncated: historyTruncated,
	}, nil
}

func agentTailDelta(previous, current []string, hadPrevious bool) ([]string, bool, bool) {
	if !hadPrevious {
		return current, len(current) > 0, false
	}
	maxOverlap := len(previous)
	if len(current) < maxOverlap {
		maxOverlap = len(current)
	}
	for overlap := maxOverlap; overlap > 0; overlap-- {
		match := true
		for i := 0; i < overlap; i++ {
			if previous[len(previous)-overlap+i] != current[i] {
				match = false
				break
			}
		}
		if match {
			selected := current[overlap:]
			return selected, len(selected) > 0, false
		}
	}
	return current, len(current) > 0, len(current) > 0
}

func (s *Service) agentTailStateLocation(sessionID, projectID, sessionKey string) (string, string) {
	identity := sessionID + "\x00" + projectID + "\x00" + sessionKey
	digest := sha256.Sum256([]byte(identity))
	name := hex.EncodeToString(digest[:12])
	return filepath.Join(s.Config.StateDir, "agent-tail", name+".json"), "agent-tail-" + name
}

func readAgentTailSeen(path string) (agentTailSeen, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return agentTailSeen{}, nil
	}
	if err != nil {
		return agentTailSeen{}, fmt.Errorf("read agent tail observation: %w", err)
	}
	var state agentTailSeen
	if err := json.Unmarshal(data, &state); err != nil || state.ProjectID == "" || state.SessionKey == "" {
		return agentTailSeen{}, fmt.Errorf("invalid agent tail observation")
	}
	return state, nil
}

func writeAgentTailSeen(path string, state agentTailSeen) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".agent-tail-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}
