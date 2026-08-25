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
	if s.Durability == nil {
		return model.ProjectConfiguration{}, fmt.Errorf("Shared project configuration authority is unavailable")
	}
	return s.projectConfigurationReadShared(ctx, projectID)

}

func (s *Service) trainV2Enabled(ctx context.Context, projectID string) (bool, error) {
	configuration, err := s.ProjectConfigurationRead(ctx, projectID)
	if err != nil {
		if IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return configuration.ExecutionModel == "train_v2", nil
}

func (s *Service) TrainV2Enabled(ctx context.Context, projectID string) (bool, error) {
	return s.trainV2Enabled(ctx, projectID)
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

func (s *Service) ProjectConfigurationUpdate(ctx context.Context, in ProjectConfigurationUpdateInput) (model.ProjectConfiguration, OperationResult, error) {
	if err := RequireWorkflowPolicyAuthority(ctx); err != nil {
		return model.ProjectConfiguration{}, OperationResult{}, err
	}
	if s.Durability == nil {
		return model.ProjectConfiguration{}, OperationResult{}, fmt.Errorf("Shared project configuration authority is unavailable")
	}
	return s.projectConfigurationUpdateShared(ctx, in)

}

func applyProjectConfigurationPatch(configuration *model.ProjectConfiguration, patch ProjectConfigurationPatch) {
	if patch.AgentRouting != nil {
		configuration.AgentRouting = *patch.AgentRouting
	}
	if patch.Watcher != nil {
		configuration.Watcher = *patch.Watcher
	}
	if patch.Workflow != nil {
		configuration.Workflow = *patch.Workflow
	}
	if patch.GateCommands != nil {
		configuration.Workflow.GateCommands = *patch.GateCommands
	}
	if patch.Checkpoint != nil {
		configuration.Checkpoint = *patch.Checkpoint
	}
	if patch.Integration != nil {
		configuration.Integration = *patch.Integration
	}
	if patch.ActivationProfileRef != nil {
		configuration.ActivationProfileRef = *patch.ActivationProfileRef
	}
}
