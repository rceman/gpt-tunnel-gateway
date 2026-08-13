package service

import (
	"context"
	"encoding/json"
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
		plan, planErr := s.PlanRead(ctx, id)
		if planErr != nil {
			if raw, rawErr := s.Hub.ReadFile(ctx, s.planPath(id)); rawErr == nil {
				var object map[string]any
				if json.Unmarshal(raw, &object) == nil {
					if _, hasBody := object["body"]; hasBody {
						result.Issues = append(result.Issues, stateIssue("LEGACY_PLAN_BODY", id, "", s.planPath(id), "workflow-v1 plan contains obsolete body field"))
					}
				}
			}
			result.Issues = append(result.Issues, stateIssue("CURRENT_PLAN_INVALID", id, "", s.planPath(id), planErr.Error()))
			result.Plans = append(result.Plans, StatePlan{
				ProjectID: id,
				Valid:     false,
			})
			continue
		}
		result.ValidCurrentPlans = append(result.ValidCurrentPlans, id)
		result.Plans = append(result.Plans, StatePlan{
			ProjectID:    id,
			Valid:        true,
			ActiveTaskID: plan.ActiveTaskID,
		})
	}
	sort.Strings(result.ValidCurrentPlans)
	result.Valid = len(result.Issues) == 0
	return result, nil
}
