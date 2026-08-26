package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

func (s *Service) agentPath(projectID, agentID string) string {
	if model.ValidateProjectIdentifier(projectID) != nil || model.ValidateObjectIdentifier(agentID) != nil {
		return "../invalid-agent"
	}
	return s.projectPrefix(projectID) + "/agents/" + agentID + ".json"
}

func (s *Service) requireAgentMutation(ctx context.Context) error {
	return RequireWorkflowPolicyAuthority(ctx)
}

func sharedAgentID(projectID, agentID string) string { return projectID + "\x00" + agentID }

func sharedAgentOperationID(ctx context.Context, kind, projectID, agentID string, value any) string {
	if operationID := durableMutationOperationID(ctx); operationID != "" {
		return operationID
	}
	payload, _ := json.Marshal(value)
	digest := sha256.Sum256(append([]byte(kind+"\x00"+projectID+"\x00"+agentID+"\x00"), payload...))
	return "agent-" + hex.EncodeToString(digest[:])
}

func (s *Service) AgentRead(ctx context.Context, projectID, agentID string) (model.Agent, error) {
	if err := model.ValidateProjectIdentifier(projectID); err != nil {
		return model.Agent{}, err
	}
	if err := model.ValidateObjectIdentifier(agentID); err != nil {
		return model.Agent{}, err
	}
	if _, err := s.EffectiveProjectConfig(projectID); err != nil {
		return model.Agent{}, err
	}
	if s.Durability == nil {
		return model.Agent{}, fmt.Errorf("Shared Agent authority is unavailable")
	}
	entity, err := s.Durability.ReadSharedEntity(ctx, "agent", sharedAgentID(projectID, agentID))
	if err != nil {
		return model.Agent{}, err
	}
	var agent model.Agent
	if err := json.Unmarshal(entity.Payload, &agent); err != nil {
		return model.Agent{}, err
	}
	if err := model.ValidateAgent(agent); err != nil || agent.ProjectID != projectID || agent.AgentID != agentID {
		return model.Agent{}, fmt.Errorf("invalid agent %q/%q", projectID, agentID)
	}
	return agent, nil
}

func (s *Service) AgentList(ctx context.Context, projectID string) ([]model.Agent, error) {
	if err := model.ValidateProjectIdentifier(projectID); err != nil {
		return nil, err
	}
	if _, err := s.EffectiveProjectConfig(projectID); err != nil {
		return nil, err
	}
	if s.Durability == nil {
		return nil, fmt.Errorf("Shared Agent authority is unavailable")
	}
	entities, err := s.Durability.ListSharedEntities(ctx, "agent", 1000)
	if err != nil {
		return nil, err
	}
	result := make([]model.Agent, 0, len(entities))
	for _, entity := range entities {
		if !strings.HasPrefix(entity.ID, projectID+"\x00") {
			continue
		}
		var agent model.Agent
		if err := json.Unmarshal(entity.Payload, &agent); err != nil {
			return nil, err
		}
		if err := model.ValidateAgent(agent); err != nil || agent.ProjectID != projectID {
			return nil, fmt.Errorf("invalid project agent record %q", entity.ID)
		}
		result = append(result, agent)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].AgentID < result[j].AgentID })
	return result, nil
}

func (s *Service) AgentRegister(ctx context.Context, in AgentRegisterInput) (model.Agent, OperationResult, error) {
	if err := s.requireAgentMutation(ctx); err != nil {
		return model.Agent{}, OperationResult{}, err
	}
	agent := in.Agent
	if err := model.ValidateProjectIdentifier(agent.ProjectID); err != nil {
		return model.Agent{}, OperationResult{}, err
	}
	if _, err := s.EffectiveProjectConfig(agent.ProjectID); err != nil {
		return model.Agent{}, OperationResult{}, err
	}
	now := time.Now().UTC()
	if agent.CreatedAt.IsZero() {
		agent.CreatedAt = now
	}
	if agent.UpdatedAt.IsZero() {
		agent.UpdatedAt = agent.CreatedAt
	}
	agent.Capabilities = model.NormalizeAgentCapabilities(agent.Capabilities)
	if err := model.ValidateAgent(agent); err != nil {
		return model.Agent{}, OperationResult{}, err
	}
	if s.Durability == nil {
		return model.Agent{}, OperationResult{}, fmt.Errorf("Shared Agent authority is unavailable")
	}
	operationID := sharedAgentOperationID(ctx, "agent-register", agent.ProjectID, agent.AgentID, agent)
	_, err := s.Durability.CommitSharedMutation(ctx, sharedMutationForAgent(operationID, agent, 0, 1, true, "agent-register"))
	if err != nil {
		return model.Agent{}, OperationResult{}, err
	}
	return agent, OperationResult{
		OperationID: operationID,
		ProjectID:   agent.ProjectID,
		Status:      "registered",
	}, nil
}

func sharedMutationForAgent(operationID string, agent model.Agent, expected, revision int64, create bool, kind string) sqlitestore.SharedMutation {
	payload, _ := json.Marshal(agent)
	return sqlitestore.SharedMutation{OperationID: operationID, EntityType: "agent", EntityID: sharedAgentID(agent.ProjectID, agent.AgentID), ExpectedRevision: expected, Revision: revision, Kind: kind, Payload: payload, CreatedAt: agent.UpdatedAt, Create: create}
}

