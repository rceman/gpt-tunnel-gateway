package service

import (
	"context"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

// WatcherAgentBinding is the resolved host-local binding for the portable
// watcher agent identity. The project never stores the provider session key.
type WatcherAgentBinding struct {
	AgentID    string
	SessionKey string
	Profile    string
}

func (s *Service) resolveWatcherBinding(projectID string) (WatcherAgentBinding, error) {
	local, err := s.projectConfig(projectID)
	if err != nil {
		return WatcherAgentBinding{}, err
	}
	settings := local.Watcher.Effective()
	if settings.AgentID == "" {
		return WatcherAgentBinding{}, fmt.Errorf("watcher agent is not configured for project %q", projectID)
	}
	resolved, err := s.ResolveAgent(context.Background(), AgentResolveInput{
		ProjectID:     projectID,
		Role:          model.AgentRoleWatcher,
		AgentID:       settings.AgentID,
		RequireUsable: false,
	})
	if err != nil {
		return WatcherAgentBinding{}, err
	}
	return WatcherAgentBinding{
		AgentID:    resolved.AgentID,
		SessionKey: resolved.SessionKey,
		Profile:    resolved.Profile,
	}, nil
}
