package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

const projectConfigurationHubMigrationMaxProjects = 4096

var errHubProjectConfigurationMigrationNoChanges = errors.New("Hub ProjectConfiguration migration made no changes")

func (s *Service) MigrateHubProjectConfigurations(ctx context.Context) error {
	projects, err := s.ProjectList(ctx)
	if err != nil {
		return fmt.Errorf("list Hub projects for ProjectConfiguration migration: %w", err)
	}
	if len(projects) > projectConfigurationHubMigrationMaxProjects {
		return fmt.Errorf("Hub ProjectConfiguration migration exceeds bounded project maximum")
	}
	seen := make(map[string]struct{}, len(projects))
	for _, project := range projects {
		if err := model.ValidateProjectIdentifier(project.ID); err != nil {
			return fmt.Errorf("invalid Hub project identity in ProjectConfiguration migration")
		}
		if _, exists := seen[project.ID]; exists {
			return fmt.Errorf("duplicate Hub project identity in ProjectConfiguration migration")
		}
		seen[project.ID] = struct{}{}
	}
	if len(projects) == 0 {
		return nil
	}
	_, err = s.Hub.Transact(ctx, "", "gateway: migrate Hub ProjectConfigurations", func(worktree string) ([]string, error) {
		changed := make([]string, 0, len(projects))
		for _, project := range projects {
			path := s.projectConfigurationPath(project.ID)
			var raw json.RawMessage
			if err := readWorktreeJSON(worktree, path, &raw); err != nil {
				if IsNotFound(err) {
					continue
				}
				return nil, fmt.Errorf("read Hub ProjectConfiguration %q: %w", project.ID, err)
			}
			configuration, canonical, err := migrateHubProjectConfigurationPayload(project.ID, raw)
			if err != nil {
				return nil, fmt.Errorf("migrate Hub ProjectConfiguration %q: %w", project.ID, err)
			}
			var compact bytes.Buffer
			if err := json.Compact(&compact, raw); err != nil {
				return nil, fmt.Errorf("compact Hub ProjectConfiguration %q: %w", project.ID, err)
			}
			if bytes.Equal(compact.Bytes(), canonical) {
				continue
			}
			if err := hub.WriteJSON(worktree, path, configuration); err != nil {
				return nil, err
			}
			changed = append(changed, path)
		}
		if len(changed) == 0 {
			return nil, errHubProjectConfigurationMigrationNoChanges
		}
		return changed, nil
	})
	if errors.Is(err, errHubProjectConfigurationMigrationNoChanges) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("migrate Hub ProjectConfigurations: %w", err)
	}
	return nil
}

func (s *Service) migrateHubProjectConfiguration(ctx context.Context, projectID string) (model.ProjectConfiguration, error) {
	path := s.projectConfigurationPath(projectID)
	var raw json.RawMessage
	if err := s.Hub.ReadJSON(ctx, path, &raw); err != nil {
		return model.ProjectConfiguration{}, err
	}
	configuration, canonical, err := migrateHubProjectConfigurationPayload(projectID, raw)
	if err != nil {
		return model.ProjectConfiguration{}, err
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		return model.ProjectConfiguration{}, fmt.Errorf("compact Hub ProjectConfiguration: %w", err)
	}
	if bytes.Equal(compact.Bytes(), canonical) {
		return configuration, nil
	}
	if _, err := s.Hub.Transact(ctx, "", "gateway: migrate Hub ProjectConfiguration "+projectID, func(worktree string) ([]string, error) {
		var latestRaw json.RawMessage
		if err := readWorktreeJSON(worktree, path, &latestRaw); err != nil {
			return nil, err
		}
		latest, latestCanonical, err := migrateHubProjectConfigurationPayload(projectID, latestRaw)
		if err != nil {
			return nil, err
		}
		if latest.ProjectID != projectID || latest.Revision != configuration.Revision || !bytes.Equal(latestCanonical, canonical) {
			return nil, fmt.Errorf("Hub ProjectConfiguration changed during migration")
		}
		if err := hub.WriteJSON(worktree, path, configuration); err != nil {
			return nil, err
		}
		return []string{path}, nil
	}); err != nil {
		return model.ProjectConfiguration{}, err
	}
	return configuration, nil
}

func migrateHubProjectConfigurationPayload(projectID string, raw []byte) (model.ProjectConfiguration, []byte, error) {
	configuration, canonical, err := sqlitestore.MigrateProjectConfigurationPayload(raw)
	if err != nil {
		return model.ProjectConfiguration{}, nil, err
	}
	if configuration.ProjectID != projectID {
		return model.ProjectConfiguration{}, nil, fmt.Errorf("Hub ProjectConfiguration project_id mismatch")
	}
	if err := model.ValidateProjectConfiguration(configuration); err != nil {
		return model.ProjectConfiguration{}, nil, err
	}
	return configuration, canonical, nil
}
