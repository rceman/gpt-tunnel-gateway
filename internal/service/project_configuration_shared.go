package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

func (s *Service) projectConfigurationReadShared(ctx context.Context, projectID string) (model.ProjectConfiguration, error) {
	entity, err := s.Durability.ReadSharedEntity(ctx, "project_configuration", projectID)
	if err != nil {
		return model.ProjectConfiguration{}, err
	}
	configuration, err := sqlitestore.DecodeCanonicalProjectConfigurationPayload(entity.Payload)
	if err != nil {
		return model.ProjectConfiguration{}, fmt.Errorf("decode Shared project configuration %s: %w", projectID, err)
	}
	if configuration.ProjectID != projectID || int64(configuration.Revision) != entity.Revision {
		return model.ProjectConfiguration{}, fmt.Errorf("Shared project configuration identity mismatch")
	}
	if err := model.ValidateProjectConfiguration(configuration); err != nil {
		return model.ProjectConfiguration{}, err
	}
	return configuration, nil
}

func requireSharedProjectConfiguration(ctx context.Context, s *Service, projectID string) error {
	if err := model.ValidateProjectIdentifier(projectID); err != nil {
		return err
	}
	if _, err := s.EffectiveProjectConfig(projectID); err != nil {
		return fmt.Errorf("project %q is not configured locally: %w", projectID, err)
	}
	return nil
}

func (s *Service) publishSharedProjectConfiguration(ctx context.Context, configuration model.ProjectConfiguration) error {
	if err := model.ValidateProjectConfiguration(configuration); err != nil {
		return err
	}
	if _, err := s.migrateHubProjectConfiguration(ctx, configuration.ProjectID); err != nil && !IsNotFound(err) {
		return fmt.Errorf("migrate Hub project configuration before publication: %w", err)
	}
	path := s.projectConfigurationPath(configuration.ProjectID)
	_, err := s.Hub.Transact(ctx, "", "gateway: publish Shared project configuration "+configuration.ProjectID, func(worktree string) ([]string, error) {
		var latestRaw json.RawMessage
		readErr := readWorktreeJSON(worktree, path, &latestRaw)
		if readErr == nil {
			latest, decodeErr := sqlitestore.DecodeCanonicalProjectConfigurationPayload(latestRaw)
			if decodeErr != nil {
				return nil, fmt.Errorf("Hub project configuration is malformed: %w", decodeErr)
			}
			var compact bytes.Buffer
			if err := json.Compact(&compact, latestRaw); err != nil {
				return nil, fmt.Errorf("compact Hub project configuration: %w", err)
			}
			canonical, err := json.Marshal(latest)
			if err != nil {
				return nil, fmt.Errorf("encode Hub project configuration: %w", err)
			}
			if latest.ProjectID != configuration.ProjectID {
				return nil, fmt.Errorf("Hub project configuration identity conflicts with Shared outbox")
			}
			if err := model.ValidateProjectConfiguration(latest); err != nil {
				return nil, fmt.Errorf("Hub project configuration is invalid: %w", err)
			}
			if latest.Revision > configuration.Revision {
				return nil, fmt.Errorf("Hub project configuration is newer than Shared outbox")
			}
			if latest.Revision == configuration.Revision {
				if !reflect.DeepEqual(latest, configuration) {
					return nil, fmt.Errorf("Hub project configuration conflicts with Shared outbox")
				}
				if bytes.Equal(compact.Bytes(), canonical) {
					return nil, errSharedOutboxNoop
				}
			}
		} else if !IsNotFound(readErr) {
			return nil, readErr
		}
		if err := hub.WriteJSON(worktree, path, configuration); err != nil {
			return nil, err
		}
		return []string{path}, nil
	})
	return err
}
