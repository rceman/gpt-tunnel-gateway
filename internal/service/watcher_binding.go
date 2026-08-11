package service

import (
	"fmt"
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
	binding, ok := s.Config.AgentBindings[settings.AgentID]
	if !ok {
		return WatcherAgentBinding{}, fmt.Errorf("watcher agent %q has no host-local binding", settings.AgentID)
	}
	if err := binding.Validate(); err != nil {
		return WatcherAgentBinding{}, fmt.Errorf("watcher agent %q binding is invalid: %w", settings.AgentID, err)
	}
	return WatcherAgentBinding{
		AgentID:    settings.AgentID,
		SessionKey: binding.SessionKey,
		Profile:    binding.Profile,
	}, nil
}
