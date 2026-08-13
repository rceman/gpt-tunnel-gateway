package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/tailcursor"
)

const (
	agentDefaultTailLines = 4
	agentMaxTailLines     = 200
)

type AgentSendResult struct {
	ProjectID  string    `json:"project_id"`
	Delivered  bool      `json:"delivered"`
	ExitCode   int       `json:"exit_code"`
	Stdout     string    `json:"stdout"`
	Stderr     string    `json:"stderr"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
	Error      string    `json:"error,omitempty"`
}

type AgentTailResult struct {
	ProjectID  string `json:"project_id"`
	Text       string `json:"text"`
	Lines      int    `json:"lines"`
	Skip       int    `json:"skip"`
	NextCursor string `json:"next_cursor"`
	HasMore    bool   `json:"has_more"`
}

type AgentTailInput struct {
	Lines  int
	Skip   int
	Cursor string
}

type AgentStatusResult struct {
	ProjectID           string   `json:"project_id"`
	State               string   `json:"state"`
	ControllerReachable bool     `json:"controller_reachable"`
	AirelayVersion      string   `json:"airelay_version,omitempty"`
	ProtocolVersion     string   `json:"protocol_version,omitempty"`
	CapacityWarnings    []string `json:"capacity_warnings"`
	ExitCode            int      `json:"exit_code"`
	Error               string   `json:"error,omitempty"`
}

func (s *Service) resolveAgentSession(ctx context.Context, projectID string) (string, error) {
	local, err := s.projectConfig(projectID)
	if err != nil {
		return "", err
	}
	project, err := s.ProjectRead(ctx, projectID)
	if err != nil {
		return "", fmt.Errorf("registered project %q is unavailable", projectID)
	}
	if project.ID != projectID || project.Status != "active" {
		return "", fmt.Errorf("project %q is not active", projectID)
	}
	if agents, listErr := s.AgentList(ctx, projectID); listErr == nil && len(agents) > 0 {
		resolved, resolveErr := s.ResolveAgent(ctx, AgentResolveInput{
			ProjectID:     projectID,
			Role:          model.AgentRoleCoding,
			RequireUsable: false,
		})
		if resolveErr != nil {
			return "", resolveErr
		}
		return resolved.SessionKey, nil
	}
	return local.AirelaySessionKey, nil
}

func (s *Service) AgentSend(ctx context.Context, projectID, message string) (AgentSendResult, error) {
	session, err := s.resolveAgentSession(ctx, projectID)
	if err != nil {
		return AgentSendResult{}, err
	}
	lock, err := s.acquireSessionSendLock(session)
	if err != nil {
		return AgentSendResult{}, fmt.Errorf("agent session send is already in progress")
	}
	defer func() { _ = lock.Release() }()
	result, sendErr := s.Airelay.Prompt(ctx, session, message)
	receipt := AgentSendResult{
		ProjectID:  projectID,
		Delivered:  sendErr == nil,
		ExitCode:   result.ExitCode,
		Stdout:     result.Stdout,
		Stderr:     result.Stderr,
		StartedAt:  result.StartedAt,
		FinishedAt: result.FinishedAt,
	}
	if sendErr != nil {
		if result.StartedAt.IsZero() {
			return AgentSendResult{}, sendErr
		}
		receipt.Error = sendErr.Error()
	}
	return receipt, nil
}

// AgentPrompt is the canonical non-interrupting steering operation. It uses
// the normal Airelay prompt primitive for both idle and working sessions; no
// interrupt, PTY input, or process-control operation is reachable here.
func (s *Service) AgentPrompt(ctx context.Context, projectID, message string) (AgentSendResult, error) {
	return s.AgentSend(ctx, projectID, message)
}

func (s *Service) AgentTail(ctx context.Context, projectID string, lines, skip int) (AgentTailResult, error) {
	return s.AgentTailPage(ctx, projectID, AgentTailInput{
		Lines: lines,
		Skip:  skip,
	})
}

func (s *Service) AgentTailPage(ctx context.Context, projectID string, input AgentTailInput) (AgentTailResult, error) {
	lines, skip := input.Lines, input.Skip
	if lines == 0 {
		lines = agentDefaultTailLines
	}
	if lines < 1 || lines > agentMaxTailLines || skip < 0 || lines+skip > agentMaxTailLines || (input.Cursor != "" && skip != 0) {
		return AgentTailResult{}, fmt.Errorf("invalid agent tail bounds")
	}
	session, err := s.resolveAgentSession(ctx, projectID)
	if err != nil {
		return AgentTailResult{}, err
	}
	result, err := s.Airelay.TailSnapshot(ctx, session, tailcursor.MaxSnapshotLines)
	if err != nil {
		return AgentTailResult{}, err
	}
	snapshot := agentSnapshotLines(result.Stdout)
	var page tailcursor.Page
	if input.Cursor == "" {
		page, err = tailcursor.Initial("project:"+projectID, session, snapshot, lines, skip)
	} else {
		page, err = tailcursor.Continue("project:"+projectID, session, input.Cursor, snapshot, lines)
	}
	if err != nil {
		return AgentTailResult{}, err
	}
	return AgentTailResult{
		ProjectID:  projectID,
		Text:       page.Text,
		Lines:      lines,
		Skip:       skip,
		NextCursor: page.NextCursor,
		HasMore:    page.HasMore,
	}, nil
}

func agentSnapshotLines(text string) []string {
	text = strings.TrimRight(text, "\r\n")
	if text == "" {
		return []string{}
	}
	return strings.Split(text, "\n")
}

func (s *Service) AgentStatus(ctx context.Context, projectID string) (AgentStatusResult, error) {
	progress, err := s.projectProgress(ctx, projectID)
	if err != nil {
		return AgentStatusResult{}, err
	}
	return AgentStatusResult{
		ProjectID:           projectID,
		State:               progress.AgentState,
		ControllerReachable: progress.ControllerReachable,
		AirelayVersion:      progress.AirelayVersion,
		ProtocolVersion:     progress.ProtocolVersion,
		CapacityWarnings:    append([]string{}, progress.CapacityWarnings...),
		ExitCode:            progress.ExitCode,
		Error:               progress.Error,
	}, nil
}

func sessionLockName(session string) string {
	digest := sha256.Sum256([]byte(session))
	return "session-send-" + hex.EncodeToString(digest[:8])
}

func (s *Service) acquireSessionSendLock(session string) (*lockfile.Lock, error) {
	return lockfile.Acquire(filepath.Join(s.Config.StateDir, "locks"), sessionLockName(session))
}
