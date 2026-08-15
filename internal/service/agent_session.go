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
	"github.com/rceman/gpt-tunnel-gateway/internal/runtime_log"
)

const ()

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

// AgentPromptResult is the compact public result for the generic agent/prompt
// action. The verbose AgentSendResult remains an internal receipt for typed
// callers that need execution details.
type AgentPromptResult struct {
	ProjectID string `json:"project_id"`
	Delivered bool   `json:"delivered"`
	ExitCode  int    `json:"exit_code,omitempty"`
	Stderr    string `json:"stderr,omitempty"`
	Error     string `json:"error,omitempty"`
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

// resolveAgentTailSession is intentionally local-only. A bounded tail read
// observes the server-configured project session; it must not perform the
// durable AgentList/ResolveAgent routing sequence used by prompt, status, and
// dispatch paths. Those paths still retain their authoritative Hub checks.
func (s *Service) resolveAgentTailSession(projectID string) (string, error) {
	local, err := s.projectConfig(projectID)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(local.AirelaySessionKey) == "" {
		return "", fmt.Errorf("project %q has no configured Airelay session", projectID)
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
	result, sendErr := s.Airelay.PromptWithProvenance(ctx, session, AgentSessionID(ctx), message)
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
func (s *Service) AgentPrompt(ctx context.Context, projectID, message string) (AgentPromptResult, error) {
	receipt, sendErr := s.AgentSend(ctx, projectID, message)
	if receipt.Delivered && receipt.Stderr != "" {
		s.recordAgentPromptWarning(ctx, projectID, receipt.Stderr)
	}
	return compactAgentPromptResult(projectID, receipt, sendErr), nil
}

func compactAgentPromptResult(projectID string, receipt AgentSendResult, sendErr error) AgentPromptResult {
	result := AgentPromptResult{
		ProjectID: projectID,
		Delivered: receipt.Delivered,
	}
	if receipt.ExitCode != 0 {
		result.ExitCode = receipt.ExitCode
	}
	if !receipt.Delivered && receipt.Stderr != "" {
		result.Stderr = boundedPromptDiagnostic(receipt.Stderr)
	}
	if sendErr != nil {
		result.Error = boundedPromptDiagnostic(sendErr.Error())
	} else if receipt.Error != "" {
		result.Error = boundedPromptDiagnostic(receipt.Error)
	}
	return result
}

func boundedPromptDiagnostic(value string) string {
	value = runtime_log.SanitizeText(value)
	return strings.TrimSpace(value)
}

func (s *Service) recordAgentPromptWarning(ctx context.Context, projectID, warning string) {
	if s.Config.StateDir == "" {
		return
	}
	_ = runtime_log.New(s.Config.StateDir).Append(runtime_log.Event{
		Timestamp:   time.Now().UTC(),
		Level:       "warn",
		Component:   "agent",
		Event:       "agent_prompt_warning",
		Action:      "agent/prompt",
		RequestID:   runtime_log.RequestID(ctx),
		OperationID: runtime_log.OperationID(ctx),
		ProjectID:   projectID,
		Message:     "Airelay prompt completed with warning",
		Error:       runtime_log.SanitizeText(warning),
	})
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
