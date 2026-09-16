package service

import (
	"fmt"
	"os"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
)

func (s *Service) EnableHostConfigRefresh() {
	s.hostConfigRefreshMu.Lock()
	s.hostConfigRefreshEnabled = true
	s.hostConfigRefreshMu.Unlock()
}

func (s *Service) refreshHostConfig() error {
	s.hostConfigRefreshMu.Lock()
	defer s.hostConfigRefreshMu.Unlock()
	if !s.hostConfigRefreshEnabled {
		return nil
	}
	path := s.ConfigPath
	if path == "" {
		path = config.DefaultPath()
	}
	loaded, err := config.Load(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("host configuration is unavailable: %w", err)
		}
		return err
	}
	s.Config.ProjectAgentBindings = cloneAgentBindings(loaded.ProjectAgentBindings)
	return nil
}

func (s *Service) agentBinding(projectID, agentID string) (config.AgentBinding, bool) {
	if err := s.refreshHostConfig(); err != nil {
		return config.AgentBinding{}, false
	}
	s.hostConfigRefreshMu.Lock()
	defer s.hostConfigRefreshMu.Unlock()
	return s.Config.ResolveAgentBinding(projectID, agentID)
}

func (s *Service) hasProjectAgentBinding(projectID string) bool {
	if err := s.refreshHostConfig(); err != nil {
		return false
	}
	s.hostConfigRefreshMu.Lock()
	defer s.hostConfigRefreshMu.Unlock()
	return len(s.Config.ProjectAgentBindings[projectID]) > 0
}

func cloneAgentBindings(input map[string]map[string]config.AgentBinding) map[string]map[string]config.AgentBinding {
	if input == nil {
		return nil
	}
	output := make(map[string]map[string]config.AgentBinding, len(input))
	for projectID, bindings := range input {
		output[projectID] = make(map[string]config.AgentBinding, len(bindings))
		for agentID, binding := range bindings {
			output[projectID][agentID] = binding
		}
	}
	return output
}
