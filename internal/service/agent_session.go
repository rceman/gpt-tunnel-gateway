package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

// AgentPromptResult is the durable PMT acceptance result. delivered remains
// false until the coding Agent reads the PMT through agent/prompt_read.
type AgentPromptResult struct {
	ProjectID string          `json:"project_id"`
	PMTID     string          `json:"pmt_id,omitempty"`
	Queued    bool            `json:"queued,omitempty"`
	Delivered bool            `json:"delivered"`
	Queue     *model.PMTQueue `json:"queue,omitempty"`
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

// AgentPrompt creates a durable local PMT before sending its bounded reference
// to Airelay. The full instruction never crosses the Airelay transport.
func (s *Service) AgentPrompt(ctx context.Context, projectID, message string) (AgentPromptResult, error) {
	return s.createAndSendPMT(ctx, projectID, "", message, nil)
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
