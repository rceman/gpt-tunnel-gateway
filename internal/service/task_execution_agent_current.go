package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/gitx"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

var errNoCurrentTask = errors.New("no current Task is assigned to this Worker")

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
	if strings.TrimSpace(projectID) != projectID || model.ValidateProjectIdentifier(projectID) != nil {
		return "", fmt.Errorf("invalid project authority")
	}
	sessionID := AgentSessionID(ctx)
	worker, err := s.ResolveWorkerSession(ctx, projectID, sessionID)
	if err != nil {
		return "", err
	}
	states, err := s.Durability.ListTaskExecutionStates(ctx, projectID)
	if err != nil {
		return "", err
	}
	var selected string
	for _, state := range states {
		if state.Agent != worker.Agent.AgentID || !model.IsTaskExecutionAgentActionable(state.Status, state.Stage) {
			continue
		}
		if _, laneErr := s.taskExecutionLane(projectID, state.TaskID, state); laneErr != nil {
			return "", fmt.Errorf("current Task lane is unavailable: %w", laneErr)
		}
		if selected != "" {
			return "", fmt.Errorf("multiple current Tasks are assigned to this Worker")
		}
		selected = state.TaskID
	}
	if selected == "" {
		return "", errNoCurrentTask
	}
	return selected, nil
}

func (s *Service) taskExecutionLane(projectID, key string, state model.TaskExecutionState) (config.ProjectConfig, error) {
	if state.ProjectID != projectID || state.TaskID != key {
		return config.ProjectConfig{}, fmt.Errorf("Task lane authority does not match the requested project and Task")
	}
	if model.ValidateCommitSHA(state.Head) != nil || model.ValidateBranch(state.Branch) != nil || state.Worktree != taskExecutionWorktree(key, strings.ToLower(state.Head[:8])) {
		return config.ProjectConfig{}, fmt.Errorf("Task lane authority is invalid or stale")
	}
	path, err := gitx.TaskWorktreePath(s.Config.StateDir, projectID, key)
	if err != nil {
		return config.ProjectConfig{}, err
	}
	project, err := s.EffectiveProjectConfig(projectID)
	if err != nil {
		return config.ProjectConfig{}, err
	}
	project.Root = path
	return project, nil
}

func (s *Service) resolveWorkerSessionForTask(ctx context.Context, projectID string) (RuntimeRoleSession, error) {
	sessionID := AgentSessionID(ctx)
	if sessionID == "" {
		return RuntimeRoleSession{}, fmt.Errorf("Task requires an active Worker Session")
	}
	worker, err := s.ResolveProjectWorker(ctx, projectID)
	if err != nil {
		return RuntimeRoleSession{}, err
	}
	if worker.Session.ID != sessionID {
		return RuntimeRoleSession{}, fmt.Errorf("RUNTIME_SESSION_UNAVAILABLE: Worker attachment is stale or mismatched")
	}
	return worker, nil
}

func isWorkerSession(record durableSession.Record) bool {
	return record.Status == durableSession.StatusActive && record.Role == durableSession.RoleWorker && record.SessionRef != nil && strings.TrimSpace(*record.SessionRef) == *record.SessionRef && *record.SessionRef != ""
}
