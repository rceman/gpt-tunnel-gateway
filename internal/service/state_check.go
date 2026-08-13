package service

import (
	"context"
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
	}
	// Plan files remain immutable history and are intentionally not part of
	// current-state validation after the Train-v2 cutover.
	result.Valid = len(result.Issues) == 0
	return result, nil
}
