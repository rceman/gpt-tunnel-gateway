package service

import (
	"github.com/rceman/gpt-tunnel-gateway/internal/entity"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s *Service) entityRegistry(projectID string) entity.Registry {
	return entity.Registry{Source: s.Hub, ProjectID: projectID, MaxItems: s.Config.MaxListItems}
}

func validateEntityProject(projectID string) error {
	return model.ValidateProjectIdentifier(projectID)
}
