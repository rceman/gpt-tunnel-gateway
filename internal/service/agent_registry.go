package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
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

func (s *Service) AgentRead(ctx context.Context, projectID, agentID string) (model.Agent, error) {
	if err := model.ValidateProjectIdentifier(projectID); err != nil {
		return model.Agent{}, err
	}
	if err := model.ValidateObjectIdentifier(agentID); err != nil {
		return model.Agent{}, err
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
		if err := model.ValidateAgent(agent); err != nil || agent.ProjectID != projectID {
			return nil, fmt.Errorf("invalid project agent record %q", path)
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
	if _, err := s.ProjectRead(ctx, agent.ProjectID); err != nil {
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
	path := s.agentPath(agent.ProjectID, agent.AgentID)
	tx, err := s.Hub.Transact(ctx, in.ExpectedHubRevision, "gateway: register agent "+agent.ProjectID+"/"+agent.AgentID, func(worktree string) ([]string, error) {
		var existing model.Agent
		if err := readWorktreeJSON(worktree, path, &existing); err == nil {
			return nil, fmt.Errorf("agent %q/%q already exists", agent.ProjectID, agent.AgentID)
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, path, agent); err != nil {
			return nil, err
		}
		return []string{path}, nil
	})
	if err != nil {
		return model.Agent{}, OperationResult{}, err
	}
	return agent, OperationResult{
		Hub:       tx,
		ProjectID: agent.ProjectID,
		Status:    "registered",
	}, nil
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
	if _, err := s.ProjectRead(ctx, in.ProjectID); err != nil {
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
		if err := hub.WriteJSON(worktree, path, updated); err != nil {
			return nil, err
		}
		return []string{path}, nil
	})
	if err != nil {
		return model.Agent{}, OperationResult{}, err
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
	if !agent.Enabled {
		status.State, status.Reason = "disabled", "agent is disabled"
		return status, nil
	}
	binding, ok := s.Config.ResolveAgentBinding(projectID, agentID)
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
	if probeErr != nil || !probe.ControllerReachable || probe.State != "idle" {
		status.State = "unavailable"
		status.Reason = "host-local agent session is not usable"
		if probeErr != nil {
			status.Reason = "host-local agent session probe failed"
		}
		return status, nil
	}
	status.State, status.Usable, status.Reason = "usable", true, "ready"
	return status, nil
}
