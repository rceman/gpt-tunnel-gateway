package service

import (
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s *Service) resolveSessionBootstrapAgent(projectID, requested string) (config.AgentBinding, error) {
	if model.ValidateProjectIdentifier(projectID) != nil || model.ValidateObjectIdentifier(requested) != nil {
		return config.AgentBinding{}, fmt.Errorf("session bootstrap token has an invalid Agent")
	}
	bindings := s.Config.ProjectAgentBindings[projectID]
	binding, ok := bindings[requested]
	if !ok || binding.Validate() != nil {
		return config.AgentBinding{}, fmt.Errorf("session bootstrap token has no valid Agent binding")
	}
	return binding, nil
}
