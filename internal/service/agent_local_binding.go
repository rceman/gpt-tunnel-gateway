package service

import (
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

// resolveLocalAgentBinding resolves host-local identity without changing the
// portable Agent record. Explicit project/agent configuration always wins.
// The project session is an auto-bind fallback only for one enabled coding
// agent; a watcher or an ambiguous coding-agent set stays unbound.
func (s *Service) resolveLocalAgentBinding(projectID string, agent model.Agent, agents []model.Agent) (config.AgentBinding, bool) {
	if binding, ok := s.Config.ResolveAgentBinding(projectID, agent.AgentID); ok {
		return binding, true
	}
	if agent.Role != model.AgentRoleCoding {
		return config.AgentBinding{}, false
	}
	enabledCoding := 0
	for _, candidate := range agents {
		if candidate.Role == model.AgentRoleCoding && candidate.Enabled {
			enabledCoding++
		}
	}
	if enabledCoding != 1 {
		return config.AgentBinding{}, false
	}
	return s.Config.ResolveAutoAgentBinding(projectID)
}
