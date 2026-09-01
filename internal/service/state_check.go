package service

import (
	"context"
	"fmt"
	"sort"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s *Service) StateCheck(ctx context.Context) (StateCheckResult, error) {
	result := StateCheckResult{
		ConfiguredProjectIDs:    []string{},
		DurableProjectIDs:       []string{},
		ValidCurrentPlans:       []string{},
		Plans:                   []StatePlan{},
		Issues:                  []StateIssue{},
		OperationalTaskRunGraph: false,
	}
	configuredIDs, resolution, err := s.effectiveProjectIDs()
	if err != nil {
		result.Issues = append(result.Issues, stateIssue("CONFIGURED_PROJECTS_INVALID", "", "", "", err.Error()))
		result.Valid = false
		result.OperationalTaskRunGraph = false
		return result, nil
	}
	result.ConfiguredProjectIDs = append(result.ConfiguredProjectIDs, configuredIDs...)
	if s.Durability != nil {
		return s.stateCheckLocal(ctx, configuredIDs)
	}
	return s.stateCheckLocalWithoutDurability(result, configuredIDs, resolution)
}

func (s *Service) stateCheckLocalWithoutDurability(result StateCheckResult, configuredIDs []string, resolution ProjectResolution) (StateCheckResult, error) {
	for _, projectID := range configuredIDs {
		project, ok := resolution.Projects[projectID]
		if !ok || project.Root == "" || project.Mirror == "" || project.DefaultBranch == "" || project.AirelaySessionKey == "" {
			result.Issues = append(result.Issues, stateIssue("CONFIGURED_PROJECT_INVALID", projectID, "", "", "local project configuration is incomplete"))
			continue
		}
		result.DurableProjectIDs = append(result.DurableProjectIDs, projectID)
	}
	sort.Strings(result.DurableProjectIDs)
	// Plan files remain immutable history and are intentionally not part of
	// current-state validation after the Train-v2 cutover. A controller-side
	// check has no SQLite owner, so live graph validation is performed by the
	// daemon's Local/Shared path above.
	result.Valid = len(result.Issues) == 0
	return result, nil
}

// stateCheckLocal validates the live durable graph from Local/Shared SQLite.
// Shared projections are replicated Hub state, but this read path never asks
// Hub to refresh, acquire its repository lock, or resolve a remote revision.
func (s *Service) stateCheckLocal(ctx context.Context, configuredIDs []string) (StateCheckResult, error) {
	result := StateCheckResult{
		ConfiguredProjectIDs:    append([]string(nil), configuredIDs...),
		DurableProjectIDs:       []string{},
		ValidCurrentPlans:       []string{},
		Plans:                   []StatePlan{},
		Issues:                  []StateIssue{},
		OperationalTaskRunGraph: false,
	}
	for _, projectID := range configuredIDs {
		configuration, err := s.ProjectConfigurationRead(ctx, projectID)
		if err != nil {
			result.Issues = append(result.Issues, stateIssue("DURABLE_PROJECTS_UNAVAILABLE", projectID, "", s.projectConfigurationPath(projectID), err.Error()))
			continue
		}
		if configuration.ProjectID != projectID {
			result.Issues = append(result.Issues, stateIssue("INVALID_DURABLE_PROJECT", projectID, "", s.projectConfigurationPath(projectID), "project configuration identity mismatch"))
			continue
		}
		result.DurableProjectIDs = append(result.DurableProjectIDs, projectID)
		trains, trainErr := s.sharedTrains(ctx, projectID)
		if trainErr != nil {
			result.Issues = append(result.Issues, stateIssue("TRAIN_V2_UNAVAILABLE", projectID, "", s.trainV2Root(projectID), trainErr.Error()))
			continue
		}
		owners := make(map[string]string)
		reported := make(map[string]bool)
		for _, train := range trains {
			if train.Historical != nil {
				continue
			}
			trainPath := s.trainV2Path(projectID, train.ID)
			for _, item := range train.Items {
				if owner, exists := owners[item.TaskID]; exists {
					if !reported[item.TaskID] {
						result.Issues = append(result.Issues, stateIssue("DUPLICATE_TRAIN_TASK_MEMBERSHIP", projectID, item.TaskID, trainPath, fmt.Sprintf("Task %q belongs to Trains %q and %q", item.TaskID, owner, train.ID)))
						reported[item.TaskID] = true
					}
					continue
				}
				owners[item.TaskID] = train.ID
				if item.Status != model.TrainV2ItemQueued && len(item.Attempts) == 0 {
					result.Issues = append(result.Issues, stateIssue("TRAIN_V2_ATTEMPT_MISSING", projectID, item.TaskID, trainPath, fmt.Sprintf("Train item %s has no item-local attempt", item.TaskID)))
				}
			}
		}
	}
	sort.Strings(result.DurableProjectIDs)
	result.Valid = len(result.Issues) == 0
	return result, nil
}
