package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

func (s *Service) agentPath(projectID, agentID string) string {
	if model.ValidateProjectIdentifier(projectID) != nil || model.ValidateObjectIdentifier(agentID) != nil {
		return "../invalid-agent"
	}
	return s.projectPrefix(projectID) + "/agents/" + agentID + ".json"
}

func (s *Service) AgentRead(ctx context.Context, projectID, agentID string) (model.Agent, error) {
	if err := model.ValidateProjectIdentifier(projectID); err != nil {
		return model.Agent{}, err
	}
	if err := model.ValidateObjectIdentifier(agentID); err != nil {
		return model.Agent{}, err
	}
	if s.Durability != nil {
		if _, err := s.projectConfig(projectID); err != nil {
			return model.Agent{}, err
		}
		return s.readLocalAgent(ctx, projectID, agentID)
	}
	if _, err := s.ProjectRead(ctx, projectID); err != nil {
		return model.Agent{}, err
	}
	var agent model.Agent
	if err := s.Hub.ReadJSON(ctx, s.agentPath(projectID, agentID), &agent); err != nil {
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
	if s.Durability != nil {
		if _, err := s.projectConfig(projectID); err != nil {
			return nil, err
		}
		return s.listLocalAgents(ctx, projectID)
	}
	if _, err := s.ProjectRead(ctx, projectID); err != nil {
		return nil, err
	}
	paths, err := s.Hub.List(ctx, s.projectPrefix(projectID)+"/agents", ".json")
	if err != nil {
		return nil, err
	}
	result := make([]model.Agent, 0, len(paths))
	for _, path := range paths {
		var agent model.Agent
		if err := s.Hub.ReadJSON(ctx, path, &agent); err != nil {
			return nil, err
		}
		if agent.Role != model.AgentRoleCoding {
			continue
		}
		if err := model.ValidateAgent(agent); err != nil || agent.ProjectID != projectID {
			return nil, fmt.Errorf("invalid project agent record %q", path)
		}
		result = append(result, agent)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].AgentID < result[j].AgentID })
	return result, nil
}

func (s *Service) readLocalAgent(ctx context.Context, projectID, agentID string) (model.Agent, error) {
	record, err := s.Durability.ReadLocalAgent(ctx, projectID, agentID)
	if err != nil {
		return model.Agent{}, err
	}
	var agent model.Agent
	if err := json.Unmarshal(record.Payload, &agent); err != nil {
		return model.Agent{}, fmt.Errorf("decode local agent %q/%q: %w", projectID, agentID, err)
	}
	if err := model.ValidateAgent(agent); err != nil || agent.ProjectID != projectID || agent.AgentID != agentID {
		return model.Agent{}, fmt.Errorf("invalid local agent %q/%q", projectID, agentID)
	}
	return agent, nil
}

func (s *Service) listLocalAgents(ctx context.Context, projectID string) ([]model.Agent, error) {
	limit := s.Config.MaxListItems
	if limit < 1 {
		return nil, fmt.Errorf("invalid configured Agent list limit")
	}
	records, err := s.Durability.ListLocalAgents(ctx, projectID, limit)
	if err != nil {
		return nil, err
	}
	result := make([]model.Agent, 0, len(records))
	for _, record := range records {
		var agent model.Agent
		if err := json.Unmarshal(record.Payload, &agent); err != nil {
			return nil, fmt.Errorf("decode local agent %q/%q: %w", projectID, record.AgentID, err)
		}
		if agent.Role != model.AgentRoleCoding {
			continue
		}
		if err := model.ValidateAgent(agent); err != nil || agent.ProjectID != projectID || agent.AgentID != record.AgentID {
			return nil, fmt.Errorf("invalid local agent %q/%q", projectID, record.AgentID)
		}
		result = append(result, agent)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].AgentID < result[j].AgentID })
	return result, nil
}

