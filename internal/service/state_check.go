package service

import (
	"context"
	"fmt"
	"sort"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
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
	snapshot, err := s.Hub.FreshReadSnapshot(ctx)
	if err != nil {
		result.Issues = append(result.Issues, stateIssue("HUB_UNAVAILABLE", "", "", "", err.Error()))
		result.Valid = false
		return result, nil
	}
	defer snapshot.Close()
	ctx = hub.WithReadSnapshot(ctx, snapshot)
	revision, err := s.Hub.RemoteRevision(ctx)
	if err != nil {
		result.Issues = append(result.Issues, stateIssue("HUB_UNAVAILABLE", "", "", "", err.Error()))
		result.Valid = false
		return result, nil
	}
	result.HubRevision = revision
	projects, err := s.ProjectList(ctx)
	if err != nil {
		result.Issues = append(result.Issues, stateIssue("DURABLE_PROJECTS_UNAVAILABLE", "", "", "", err.Error()))
		result.Valid = false
		return result, nil
	}
	durable := map[string]model.Project{}
	for _, project := range projects {
		if err := model.ValidateProject(project); err != nil {
			result.Issues = append(result.Issues, stateIssue("INVALID_DURABLE_PROJECT", project.ID, "", s.projectPath(project.ID), err.Error()))
			continue
		}
		if _, exists := durable[project.ID]; exists {
			result.Issues = append(result.Issues, stateIssue("DUPLICATE_DURABLE_PROJECT", project.ID, "", s.projectPath(project.ID), "duplicate project ID"))
			continue
		}
		durable[project.ID] = project
		result.DurableProjectIDs = append(result.DurableProjectIDs, project.ID)
	}
	sort.Strings(result.DurableProjectIDs)
	for _, project := range projects {
		if project.Status == "active" {
			if _, configured := resolution.Projects[project.ID]; !configured {
				result.Issues = append(result.Issues, stateIssue("DURABLE_PROJECT_NOT_CONFIGURED", project.ID, "", s.projectPath(project.ID), "active durable project is not configured"))
			}
		}
	}
	for _, id := range result.ConfiguredProjectIDs {
		project, exists := durable[id]
		if !exists {
			result.Issues = append(result.Issues, stateIssue("CONFIGURED_PROJECT_MISSING", id, "", s.projectPath(id), "configured project has no durable project record"))
			continue
		}
		if project.Status != "active" {
			result.Issues = append(result.Issues, stateIssue("CONFIGURED_PROJECT_NOT_ACTIVE", id, "", s.projectPath(id), "configured project is not active"))
		}
		trains, trainErr := s.readTrainV2Records(ctx, id)
		if trainErr != nil {
			if !IsNotFound(trainErr) {
				result.Issues = append(result.Issues, stateIssue("TRAIN_V2_UNAVAILABLE", id, "", s.trainV2Root(id), trainErr.Error()))
			}
			continue
		}
		owners := make(map[string]string)
		reported := make(map[string]bool)
		for _, train := range trains {
			if train.Historical != nil {
				continue
			}
			for _, item := range train.Items {
				if owner, exists := owners[item.TaskID]; exists {
					if !reported[item.TaskID] {
						result.Issues = append(result.Issues, stateIssue("DUPLICATE_TRAIN_TASK_MEMBERSHIP", id, item.TaskID, s.trainV2Path(id, train.ID), fmt.Sprintf("Task %q belongs to Trains %q and %q", item.TaskID, owner, train.ID)))
						reported[item.TaskID] = true
					}
					continue
				}
				owners[item.TaskID] = train.ID
			}
		}
		for _, train := range trains {
			if train.Historical != nil {
				continue
			}
			classification, classifyErr := s.classifyTrainV2LifecycleWithContext(ctx, id, train)
			if classifyErr != nil {
				result.Issues = append(result.Issues, stateIssue("TRAIN_V2_RECONCILIATION_UNAVAILABLE", id, "", s.trainV2Path(id, train.ID), classifyErr.Error()))
				continue
			}
			switch classification.Class {
			case trainV2ClassStale, trainV2ClassAmbiguous:
				result.Issues = append(result.Issues, stateIssue("TRAIN_STALE_RECONCILIATION_REQUIRED", id, "", s.trainV2Path(id, train.ID), classification.Detail))
			}
		}
	}
	// Plan files remain immutable history and are intentionally not part of
	// current-state validation after the Train-v2 cutover.
	result.Valid = len(result.Issues) == 0
	return result, nil
}
