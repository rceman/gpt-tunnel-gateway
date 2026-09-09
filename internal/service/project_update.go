package service

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/session"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

func (s *Service) ProjectUpdate(ctx context.Context, in ProjectUpdateInput) (ProjectUpdateResult, error) {
	if err := model.ValidateProjectIdentifier(in.ProjectID); err != nil {
		return ProjectUpdateResult{}, err
	}
	if err := model.ValidateProjectCode(in.ProjectCode); err != nil {
		return ProjectUpdateResult{}, err
	}
	if s.Durability == nil {
		return ProjectUpdateResult{}, fmt.Errorf("Shared durability is required for project update")
	}
	local, err := s.EffectiveProjectConfig(in.ProjectID)
	if err != nil {
		return ProjectUpdateResult{}, err
	}
	if local.ProjectCode == in.ProjectCode {
		return ProjectUpdateResult{}, fmt.Errorf("project code is already %q", in.ProjectCode)
	}
	snapshot, err := s.Hub.ReadSnapshot(ctx)
	if err != nil {
		return ProjectUpdateResult{}, err
	}
	preflight := func() (model.ProjectIdentifiers, model.ProjectConfiguration, error) {
		readCtx := hub.WithReadSnapshot(ctx, snapshot)
		project, err := s.ProjectRead(readCtx, in.ProjectID)
		if err != nil {
			return model.ProjectIdentifiers{}, model.ProjectConfiguration{}, err
		}
		if err := model.ValidateProject(project); err != nil {
			return model.ProjectIdentifiers{}, model.ProjectConfiguration{}, err
		}
		identifiers, err := s.ProjectIdentifiersRead(readCtx, in.ProjectID)
		if err != nil {
			return model.ProjectIdentifiers{}, model.ProjectConfiguration{}, err
		}
		if identifiers.ProjectCode != local.ProjectCode {
			return model.ProjectIdentifiers{}, model.ProjectConfiguration{}, fmt.Errorf("local and durable project codes disagree")
		}
		if identifiers.NextTaskNumber != 1 || identifiers.NextADRNumber != 1 {
			return model.ProjectIdentifiers{}, model.ProjectConfiguration{}, fmt.Errorf("project code correction requires virgin Hub identifier counters")
		}
		var configuration model.ProjectConfiguration
		if err := snapshot.ReadJSON(readCtx, s.projectConfigurationPath(in.ProjectID), &configuration); err != nil {
			return model.ProjectIdentifiers{}, model.ProjectConfiguration{}, fmt.Errorf("Hub project configuration is unavailable: %w", err)
		}
		normalizeProjectConfiguration(&configuration)
		if err := model.ValidateProjectConfiguration(configuration); err != nil {
			return model.ProjectIdentifiers{}, model.ProjectConfiguration{}, err
		}
		if err := validateVirginHub(readCtx, snapshot, in.ProjectID, in.ProjectCode); err != nil {
			return model.ProjectIdentifiers{}, model.ProjectConfiguration{}, err
		}
		if err := validateVirginLocal(s.Durability, in.ProjectID); err != nil {
			return model.ProjectIdentifiers{}, model.ProjectConfiguration{}, err
		}
		if err := validateVirginShared(ctx, s.Durability, in.ProjectID); err != nil {
			return model.ProjectIdentifiers{}, model.ProjectConfiguration{}, err
		}
		if err := ensureUniqueHubCode(readCtx, snapshot, in.ProjectID, in.ProjectCode); err != nil {
			return model.ProjectIdentifiers{}, model.ProjectConfiguration{}, err
		}
		return identifiers, configuration, nil
	}
	identifiers, configuration, preflightErr := preflight()
	hubRevision := snapshot.Revision()
	closeErr := snapshot.Close()
	if preflightErr != nil {
		return ProjectUpdateResult{}, preflightErr
	}
	if closeErr != nil {
		return ProjectUpdateResult{}, fmt.Errorf("close Hub preflight snapshot: %w", closeErr)
	}

	originalConfig, err := config.UpdateProjectCode(s.ConfigPath, in.ProjectID, local.ProjectCode, in.ProjectCode)
	if err != nil {
		return ProjectUpdateResult{}, err
	}
	tx, err := s.Hub.Transact(ctx, hubRevision, "gateway: update project code "+in.ProjectID, func(worktree string) ([]string, error) {
		path := s.projectIdentifiersPath(in.ProjectID)
		var current model.ProjectIdentifiers
		if err := readWorktreeJSON(worktree, path, &current); err != nil {
			return nil, err
		}
		if current.ProjectCode != local.ProjectCode {
			return nil, fmt.Errorf("durable project code changed during update")
		}
		current.ProjectCode = in.ProjectCode
		if err := model.ValidateProjectIdentifiers(current); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, path, current); err != nil {
			return nil, err
		}
		return []string{path}, nil
	})
	if err != nil {
		_ = config.Restore(s.ConfigPath, originalConfig)
		return ProjectUpdateResult{}, err
	}
	if err := s.Durability.ReconcileProjectBootstrap(ctx, sqlitestore.ProjectBootstrapUpdate{ProjectID: in.ProjectID, PreviousProjectCode: local.ProjectCode, ProjectCode: in.ProjectCode, HubIdentifiers: identifiers, Configuration: configuration}); err != nil {
		rollbackErr := s.rollbackProjectCode(ctx, tx.After, in.ProjectID, local.ProjectCode)
		configErr := config.Restore(s.ConfigPath, originalConfig)
		if rollbackErr != nil || configErr != nil {
			return ProjectUpdateResult{}, fmt.Errorf("project update left a mixed authority state: shared=%v hub_rollback=%v config_rollback=%v", err, rollbackErr, configErr)
		}
		return ProjectUpdateResult{}, err
	}
	return ProjectUpdateResult{
		ProjectID:           in.ProjectID,
		PreviousProjectCode: local.ProjectCode,
		ProjectCode:         in.ProjectCode,
		SharedConfiguration: true,
	}, nil
}

