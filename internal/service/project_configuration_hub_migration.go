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

// HubProjectConfigurationMigrationPlan is the read-only planning result of
// one Hub ProjectConfiguration migration. Its Hub reads can share a pinned
// ReadSnapshot consistency point; the write boundary stays a separate
// bounded transaction.
type HubProjectConfigurationMigrationPlan struct {
	projects       []model.Project
	policies       map[string]*model.ProjectWorkflowPolicy
	policyPayloads map[string][]byte
	needed         bool
}

func (s *Service) MigrateHubProjectConfigurations(ctx context.Context) error {
	plan, err := s.PlanHubProjectConfigurationMigration(ctx)
	if err != nil {
		return err
	}
	return s.ApplyHubProjectConfigurationMigration(ctx, plan)
}

// PlanHubProjectConfigurationMigration performs the read phase of Hub
// ProjectConfiguration migration: project enumeration and payload reads. The
// ctx may carry a pinned hub.ReadSnapshot so the whole convergence attempt
// shares one fresh consistency point instead of fetching per project.
func (s *Service) PlanHubProjectConfigurationMigration(ctx context.Context) (*HubProjectConfigurationMigrationPlan, error) {
	projects, err := s.ProjectList(ctx)
	if err != nil {
		return nil, fmt.Errorf("list Hub projects for ProjectConfiguration migration: %w", err)
	}
	if len(projects) > projectConfigurationHubMigrationMaxProjects {
		return nil, fmt.Errorf("Hub ProjectConfiguration migration exceeds bounded project maximum")
	}
	seen := make(map[string]struct{}, len(projects))
	policies := make(map[string]*model.ProjectWorkflowPolicy, len(projects))
	policyPayloads := make(map[string][]byte, len(projects))
	for _, project := range projects {
		if err := model.ValidateProjectIdentifier(project.ID); err != nil {
			return nil, fmt.Errorf("invalid Hub project identity in ProjectConfiguration migration")
		}
		if _, exists := seen[project.ID]; exists {
			return nil, fmt.Errorf("duplicate Hub project identity in ProjectConfiguration migration")
		}
		seen[project.ID] = struct{}{}
		if s.Durability == nil {
			var policyRaw json.RawMessage
			if err := s.Hub.ReadJSON(ctx, s.workflowPolicyPath(project.ID), &policyRaw); err == nil {
				policy, canonical, err := migrateHubWorkflowPolicyPayload(project.ID, policyRaw)
				if err != nil {
					return nil, fmt.Errorf("migrate Hub ProjectWorkflowPolicy %q: %w", project.ID, err)
				}
				policies[project.ID] = &policy
				policyPayloads[project.ID] = canonical
			} else if !IsNotFound(err) {
				return nil, fmt.Errorf("read Hub ProjectWorkflowPolicy %q: %w", project.ID, err)
			}
		}
		var configurationRaw json.RawMessage
		if err := s.Hub.ReadJSON(ctx, s.projectConfigurationPath(project.ID), &configurationRaw); err != nil {
			if IsNotFound(err) {
				continue
			}
			return nil, fmt.Errorf("read Hub ProjectConfiguration %q: %w", project.ID, err)
		}
		if sqlitestore.ProjectConfigurationPayloadRequiresWorkflowPolicy(configurationRaw) && policies[project.ID] == nil {
			if s.Durability != nil {
				policy, err := s.ProjectWorkflowPolicyRead(ctx, project.ID)
				if err != nil {
					return nil, fmt.Errorf("read canonical ProjectWorkflowPolicy for %q: %w", project.ID, err)
				}
				policies[project.ID] = &policy
			} else {
				return nil, fmt.Errorf("canonical Hub ProjectWorkflowPolicy is missing for %q", project.ID)
			}
		}
	}
	plan := &HubProjectConfigurationMigrationPlan{
		projects:       projects,
		policies:       policies,
		policyPayloads: policyPayloads,
	}
	for _, project := range plan.projects {
		var raw json.RawMessage
		if err := s.Hub.ReadJSON(ctx, s.projectConfigurationPath(project.ID), &raw); err != nil {
			if IsNotFound(err) {
				continue
			}
			return nil, fmt.Errorf("read Hub ProjectConfiguration %q: %w", project.ID, err)
		}
		_, canonical, err := migrateHubProjectConfigurationPayload(project.ID, raw, plan.policies[project.ID])
		if err != nil {
			return nil, fmt.Errorf("migrate Hub ProjectConfiguration %q: %w", project.ID, err)
		}
		var compact bytes.Buffer
		if err := json.Compact(&compact, raw); err != nil {
			return nil, fmt.Errorf("compact Hub ProjectConfiguration %q: %w", project.ID, err)
		}
		if !bytes.Equal(compact.Bytes(), canonical) {
			plan.needed = true
		}
	}
	for projectID, expectedCanonical := range plan.policyPayloads {
		var raw json.RawMessage
		if err := s.Hub.ReadJSON(ctx, s.workflowPolicyPath(projectID), &raw); err != nil {
			return nil, fmt.Errorf("read Hub ProjectWorkflowPolicy %q: %w", projectID, err)
		}
		_, canonical, err := migrateHubWorkflowPolicyPayload(projectID, raw)
		if err != nil || !bytes.Equal(canonical, expectedCanonical) {
			return nil, fmt.Errorf("Hub ProjectWorkflowPolicy changed during migration")
		}
		var compact bytes.Buffer
		if err := json.Compact(&compact, raw); err != nil {
			return nil, err
		}
		if !bytes.Equal(compact.Bytes(), canonical) {
			plan.needed = true
		}
	}
	return plan, nil
}