func (s *Service) AgentUpdate(ctx context.Context, in AgentUpdateInput) (model.Agent, OperationResult, error) {
	if err := s.requireAgentMutation(ctx); err != nil {
		return model.Agent{}, OperationResult{}, err
	}
	if err := model.ValidateProjectIdentifier(in.ProjectID); err != nil {
		return model.Agent{}, OperationResult{}, err
	}
	if err := model.ValidateObjectIdentifier(in.AgentID); err != nil {
		return model.Agent{}, OperationResult{}, err
	}
	if strings.TrimSpace(in.UpdatedBy) == "" {
		return model.Agent{}, OperationResult{}, fmt.Errorf("updated_by is required")
	}
	if _, err := s.EffectiveProjectConfig(in.ProjectID); err != nil {
		return model.Agent{}, OperationResult{}, err
	}
	if s.Durability == nil {
		return model.Agent{}, OperationResult{}, fmt.Errorf("Shared Agent authority is unavailable")
	}
	currentEntity, err := s.Durability.ReadSharedEntity(ctx, "agent", sharedAgentID(in.ProjectID, in.AgentID))
	if err != nil {
		return model.Agent{}, OperationResult{}, err
	}
	var updated model.Agent
	if err := json.Unmarshal(currentEntity.Payload, &updated); err != nil {
		return model.Agent{}, OperationResult{}, err
	}
	if err := model.ValidateAgent(updated); err != nil || updated.ProjectID != in.ProjectID || updated.AgentID != in.AgentID {
		return model.Agent{}, OperationResult{}, fmt.Errorf("invalid existing agent")
	}
	if in.Enabled != nil {
		updated.Enabled = *in.Enabled
	}
	if in.Role != nil {
		updated.Role = *in.Role
	}
	if in.RecommendedReasoning != nil {
		updated.RecommendedReasoning = *in.RecommendedReasoning
	}
	if in.Capabilities != nil {
		updated.Capabilities = model.NormalizeAgentCapabilities(*in.Capabilities)
	}
	updatedAt := time.Now().UTC()
	if updatedAt.Before(updated.CreatedAt) {
		updatedAt = updated.CreatedAt
	}
	updated.UpdatedAt = updatedAt
	if err := model.ValidateAgent(updated); err != nil {
		return model.Agent{}, OperationResult{}, err
	}
	operationID := sharedAgentOperationID(ctx, "agent-update", in.ProjectID, in.AgentID, in)
	_, err = s.Durability.CommitSharedMutation(ctx, sharedMutationForAgent(operationID, updated, currentEntity.Revision, currentEntity.Revision+1, false, "agent-update"))
	if err != nil {
		return model.Agent{}, OperationResult{}, err
	}
	return updated, OperationResult{
		OperationID: operationID,
		ProjectID:   in.ProjectID,
		Status:      "updated",
	}, nil
}

func (s *Service) AgentDisable(ctx context.Context, in AgentDisableInput) (model.Agent, OperationResult, error) {
	disabled := false
	return s.AgentUpdate(ctx, AgentUpdateInput{
		ProjectID:    in.ProjectID,
		AgentID:      in.AgentID,
		Enabled:      &disabled,
		UpdatedBy:    in.UpdatedBy,
		WriteOptions: in.WriteOptions,
	})
}

func (s *Service) AgentRegistryStatus(ctx context.Context, projectID, agentID string) (model.AgentAvailabilityStatus, error) {
	agent, err := s.AgentRead(ctx, projectID, agentID)
	if err != nil {
		return model.AgentAvailabilityStatus{}, err
	}
	status := model.AgentAvailabilityStatus{
		SchemaVersion: model.AgentSchemaVersion,
		ProjectID:     projectID,
		AgentID:       agentID,
		Role:          agent.Role,
		Registered:    true,
		Enabled:       agent.Enabled,
		State:         "registered",
	}
	active, activeFound, activeErr := s.trainV2ActiveAttempt(ctx, projectID)
	if activeErr == nil && activeFound && active.Attempt.AgentID == agentID {
		status.AttemptState = active.Attempt.Status
		status.TrainID = active.Train.ID
		status.ItemPosition = active.Item.Position
		status.TaskID = active.Item.TaskID
		status.AttemptNumber = active.Attempt.Number
		status.Recoverable = active.Attempt.Status == model.TrainV2AttemptRunning
		status.RecoveryReason = "durable Train Attempt owns this Agent execution"
	}
	if !agent.Enabled {
		status.State, status.Reason = "disabled", "agent is disabled"
		return status, nil
	}
	agents := []model.Agent{agent}
	if _, explicit := s.Config.ResolveAgentBinding(projectID, agentID); !explicit && agent.Role == model.AgentRoleCoding {
		listed, listErr := s.AgentList(ctx, projectID)
		if listErr != nil {
			status.State, status.Reason = "unavailable", "agent registry could not resolve host-local binding"
			return status, nil
		}
		agents = listed
	}
	binding, ok := s.resolveLocalAgentBinding(projectID, agent, agents)
	if !ok {
		status.State, status.Reason = "unbound", "no host-local binding"
		return status, nil
	}
	if err := binding.Validate(); err != nil {
		status.State, status.Reason = "unavailable", "host-local binding is invalid"
		return status, nil
	}
	status.Bound = true
	probe, probeErr := s.Airelay.Status(ctx, binding.SessionKey)
	status.SessionState = probe.State
	if probeErr != nil || !probe.ControllerReachable || probe.State != "idle" {
		status.State = "unavailable"
		status.Reason = "host-local agent session is not usable"
		if probeErr != nil {
			status.Reason = "host-local agent session probe failed"
		}
		if status.Recoverable {
			status.RecoveryReason = "durable Attempt is recoverable without creating a parallel Agent"
		}
		return status, nil
	}
	status.State, status.Usable, status.Reason = "usable", true, "ready"
	return status, nil
}
