package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
	return ProjectConfigurationStatus{State: state, Conflicts: []string{err.Error()}}
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
	if in.Patch.AgentRouting == nil && in.Patch.Watcher == nil && in.Patch.Workflow == nil && in.Patch.ActivationProfileRef == nil && in.Patch.ExecutionModel == nil {
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
	if (in.Patch.Workflow != nil || in.Patch.ActivationProfileRef != nil || in.Patch.ExecutionModel != nil) && s.projectHasActiveRun(ctx, in.ProjectID) {
		return model.ProjectConfiguration{}, OperationResult{}, fmt.Errorf("execution-sensitive project configuration cannot change while an active run exists")
	}

	updated := current
	applyProjectConfigurationPatch(&updated, in.Patch)
	updated.Revision = current.Revision + 1
	updated.UpdatedBy = in.UpdatedBy
	updated.UpdatedAt = time.Now().UTC()
	if err := model.ValidateProjectConfiguration(updated); err != nil {
		return model.ProjectConfiguration{}, OperationResult{}, err
	}
	path := s.projectConfigurationPath(in.ProjectID)
	tx, err := s.Hub.Transact(ctx, in.ExpectedHubRevision, "gateway: update project configuration "+in.ProjectID, func(worktree string) ([]string, error) {
		var latest model.ProjectConfiguration
		if err := readWorktreeJSON(worktree, path, &latest); err != nil {
			return nil, fmt.Errorf("read project configuration: %w", err)
		}
		if err := model.ValidateProjectConfiguration(latest); err != nil {
			return nil, fmt.Errorf("current project configuration is invalid: %w", err)
		}
		if latest.Revision != in.ExpectedRevision {
			return nil, fmt.Errorf("project configuration revision conflict: expected %d, current %d", in.ExpectedRevision, latest.Revision)
		}
		if (in.Patch.Workflow != nil || in.Patch.ActivationProfileRef != nil || in.Patch.ExecutionModel != nil) && activeRunInWorktree(worktree, in.ProjectID) {
			return nil, fmt.Errorf("execution-sensitive project configuration cannot change while an active run exists")
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
	return updated, OperationResult{Hub: tx, ProjectID: in.ProjectID, Status: "updated"}, nil
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
	if patch.ActivationProfileRef != nil {
		configuration.ActivationProfileRef = *patch.ActivationProfileRef
	}
	if patch.ExecutionModel != nil {
		configuration.ExecutionModel = *patch.ExecutionModel
	}
}

func (s *Service) projectHasActiveRun(ctx context.Context, projectID string) bool {
	runs, err := s.RunList(ctx, projectID)
	if err != nil {
		return false
	}
	for _, run := range runs {
		if operationalActiveRun(run) {
			return true
		}
	}
	return false
}

func activeRunInWorktree(worktree, projectID string) bool {
	root := filepath.Join(worktree, filepath.FromSlash(hub.ProtocolRoot), "projects", projectID, "runs")
	entries, err := os.ReadDir(root)
	if err != nil {
		return false
	}
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			ids = append(ids, entry.Name())
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		var run model.Run
		if err := readWorktreeJSON(worktree, filepath.ToSlash(filepath.Join(hub.ProtocolRoot, "projects", projectID, "runs", id, "run.json")), &run); err != nil {
			continue
		}
		if operationalActiveRun(run) {
			return true
		}
	}
	return false
}