func (s *Service) AgentUpdate(ctx context.Context, in AgentUpdateInput) (model.Agent, OperationResult, error) {
	if err := model.ValidateProjectIdentifier(in.ProjectID); err != nil {
		return model.Agent{}, OperationResult{}, err
	}
	if err := model.ValidateObjectIdentifier(in.AgentID); err != nil {
		return model.Agent{}, OperationResult{}, err
	}
	if strings.TrimSpace(in.UpdatedBy) == "" {
		return model.Agent{}, OperationResult{}, fmt.Errorf("updated_by is required")
	}
	if _, err := s.ProjectRead(ctx, in.ProjectID); err != nil {
		return model.Agent{}, OperationResult{}, err
	}
	existing, err := s.AgentRead(ctx, in.ProjectID, in.AgentID)
	if err != nil {
		return model.Agent{}, OperationResult{}, err
	}
	candidate := existing
	if in.Enabled != nil {
		candidate.Enabled = *in.Enabled
	}
	if in.Role != nil {
		candidate.Role = *in.Role
	}
	if in.RecommendedReasoning != nil {
		candidate.RecommendedReasoning = *in.RecommendedReasoning
	}
	if in.Capabilities != nil {
		candidate.Capabilities = model.NormalizeAgentCapabilities(*in.Capabilities)
	}
	if err := model.ValidateAgent(candidate); err != nil {
		return model.Agent{}, OperationResult{}, err
	}
	if err := s.validateManagedRuntimeBindingCollision(ctx, candidate); err != nil {
		return model.Agent{}, OperationResult{}, err
	}
	path := s.agentPath(in.ProjectID, in.AgentID)
	var updated model.Agent
	tx, err := s.Hub.Transact(ctx, in.ExpectedHubRevision, "gateway: update agent "+in.ProjectID+"/"+in.AgentID, func(worktree string) ([]string, error) {
		if err := readWorktreeJSON(worktree, path, &updated); err != nil {
			return nil, err
		}
		if err := model.ValidateAgent(updated); err != nil || updated.ProjectID != in.ProjectID || updated.AgentID != in.AgentID {
			return nil, fmt.Errorf("invalid existing agent")
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
			return nil, err
		}
		agentPaths, err := listWorktreeAgents(worktree, s.projectPrefix(in.ProjectID))
		if err != nil {
			return nil, err
		}
		existingAgents := make([]model.Agent, 0, len(agentPaths))
		for _, existingPath := range agentPaths {
			var existing model.Agent
			if err := readWorktreeJSON(worktree, existingPath, &existing); err != nil {
				return nil, err
			}
			if err := model.ValidateAgent(existing); err != nil || existing.ProjectID != in.ProjectID {
				return nil, fmt.Errorf("invalid existing Agent record %q", existingPath)
			}
			existingAgents = append(existingAgents, existing)
		}
		if err := s.validateManagedRuntimeBindingAgainst(updated, existingAgents); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, path, updated); err != nil {
			return nil, err
		}
		return []string{path}, nil
	})
	if err != nil {
		return model.Agent{}, OperationResult{}, err
	}
	if s.Durability != nil {
		payload, marshalErr := json.Marshal(updated)
		if marshalErr != nil {
			return model.Agent{}, OperationResult{}, fmt.Errorf("encode local Agent projection: %w", marshalErr)
		}
		if localErr := s.Durability.UpsertLocalAgent(ctx, sqlitestore.LocalAgent{
			ProjectID: in.ProjectID,
			AgentID:   updated.AgentID,
			Payload:   payload,
			UpdatedAt: updated.UpdatedAt.UTC().Format(time.RFC3339Nano),
		}); localErr != nil {
			return model.Agent{}, OperationResult{}, fmt.Errorf("update local Agent projection: %w", localErr)
		}
	}
	return updated, OperationResult{
		Hub:       tx,
		ProjectID: in.ProjectID,
		Status:    "updated",
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
	if sessionID := AgentSessionID(ctx); sessionID != "" {
		record, sessionErr := durableSession.NewStoreWithDurability(s.Durability).Get(sessionID)
		if sessionErr == nil && isWorkerSession(record) {
			return s.workerAgentRegistryStatus(ctx, projectID, agent, status)
		}
	}
	if s.Durability != nil {
		if states, statesErr := s.Durability.ListTaskExecutionStates(ctx, projectID); statesErr == nil {
			for _, state := range states {
				if state.Agent == agentID && model.IsTaskExecutionAgentOwned(state.Status) {
					status.TaskID = state.TaskID
					status.Recoverable = true
					status.RecoveryReason = "durable Task execution owns this Agent"
					break
				}
			}
		}
	}
	if !agent.Enabled {
		status.State, status.Reason = "disabled", "agent is disabled"
		return status, nil
	}
	binding, ok := s.resolveExplicitLocalAgentBinding(projectID, agent)
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

func (s *Service) workerAgentRegistryStatus(ctx context.Context, projectID string, agent model.Agent, status model.AgentAvailabilityStatus) (model.AgentAvailabilityStatus, error) {
	if !agent.Enabled {
		status.State, status.Reason = "disabled", "agent is disabled"
		return status, nil
	}
	worker, err := s.ResolveProjectWorker(ctx, projectID)
	if err != nil {
		status.State, status.Reason = "unavailable", err.Error()
		return status, nil
	}
	if worker.Agent.AgentID != agent.AgentID || worker.Session.ID != AgentSessionID(ctx) {
		status.State, status.Reason = "unavailable", "Agent is not the explicitly attached Worker"
		return status, nil
	}
	status.Bound = true
	status.SessionState = worker.RuntimeState
	status.State, status.Usable, status.Reason = "usable", true, "ready"
	states, statesErr := s.Durability.ListTaskExecutionStates(ctx, projectID)
	if statesErr != nil {
		return status, nil
	}
	for _, state := range states {
		if state.Agent == agent.AgentID && model.IsTaskExecutionAgentOwned(state.Status) {
			status.TaskID = state.TaskID
			break
		}
	}
	return status, nil
}
