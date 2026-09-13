package service

import (
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

// resolveLocalAgentBinding resolves host-local identity without changing the
// portable Agent record. Explicit project/agent configuration always wins.
func (s *Service) resolveLocalAgentBinding(projectID string, agent model.Agent, agents []model.Agent) (config.AgentBinding, bool) {
	binding, ok := s.Config.ResolveAgentBinding(projectID, agent.AgentID)
	return binding, ok
}

// resolveExplicitLocalAgentBinding resolves one known Agent without
// enumerating the registry. Explicit selectors must not depend on collection
// limits or on unrelated Agent records.
func (s *Service) resolveExplicitLocalAgentBinding(projectID string, agent model.Agent) (config.AgentBinding, bool) {
	binding, ok := s.Config.ResolveAgentBinding(projectID, agent.AgentID)
	return binding, ok
}
