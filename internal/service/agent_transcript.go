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
	agentTranscriptRead   = 200
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
	SessionID  string `json:"session_id"`
	ProjectID  string `json:"project_id"`
	SessionKey string `json:"session_key"`
	Timestamp  int64  `json:"timestamp"`
	LineIndex  int    `json:"line_index"`
	HasLast    bool   `json:"has_last"`
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
	session, err := s.resolveAgentSession(ctx, projectID)
	if err != nil {
		return AgentTailResult{}, err
	}
	transcript, err := s.Airelay.Transcript(ctx, session, agentTranscriptRead)
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
	start := 0
	if seen.HasLast {
		start = len(transcript.Lines)
		found := false
		for index, line := range transcript.Lines {
			if line.Timestamp == seen.Timestamp && index == seen.LineIndex {
				start = index + 1
				found = true
				break
			}
		}
		if !found && len(transcript.Lines) > 0 {
			start = 0
		}
	}
	unseen := transcript.Lines[start:]
	hasNew := len(unseen) > 0
	selected := transcript.Lines
	overflow := false
	selected = unseen
	if len(selected) > lines {
		overflow = true
		selected = selected[len(selected)-lines:]
	}
	if len(transcript.Lines) > 0 {
		last := transcript.Lines[len(transcript.Lines)-1]
		seen.SessionID = input.SessionID
		seen.ProjectID = projectID
		seen.SessionKey = session
		seen.Timestamp = last.Timestamp
		seen.LineIndex = len(transcript.Lines) - 1
		seen.HasLast = true
		if err := writeAgentTailSeen(statePath, seen); err != nil {
			return AgentTailResult{}, err
		}
	}
	linesOut := make([]string, 0, len(selected))
	for _, line := range selected {
		linesOut = append(linesOut, line.Text)
	}
	return AgentTailResult{
		Lines:            linesOut,
		Count:            len(linesOut),
		HasNewInfo:       hasNew,
		Overflow:         overflow,
		HistoryTruncated: len(transcript.Lines) >= agentTranscriptRead,
	}, nil
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
