package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
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
	ProjectID string `json:"project_id"`
	Text      string `json:"text"`
	Lines     int    `json:"lines"`
	Skip      int    `json:"skip"`
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
	return local.AirelaySessionKey, nil
}

func (s *Service) AgentSend(ctx context.Context, projectID, message string) (AgentSendResult, error) {
	session, err := s.resolveAgentSession(ctx, projectID)
	if err != nil {
		return AgentSendResult{}, err
	}
	lock, err := lockfile.Acquire(filepath.Join(s.Config.StateDir, "agent-sessions"), sessionLockName(session))
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

func (s *Service) AgentTail(ctx context.Context, projectID string, lines, skip int) (AgentTailResult, error) {
	if lines == 0 {
		lines = agentDefaultTailLines
	}
	if lines < 1 || lines > agentMaxTailLines || skip < 0 || lines+skip > agentMaxTailLines {
		return AgentTailResult{}, fmt.Errorf("invalid agent tail bounds")
	}
	session, err := s.resolveAgentSession(ctx, projectID)
	if err != nil {
		return AgentTailResult{}, err
	}
	result, err := s.Airelay.TailWithSkip(ctx, session, lines, skip)
	if err != nil {
		return AgentTailResult{}, err
	}
	return AgentTailResult{ProjectID: projectID, Text: result.Stdout, Lines: lines, Skip: skip}, nil
}

func (s *Service) AgentStatus(ctx context.Context, projectID string) (AgentStatusResult, error) {
	session, err := s.resolveAgentSession(ctx, projectID)
	if err != nil {
		return AgentStatusResult{}, err
	}
	status, err := s.Airelay.Status(ctx, session)
	if err != nil {
		return AgentStatusResult{}, err
	}
	return AgentStatusResult{
		ProjectID:           projectID,
		State:               status.State,
		ControllerReachable: status.ControllerReachable,
		AirelayVersion:      status.AirelayVersion,
		ProtocolVersion:     status.ProtocolVersion,
		CapacityWarnings:    append([]string{}, status.CapacityWarnings...),
		ExitCode:            status.ExitCode,
		Error:               status.Error,
	}, nil
}

func sessionLockName(session string) string {
	digest := sha256.Sum256([]byte(session))
	return "send-" + hex.EncodeToString(digest[:8])
}
