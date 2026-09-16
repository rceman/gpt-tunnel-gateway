package service

import (
	"context"
	"sort"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s *Service) projectHasExplicitAgentBinding(ctx context.Context, projectID string) bool {
	if s.hasProjectAgentBinding(projectID) {
		return true
	}
	agents, err := s.AgentList(ctx, projectID)
	if err != nil {
		return false
	}
	for _, agent := range agents {
		if _, ok := s.agentBinding(projectID, agent.AgentID); ok {
			return true
		}
	}
	return false
}

func (s *Service) populateProjectOperationalTask(result *ProjectOperationalStatus, states []model.TaskExecutionState, agentID string) {
	candidates := append([]model.TaskExecutionState{}, states...)
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].UpdatedAt.After(candidates[j].UpdatedAt) })
	for _, state := range candidates {
		if state.Agent != agentID || !model.IsTaskExecutionAgentActionable(state.Status, state.Stage) {
			continue
		}
		result.TaskID = state.TaskID
		result.TaskState = state.Status
		if result.State == "idle" {
			result.State = "working"
			result.RecommendedNextAction = "supervise current Worker Task"
		}
		return
	}
}
