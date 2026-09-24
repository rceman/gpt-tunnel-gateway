package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"time"

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

func (s *Service) projectConfigurationUpdateShared(ctx context.Context, in ProjectConfigurationUpdateInput) (model.ProjectConfiguration, OperationResult, error) {
	if err := requireSharedProjectConfiguration(ctx, s, in.ProjectID); err != nil {
		return model.ProjectConfiguration{}, OperationResult{}, err
	}
	if err := validateProjectConfigurationUpdateInput(in); err != nil {
		return model.ProjectConfiguration{}, OperationResult{}, err
	}
	operationID, err := projectConfigurationOperationID(ctx, in)
	if err != nil {
		return model.ProjectConfiguration{}, OperationResult{}, err
	}
	if existing, found, err := s.Durability.ReadSharedOutboxEntry(ctx, operationID); err != nil {
		return model.ProjectConfiguration{}, OperationResult{}, err
	} else if found {
		if existing.EntityType != "project_configuration" || existing.EntityID != in.ProjectID || existing.Kind != "project-configuration-update" {
			return model.ProjectConfiguration{}, OperationResult{}, fmt.Errorf("shared project configuration operation identity mismatch")
		}
		committed, err := sqlitestore.DecodeCanonicalProjectConfigurationPayload(existing.Payload)
		if err != nil {
			return model.ProjectConfiguration{}, OperationResult{}, fmt.Errorf("decode committed Shared project configuration %s: %w", in.ProjectID, err)
		}
		if committed.ProjectID != in.ProjectID || int64(committed.Revision) != existing.Revision {
			return model.ProjectConfiguration{}, OperationResult{}, fmt.Errorf("committed Shared project configuration identity mismatch")
		}
		if err := model.ValidateProjectConfiguration(committed); err != nil {
			return model.ProjectConfiguration{}, OperationResult{}, err
		}
		return committed, OperationResult{
			OperationID: operationID,
			ProjectID:   in.ProjectID,
			Status:      "updated",
			Hub:         hub.TransactionResult{Paths: []string{}},
		}, nil
	}
	current, err := s.projectConfigurationReadShared(ctx, in.ProjectID)
	if err != nil {
		return model.ProjectConfiguration{}, OperationResult{}, err
	}
	if current.Revision != in.ExpectedRevision {
		return model.ProjectConfiguration{}, OperationResult{}, fmt.Errorf("project configuration revision conflict: expected %d, current %d", in.ExpectedRevision, current.Revision)
	}
	active, err := s.projectHasActiveTaskExecution(ctx, in.ProjectID)
	if err != nil {
		return model.ProjectConfiguration{}, OperationResult{}, fmt.Errorf("inspect active Task execution: %w", err)
	}
	if active && projectConfigurationPatchIsExecutionSensitive(in.Patch) {
		return model.ProjectConfiguration{}, OperationResult{}, fmt.Errorf("execution-sensitive project configuration cannot change while an active Task execution exists")
	}
	// The workflow leaf fields (workflow_stage, integration_branch,
	// agent.wait_for_ci, ci.release, ci.task, ci.task_merge) are governed by
	// the named-rule effective set; the configuration document only stores
	// them as seed provenance. A patch that would diverge them from rule
	// authority is rejected — leaf changes must go through rule/update.
	if in.Patch.Workflow != nil {
		effective, _, leafErr := s.ruleEffectiveSetShared(ctx, in.ProjectID)
		if leafErr != nil {
			return model.ProjectConfiguration{}, OperationResult{}, leafErr
		}
		governed, leafErr := workflowPolicyFromEffectiveRules(current, effective)
		if leafErr != nil {
			return model.ProjectConfiguration{}, OperationResult{}, leafErr
		}
		patched := *in.Patch.Workflow
		if patched.WorkflowStage != governed.WorkflowStage || patched.IntegrationBranch != governed.IntegrationBranch || patched.WaitForCI != governed.Agent.WaitForCI || patched.CI != governed.CI {
			return model.ProjectConfiguration{}, OperationResult{}, fmt.Errorf("workflow leaf fields are governed by durable rules; change them through rule/update")
		}
	}
	updated := current
	applyProjectConfigurationPatch(&updated, in.Patch)
	updated.Revision = current.Revision + 1
	updated.UpdatedBy = in.UpdatedBy
	updated.UpdatedAt = time.Now().UTC()
	if err := model.ValidateProjectConfiguration(updated); err != nil {
		return model.ProjectConfiguration{}, OperationResult{}, err
	}
	payload, err := json.Marshal(updated)
	if err != nil {
		return model.ProjectConfiguration{}, OperationResult{}, err
	}
	if _, err := s.Durability.CommitSharedMutation(ctx, sqlitestore.SharedMutation{
		OperationID: operationID, EntityType: "project_configuration", EntityID: in.ProjectID,
		ExpectedRevision: int64(current.Revision), Revision: int64(updated.Revision),
		Kind: "project-configuration-update", Payload: payload, CreatedAt: updated.UpdatedAt,
	}); err != nil {
		return model.ProjectConfiguration{}, OperationResult{}, err
	}
	return updated, OperationResult{
		OperationID: operationID,
		ProjectID:   in.ProjectID,
		Status:      "updated",
		Hub:         hub.TransactionResult{Paths: []string{}},
	}, nil
}

func projectConfigurationOperationID(ctx context.Context, in ProjectConfigurationUpdateInput) (string, error) {
	if operationID := durableMutationOperationID(ctx); operationID != "" {
		return operationID, nil
	}
	encoded, err := json.Marshal(in)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return "project-configuration-shared-" + hex.EncodeToString(digest[:]), nil
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

func validateProjectConfigurationUpdateInput(in ProjectConfigurationUpdateInput) error {
	if err := model.ValidateProjectIdentifier(in.ProjectID); err != nil {
		return err
	}
	if in.ExpectedRevision < 1 {
		return fmt.Errorf("expected_revision is required")
	}
	if in.UpdatedBy == "" || containsControl(in.UpdatedBy) {
		return fmt.Errorf("updated_by is required")
	}
	if in.Patch.AgentRouting == nil && in.Patch.Workflow == nil && in.Patch.GateCommands == nil && in.Patch.Checkpoint == nil && in.Patch.Integration == nil && in.Patch.GuideBindings == nil && in.Patch.Callbacks == nil && in.Patch.ActivationProfileRef == nil {
		return fmt.Errorf("project configuration patch is empty")
	}
	return nil
}

func projectConfigurationPatchIsExecutionSensitive(patch ProjectConfigurationPatch) bool {
	return patch.Workflow != nil || patch.Integration != nil || patch.ActivationProfileRef != nil
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