func (s *Service) rollbackProjectCode(ctx context.Context, expected, projectID, oldCode string) error {
	_, err := s.Hub.Transact(ctx, expected, "gateway: rollback project code "+projectID, func(worktree string) ([]string, error) {
		path := s.projectIdentifiersPath(projectID)
		var ids model.ProjectIdentifiers
		if err := readWorktreeJSON(worktree, path, &ids); err != nil {
			return nil, err
		}
		ids.ProjectCode = oldCode
		if err := model.ValidateProjectIdentifiers(ids); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, path, ids); err != nil {
			return nil, err
		}
		return []string{path}, nil
	})
	return err
}

func validateVirginHub(ctx context.Context, snapshot *hub.ReadSnapshot, projectID, newCode string) error {
	for _, suffix := range []string{"/tasks-v2/", "/tasks/", "/adrs/", "/agents/", "/trains-v2/", "/train-v2-starts/", "/train-attempts/", "/runs/", "/operations/", "/releases/", "/deployments/"} {
		paths, err := snapshot.List(ctx, "gpt-tunnel/v1/projects/"+projectID+suffix, ".json")
		if err != nil && !IsNotFound(err) {
			return err
		}
		if len(paths) > 0 {
			return fmt.Errorf("project %q is not bootstrap-only: durable path %q exists", projectID, paths[0])
		}
	}
	return nil
}

func ensureUniqueHubCode(ctx context.Context, snapshot *hub.ReadSnapshot, projectID, code string) error {
	paths, err := snapshot.List(ctx, "gpt-tunnel/v1/projects", "/identifiers.json")
	if err != nil {
		return err
	}
	for _, path := range paths {
		var ids model.ProjectIdentifiers
		if err := snapshot.ReadJSON(ctx, path, &ids); err != nil {
			return err
		}
		if ids.ProjectID != projectID && ids.ProjectCode == code {
			return fmt.Errorf("project code %q is already used by %q", code, ids.ProjectID)
		}
	}
	return nil
}

func validateVirginLocal(d *sqlitestore.Databases, projectID string) error {
	records, err := session.NewStoreWithDurability(d).List()
	if err != nil {
		return err
	}
	for _, record := range records {
		if record.ProjectID == projectID {
			return fmt.Errorf("project %q has durable sessions", projectID)
		}
	}
	agents, err := d.ListLocalAgents(context.Background(), projectID, 1000)
	if err != nil {
		return err
	}
	if len(agents) > 0 {
		return fmt.Errorf("project %q has durable agents", projectID)
	}
	return nil
}

func validateVirginShared(ctx context.Context, d *sqlitestore.Databases, projectID string) error {
	for _, typ := range []string{"task", "adr", "train", "journal", "rule"} {
		entities, err := d.ListSharedEntities(ctx, typ, 1000)
		if err != nil {
			return err
		}
		for _, entity := range entities {
			var identity struct {
				ProjectID string `json:"project_id"`
			}
			if err := json.Unmarshal(entity.Payload, &identity); err != nil {
				return fmt.Errorf("decode Shared %s entity %q: %w", typ, entity.ID, err)
			}
			if identity.ProjectID == projectID {
				return fmt.Errorf("project %q has Shared %s entities", projectID, typ)
			}
		}
	}
	return nil
}