// ApplyHubProjectConfigurationMigration commits a previously planned
// migration in one bounded Hub transaction. The transaction re-reads the
// worktree under the repository lock and fails closed when the planned
// canonical payloads drifted, so a stale plan is safe.
func (s *Service) ApplyHubProjectConfigurationMigration(ctx context.Context, plan *HubProjectConfigurationMigrationPlan) error {
	if plan == nil || !plan.needed {
		return nil
	}
	_, err := s.Hub.Transact(ctx, "", "gateway: migrate Hub ProjectConfigurations", func(worktree string) ([]string, error) {
		changed := make([]string, 0, len(plan.projects))
		for _, project := range plan.projects {
			path := s.projectConfigurationPath(project.ID)
			var raw json.RawMessage
			if err := readWorktreeJSON(worktree, path, &raw); err != nil {
				if IsNotFound(err) {
					continue
				}
				return nil, fmt.Errorf("read Hub ProjectConfiguration %q: %w", project.ID, err)
			}
			configuration, canonical, err := migrateHubProjectConfigurationPayload(project.ID, raw, plan.policies[project.ID])
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
		for projectID, expectedCanonical := range plan.policyPayloads {
			path := s.workflowPolicyPath(projectID)
			var latestRaw json.RawMessage
			if err := readWorktreeJSON(worktree, path, &latestRaw); err != nil {
				return nil, err
			}
			latest, latestCanonical, err := migrateHubWorkflowPolicyPayload(projectID, latestRaw)
			if err != nil || !bytes.Equal(latestCanonical, expectedCanonical) {
				return nil, fmt.Errorf("Hub ProjectWorkflowPolicy changed during migration")
			}
			var compact bytes.Buffer
			if err := json.Compact(&compact, latestRaw); err != nil {
				return nil, err
			}
			if bytes.Equal(compact.Bytes(), latestCanonical) {
				continue
			}
			if err := hub.WriteJSON(worktree, path, latest); err != nil {
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
	var policy *model.ProjectWorkflowPolicy
	if sqlitestore.ProjectConfigurationPayloadRequiresWorkflowPolicy(raw) {
		if s.Durability != nil {
			current, err := s.ProjectWorkflowPolicyRead(ctx, projectID)
			if err != nil {
				return model.ProjectConfiguration{}, fmt.Errorf("read canonical ProjectWorkflowPolicy: %w", err)
			}
			policy = &current
		} else {
			var policyRaw json.RawMessage
			if err := s.Hub.ReadJSON(ctx, s.workflowPolicyPath(projectID), &policyRaw); err != nil {
				return model.ProjectConfiguration{}, fmt.Errorf("read canonical Hub ProjectWorkflowPolicy: %w", err)
			}
			current, _, err := migrateHubWorkflowPolicyPayload(projectID, policyRaw)
			if err != nil {
				return model.ProjectConfiguration{}, fmt.Errorf("migrate canonical Hub ProjectWorkflowPolicy: %w", err)
			}
			policy = &current
		}
	}
	configuration, canonical, err := migrateHubProjectConfigurationPayload(projectID, raw, policy)
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
		latest, latestCanonical, err := migrateHubProjectConfigurationPayload(projectID, latestRaw, policy)
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

func migrateHubProjectConfigurationPayload(projectID string, raw []byte, policy *model.ProjectWorkflowPolicy) (model.ProjectConfiguration, []byte, error) {
	configuration, canonical, err := sqlitestore.MigrateProjectConfigurationPayloadWithPolicy(raw, policy)
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

func migrateHubWorkflowPolicyPayload(projectID string, raw []byte) (model.ProjectWorkflowPolicy, []byte, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return model.ProjectWorkflowPolicy{}, nil, fmt.Errorf("decode Hub ProjectWorkflowPolicy migration payload")
	}
	if gatesRaw, ok := fields["gates"]; ok {
		var selected []string
		if err := json.Unmarshal(gatesRaw, &selected); err != nil || len(selected) != 0 {
			return model.ProjectWorkflowPolicy{}, nil, fmt.Errorf("ProjectWorkflowPolicy gates require explicit migration")
		}
		delete(fields, "gates")
	}
	canonicalInput, err := json.Marshal(fields)
	if err != nil {
		return model.ProjectWorkflowPolicy{}, nil, err
	}
	var policy model.ProjectWorkflowPolicy
	if err := decodeStrict(canonicalInput, &policy); err != nil {
		return model.ProjectWorkflowPolicy{}, nil, err
	}
	if err := model.ValidateProjectWorkflowPolicy(policy); err != nil {
		return model.ProjectWorkflowPolicy{}, nil, err
	}
	if policy.ProjectID != projectID {
		return model.ProjectWorkflowPolicy{}, nil, fmt.Errorf("Hub ProjectWorkflowPolicy project_id mismatch")
	}
	canonical, err := json.Marshal(policy)
	return policy, canonical, err
}
