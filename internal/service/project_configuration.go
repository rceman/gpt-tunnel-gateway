package service

import (
	"context"
	"errors"
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
	return s.projectConfigurationRead(ctx, projectID, false)
}

func (s *Service) projectConfigurationRead(ctx context.Context, projectID string, failClosedOnHubError bool) (model.ProjectConfiguration, error) {
	if err := model.ValidateProjectIdentifier(projectID); err != nil {
		return model.ProjectConfiguration{}, err
	}
	retired, err := s.isProjectRetired(ctx, projectID)
	if err != nil {
		return model.ProjectConfiguration{}, err
	}
	if retired {
		return model.ProjectConfiguration{}, fmt.Errorf("project %q is retired", projectID)
	}
	if s.Durability != nil {
		configuration, sharedErr := s.projectConfigurationReadShared(ctx, projectID)
		if sharedErr == nil {
			_, hubRetired, hubErr := s.readHubProjectRetirementBounded(ctx, projectID)
			if hubErr == nil && hubRetired {
				return model.ProjectConfiguration{}, fmt.Errorf("project %q is retired", projectID)
			}
			if hubErr != nil && (failClosedOnHubError || errors.Is(hubErr, ErrInvalidHubProjectRetirement) || ctx.Err() != nil) {
				if ctx.Err() != nil {
					return model.ProjectConfiguration{}, ctx.Err()
				}
				return model.ProjectConfiguration{}, hubErr
			}
			return configuration, nil
		}
		if !IsNotFound(sharedErr) {
			return model.ProjectConfiguration{}, sharedErr
		}
		_, hubRetired, hubErr := s.readHubProjectRetirementBounded(ctx, projectID)
		if hubErr != nil {
			return model.ProjectConfiguration{}, hubErr
		}
		if hubRetired {
			return model.ProjectConfiguration{}, fmt.Errorf("project %q is retired", projectID)
		}
		return model.ProjectConfiguration{}, sharedErr
	}
	_, retired, err = s.readHubProjectRetirementBounded(ctx, projectID)
	if err != nil {
		return model.ProjectConfiguration{}, err
	}
	if retired {
		return model.ProjectConfiguration{}, fmt.Errorf("project %q is retired", projectID)
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
