package service

import (
	"context"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s *Service) projectConfigurationPath(projectID string) string {
	if model.ValidateProjectIdentifier(projectID) != nil {
		return "../invalid-project-configuration"
	}
	return s.projectPrefix(projectID) + "/configuration/current.json"
}

func (s *Service) ProjectConfigurationRead(ctx context.Context, projectID string) (model.ProjectConfiguration, error) {
	if err := model.ValidateProjectIdentifier(projectID); err != nil {
		return model.ProjectConfiguration{}, err
	}
	if s.Durability != nil {
		return s.projectConfigurationReadShared(ctx, projectID)
	}
	data, err := s.Hub.ReadFile(ctx, s.projectConfigurationPath(projectID))
	if err != nil {
		return model.ProjectConfiguration{}, err
	}
	var configuration model.ProjectConfiguration
	if err := decodeStrict(data, &configuration); err != nil {
		return model.ProjectConfiguration{}, err
	}
	if err := model.ValidateProjectConfiguration(configuration); err != nil {
		return model.ProjectConfiguration{}, err
	}
	if configuration.ProjectID != projectID {
		return model.ProjectConfiguration{}, fmt.Errorf("project configuration project_id mismatch")
	}
	return configuration, nil
}

func (s *Service) projectConfigurationStatus(ctx context.Context, projectID string) ProjectConfigurationStatus {
	configuration, err := s.ProjectConfigurationRead(ctx, projectID)
	if err == nil {
		return ProjectConfigurationStatus{
			State:         "valid",
			Revision:      configuration.Revision,
			Configuration: &configuration,
			Conflicts:     []string{},
		}
	}
	state := "invalid"
	if IsNotFound(err) {
		state = "missing"
	}
	return ProjectConfigurationStatus{
		State:     state,
		Conflicts: []string{err.Error()},
	}
}
