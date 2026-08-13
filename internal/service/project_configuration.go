package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
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
	var configuration model.ProjectConfiguration
	if err := s.Hub.ReadJSON(ctx, s.projectConfigurationPath(projectID), &configuration); err != nil {
		return model.ProjectConfiguration{}, err
	}
	if configuration.Workflow.GateCommands.IsZero() {
		configuration.Workflow.GateCommands = model.DefaultProjectGateCommands()
	}
	if configuration.Integration.TargetBranch == "" {
		configuration.Integration.TargetBranch = configuration.Workflow.IntegrationBranch
	}
	if err := model.ValidateProjectConfiguration(configuration); err != nil {
		return model.ProjectConfiguration{}, err
	}
	if configuration.ProjectID != projectID {
		return model.ProjectConfiguration{}, fmt.Errorf("project configuration project_id mismatch")
	}
	return configuration, nil
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
	if err := model.ValidateProjectIdentifier(in.ProjectID); err != nil {
		return model.ProjectConfiguration{}, OperationResult{}, err
	}
	if in.ExpectedRevision < 1 {
		return model.ProjectConfiguration{}, OperationResult{}, fmt.Errorf("expected_revision is required")
	}
	if in.UpdatedBy == "" || strings.ContainsAny(in.UpdatedBy, "\x00\r\n") {
		return model.ProjectConfiguration{}, OperationResult{}, fmt.Errorf("updated_by is required")
	}
	if in.Patch.AgentRouting == nil && in.Patch.Watcher == nil && in.Patch.Workflow == nil && in.Patch.GateCommands == nil && in.Patch.Integration == nil && in.Patch.ActivationProfileRef == nil {
		return model.ProjectConfiguration{}, OperationResult{}, fmt.Errorf("project configuration patch is empty")
	}
	if _, err := s.ProjectRead(ctx, in.ProjectID); err != nil {
		return model.ProjectConfiguration{}, OperationResult{}, err
	}
	current, err := s.ProjectConfigurationRead(ctx, in.ProjectID)
	if err != nil {
		return model.ProjectConfiguration{}, OperationResult{}, err
	}
	if current.Revision != in.ExpectedRevision {
		return model.ProjectConfiguration{}, OperationResult{}, fmt.Errorf("project configuration revision conflict: expected %d, current %d", in.ExpectedRevision, current.Revision)
	}

	updated := current
	applyProjectConfigurationPatch(&updated, in.Patch)
	updated.Revision = current.Revision + 1
	updated.UpdatedBy = in.UpdatedBy
	updated.UpdatedAt = time.Now().UTC()
	if err := model.ValidateProjectConfiguration(updated); err != nil {
		return model.ProjectConfiguration{}, OperationResult{}, err
	}
	active, err := s.projectHasActiveTrainAttempt(ctx, in.ProjectID)
	if err != nil {
		return model.ProjectConfiguration{}, OperationResult{}, fmt.Errorf("inspect active Train Attempt: %w", err)
	}
	if active && (in.Patch.Workflow != nil || in.Patch.Integration != nil || in.Patch.ActivationProfileRef != nil) {
		return model.ProjectConfiguration{}, OperationResult{}, fmt.Errorf("execution-sensitive project configuration cannot change while an active Train Attempt exists")
	}
	path := s.projectConfigurationPath(in.ProjectID)
	tx, err := s.Hub.Transact(ctx, in.ExpectedHubRevision, "gateway: update project configuration "+in.ProjectID, func(worktree string) ([]string, error) {
		var latest model.ProjectConfiguration
		if err := readWorktreeJSON(worktree, path, &latest); err != nil {
			return nil, fmt.Errorf("read project configuration: %w", err)
		}
		if latest.Workflow.GateCommands.IsZero() {
			latest.Workflow.GateCommands = model.DefaultProjectGateCommands()
		}
		if latest.Integration.TargetBranch == "" {
			latest.Integration.TargetBranch = latest.Workflow.IntegrationBranch
		}
		if err := model.ValidateProjectConfiguration(latest); err != nil {
			return nil, fmt.Errorf("current project configuration is invalid: %w", err)
		}
		if latest.Revision != in.ExpectedRevision {
			return nil, fmt.Errorf("project configuration revision conflict: expected %d, current %d", in.ExpectedRevision, latest.Revision)
		}
		active, err := activeTrainAttemptInWorktree(worktree, in.ProjectID)
		if err != nil {
			return nil, fmt.Errorf("inspect active Train Attempt: %w", err)
		}
		if active && (in.Patch.Workflow != nil || in.Patch.Integration != nil || in.Patch.ActivationProfileRef != nil) {
			return nil, fmt.Errorf("execution-sensitive project configuration cannot change while an active Train Attempt exists")
		}
		candidate := latest
		applyProjectConfigurationPatch(&candidate, in.Patch)
		candidate.Revision = latest.Revision + 1
		candidate.UpdatedBy = in.UpdatedBy
		candidate.UpdatedAt = updated.UpdatedAt
		if err := model.ValidateProjectConfiguration(candidate); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, path, candidate); err != nil {
			return nil, err
		}
		updated = candidate
		return []string{path}, nil
	})
	if err != nil {
		return model.ProjectConfiguration{}, OperationResult{}, err
	}
	return updated, OperationResult{
		Hub:       tx,
		ProjectID: in.ProjectID,
		Status:    "updated",
	}, nil
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
	if patch.Integration != nil {
		configuration.Integration = *patch.Integration
	}
	if patch.ActivationProfileRef != nil {
		configuration.ActivationProfileRef = *patch.ActivationProfileRef
	}
}
