package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
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
	var configuration model.ProjectConfiguration
	if err := json.Unmarshal(entity.Payload, &configuration); err != nil {
		return model.ProjectConfiguration{}, fmt.Errorf("decode Shared project configuration %s: %w", projectID, err)
	}
	if configuration.ProjectID != projectID || int64(configuration.Revision) != entity.Revision {
		return model.ProjectConfiguration{}, fmt.Errorf("Shared project configuration identity mismatch")
	}
	normalizeProjectConfiguration(&configuration)
	if err := model.ValidateProjectConfiguration(configuration); err != nil {
		return model.ProjectConfiguration{}, err
	}
	return configuration, nil
}

func normalizeProjectConfiguration(configuration *model.ProjectConfiguration) {
	if configuration.Workflow.GateCommands.IsZero() {
		configuration.Workflow.GateCommands = model.DefaultProjectGateCommands()
	}
	if configuration.Integration.TargetBranch == "" {
		configuration.Integration.TargetBranch = configuration.Workflow.IntegrationBranch
	}
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
		var committed model.ProjectConfiguration
		if err := json.Unmarshal(existing.Payload, &committed); err != nil {
			return model.ProjectConfiguration{}, OperationResult{}, fmt.Errorf("decode committed Shared project configuration %s: %w", in.ProjectID, err)
		}
		normalizeProjectConfiguration(&committed)
		if committed.ProjectID != in.ProjectID || int64(committed.Revision) != existing.Revision {
			return model.ProjectConfiguration{}, OperationResult{}, fmt.Errorf("committed Shared project configuration identity mismatch")
		}
		if err := model.ValidateProjectConfiguration(committed); err != nil {
			return model.ProjectConfiguration{}, OperationResult{}, err
		}
		return committed, OperationResult{OperationID: operationID, ProjectID: in.ProjectID, Status: "updated", Hub: hub.TransactionResult{Paths: []string{}}}, nil
	}
	current, err := s.projectConfigurationReadShared(ctx, in.ProjectID)
	if err != nil {
		return model.ProjectConfiguration{}, OperationResult{}, err
	}
	if current.Revision != in.ExpectedRevision {
		return model.ProjectConfiguration{}, OperationResult{}, fmt.Errorf("project configuration revision conflict: expected %d, current %d", in.ExpectedRevision, current.Revision)
	}
	active, err := s.projectHasActiveTrainAttempt(ctx, in.ProjectID)
	if err != nil {
		return model.ProjectConfiguration{}, OperationResult{}, fmt.Errorf("inspect active Train Attempt: %w", err)
	}
	if active && projectConfigurationPatchIsExecutionSensitive(in.Patch) {
		return model.ProjectConfiguration{}, OperationResult{}, fmt.Errorf("execution-sensitive project configuration cannot change while an active Train Attempt exists")
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
	return updated, OperationResult{OperationID: operationID, ProjectID: in.ProjectID, Status: "updated", Hub: hub.TransactionResult{Paths: []string{}}}, nil
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
	if _, ok := s.Config.Projects[projectID]; !ok {
		return fmt.Errorf("project %q is not configured locally", projectID)
	}
	complete, err := s.Durability.SharedBootstrapComplete(ctx, projectID)
	if err != nil {
		return fmt.Errorf("read Shared bootstrap marker: %w", err)
	}
	if !complete {
		return fmt.Errorf("Shared bootstrap is incomplete for project %q", projectID)
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
	if in.Patch.AgentRouting == nil && in.Patch.Watcher == nil && in.Patch.Workflow == nil && in.Patch.GateCommands == nil && in.Patch.Checkpoint == nil && in.Patch.Integration == nil && in.Patch.ActivationProfileRef == nil {
		return fmt.Errorf("project configuration patch is empty")
	}
	return nil
}

func projectConfigurationPatchIsExecutionSensitive(patch ProjectConfigurationPatch) bool {
	return patch.Workflow != nil || patch.Integration != nil || patch.ActivationProfileRef != nil
}
