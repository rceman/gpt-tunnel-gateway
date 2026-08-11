package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s *Service) ProjectIdentifiersAdopt(ctx context.Context, in ProjectIdentifiersAdoptInput) (model.ProjectIdentifiers, OperationResult, error) {
	if err := model.ValidateProjectIdentifier(in.ProjectID); err != nil {
		return model.ProjectIdentifiers{}, OperationResult{}, err
	}
	if err := model.ValidateProjectCode(in.ProjectCode); err != nil {
		return model.ProjectIdentifiers{}, OperationResult{}, err
	}
	if _, err := s.ProjectRead(ctx, in.ProjectID); err != nil {
		return model.ProjectIdentifiers{}, OperationResult{}, err
	}
	identifiers := model.ProjectIdentifiers{SchemaVersion: model.SchemaVersion, ProjectID: in.ProjectID, ProjectCode: in.ProjectCode, NextTaskNumber: 1, NextADRNumber: 1}
	if err := model.ValidateProjectIdentifiers(identifiers); err != nil {
		return model.ProjectIdentifiers{}, OperationResult{}, err
	}
	tx, err := s.Hub.Transact(ctx, in.ExpectedHubRevision, "gateway: adopt project identifiers "+in.ProjectID, func(worktree string) ([]string, error) {
		var project model.Project
		if err := readWorktreeJSON(worktree, s.projectPath(in.ProjectID), &project); err != nil {
			return nil, fmt.Errorf("project %q is not durable: %w", in.ProjectID, err)
		}
		if err := model.ValidateProject(project); err != nil {
			return nil, fmt.Errorf("project %q is invalid: %w", in.ProjectID, err)
		}
		if project.ID != in.ProjectID {
			return nil, fmt.Errorf("project %q has mismatched durable ID", in.ProjectID)
		}
		identifiersPath := s.projectIdentifiersPath(in.ProjectID)
		if _, err := os.Lstat(filepath.Join(worktree, filepath.FromSlash(identifiersPath))); err == nil {
			return nil, fmt.Errorf("project identifiers already exist for %q", in.ProjectID)
		} else if !os.IsNotExist(err) {
			return nil, err
		}
		projectsRoot := filepath.Join(worktree, filepath.FromSlash(hub.ProtocolRoot), "projects")
		entries, err := os.ReadDir(projectsRoot)
		if err != nil {
			return nil, fmt.Errorf("list durable projects: %w", err)
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			projectID := entry.Name()
			if err := model.ValidateProjectIdentifier(projectID); err != nil {
				return nil, fmt.Errorf("invalid durable project directory %q: %w", projectID, err)
			}
			path := s.projectIdentifiersPath(projectID)
			if _, err := os.Lstat(filepath.Join(worktree, filepath.FromSlash(path))); err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return nil, err
			}
			var existing model.ProjectIdentifiers
			if err := readWorktreeJSON(worktree, path, &existing); err != nil {
				return nil, fmt.Errorf("read project identifiers for %q: %w", projectID, err)
			}
			if err := model.ValidateProjectIdentifiers(existing); err != nil {
				return nil, fmt.Errorf("invalid project identifiers for %q: %w", projectID, err)
			}
			if existing.ProjectID != projectID {
				return nil, fmt.Errorf("project identifiers for %q have mismatched project_id", projectID)
			}
			if existing.ProjectCode == in.ProjectCode {
				return nil, fmt.Errorf("project code %q is already adopted by %q", in.ProjectCode, projectID)
			}
		}
		if err := hub.WriteJSON(worktree, identifiersPath, identifiers); err != nil {
			return nil, err
		}
		return []string{identifiersPath}, nil
	})
	if err != nil {
		return model.ProjectIdentifiers{}, OperationResult{}, err
	}
	return identifiers, OperationResult{
		Hub:       tx,
		ProjectID: in.ProjectID,
		Status:    "adopted",
	}, nil
}

func (s *Service) ProjectRegister(ctx context.Context, in ProjectRegisterInput) (OperationResult, error) {
	p := in.Project
	now := time.Now().UTC()
	p.SchemaVersion = model.SchemaVersion
	if p.CreatedAt.IsZero() {
		p.CreatedAt = now
	}
	p.UpdatedAt = now
	if p.Status == "" {
		p.Status = "active"
	}
	if err := model.ValidateProject(p); err != nil {
		return OperationResult{}, err
	}
	if _, err := s.projectConfig(p.ID); err != nil {
		return OperationResult{}, err
	}
	plan := model.Plan{
		SchemaVersion:    model.PlanSchemaVersion,
		ProjectID:        p.ID,
		Revision:         1,
		Title:            "Registered active project",
		Summary:          "Registered active project with no current authorized work",
		CurrentObjective: "The " + p.ID + " repository is registered with the gateway and is available for future durable tasks. No task or run is currently active.\n\nNext action: Await an explicitly authorized durable task before implementation, release, runtime mutation or repository changes.",
		Queue:            []string{},
		Sections:         []model.PlanSectionIndex{},
		UpdatedBy:        in.Project.ID,
		UpdatedAt:        now,
	}
	if err := model.ValidatePlan(plan); err != nil {
		return OperationResult{}, err
	}
	configuration := model.DefaultProjectConfiguration(p.ID, now)
	if err := model.ValidateProjectConfiguration(configuration); err != nil {
		return OperationResult{}, err
	}
	tx, err := s.Hub.Transact(ctx, in.ExpectedHubRevision, "gateway: register project "+p.ID, func(w string) ([]string, error) {
		projectPath := s.projectPath(p.ID)
		if _, err := os.Stat(filepath.Join(w, filepath.FromSlash(projectPath))); err == nil {
			return nil, fmt.Errorf("project already exists")
		}
		planPath := s.planPath(p.ID)
		if _, err := os.Stat(filepath.Join(w, filepath.FromSlash(planPath))); err == nil {
			return nil, fmt.Errorf("project plan already exists")
		}
		configurationPath := s.projectConfigurationPath(p.ID)
		if _, err := os.Stat(filepath.Join(w, filepath.FromSlash(configurationPath))); err == nil {
			return nil, fmt.Errorf("project configuration already exists")
		}
		if err := hub.WriteJSON(w, projectPath, p); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(w, planPath, plan); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(w, configurationPath, configuration); err != nil {
			return nil, err
		}
		return []string{projectPath, planPath, configurationPath}, nil
	})
	if err != nil {
		return OperationResult{}, err
	}
	return OperationResult{
		Hub:       tx,
		ProjectID: p.ID,
		Status:    "registered",
	}, nil
}
