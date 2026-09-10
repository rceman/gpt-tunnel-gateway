package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

func (s *Service) TaskExecutionCurrent(ctx context.Context, projectID string) (TaskExecutionPublicOutput, error) {
	key, err := s.resolveTaskExecutionTaskForAgent(ctx, projectID)
	if err != nil {
		return TaskExecutionPublicOutput{}, err
	}
	return s.TaskExecutionStatus(ctx, projectID, key)
}

func (s *Service) TaskExecutionSubmitCodeForAgent(ctx context.Context, projectID string) (TaskExecutionPublicOutput, error) {
	key, err := s.resolveTaskExecutionTaskForAgent(ctx, projectID)
	if err != nil {
		return TaskExecutionPublicOutput{}, err
	}
	return s.TaskExecutionSubmitCode(ctx, projectID, key)
}

func (s *Service) TaskExecutionSubmitTestsForAgent(ctx context.Context, projectID string) (TaskExecutionPublicOutput, error) {
	key, err := s.resolveTaskExecutionTaskForAgent(ctx, projectID)
	if err != nil {
		return TaskExecutionPublicOutput{}, err
	}
	return s.TaskExecutionSubmitTests(ctx, projectID, key)
}

func (s *Service) TaskExecutionSubmitRebaseForAgent(ctx context.Context, projectID string) (TaskExecutionPublicOutput, error) {
	key, err := s.resolveTaskExecutionTaskForAgent(ctx, projectID)
	if err != nil {
		return TaskExecutionPublicOutput{}, err
	}
	return s.TaskExecutionSubmitRebase(ctx, projectID, key)
}

func (s *Service) resolveTaskExecutionTaskForAgent(ctx context.Context, projectID string) (string, error) {
	if s.Durability == nil {
		return "", fmt.Errorf("shared durability is unavailable")
	}
	sessionID := AgentSessionID(ctx)
	if !strings.HasPrefix(sessionID, durableSession.SessionIDPrefixAgent+"-") {
		return "", fmt.Errorf("Gateway Agent session authority is required")
	}
	agents, err := s.AgentList(ctx, projectID)
	if err != nil {
		return "", err
	}
	matched := make(map[string]struct{})
	for _, agent := range agents {
		if agent.Role != model.AgentRoleCoding || !agent.Enabled {
			continue
		}
		resolved, resolveErr := s.ResolveAgent(ctx, AgentResolveInput{
			ProjectID:       projectID,
			Role:            model.AgentRoleCoding,
			AgentID:         agent.AgentID,
			RequireAttached: true,
		})
		if resolveErr == nil && s.validateTaskExecutionAgentSession(ctx, projectID, resolved, sessionID) == nil {
			matched[agent.AgentID] = struct{}{}
		}
	}
	if len(matched) != 1 {
		return "", fmt.Errorf("Gateway Agent session is not uniquely bound to a coding Agent")
	}
	states, err := s.Durability.ListTaskExecutionStates(ctx, projectID)
	if err != nil {
		return "", err
	}
	var selected string
	for _, state := range states {
		if _, ok := matched[state.Agent]; !ok || !model.IsTaskExecutionNonTerminal(state.Status) {
			continue
		}
		if selected != "" {
			return "", fmt.Errorf("multiple current Tasks are assigned to this Agent")
		}
		selected = state.TaskID
	}
	if selected == "" {
		return "", fmt.Errorf("no current Task is assigned to this Agent")
	}
	return selected, nil
}
