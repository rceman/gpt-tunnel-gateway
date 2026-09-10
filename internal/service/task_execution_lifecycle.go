package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

type TaskExecutionDispatchInput struct {
	ProjectID string
	Key       string
	Agent     string
}

type TaskExecutionPublicOutput struct {
	Key               string `json:"key"`
	Status            string `json:"status"`
	Stage             string `json:"stage"`
	Worktree          string `json:"worktree"`
	Head              string `json:"head"`
	Agent             string `json:"agent"`
	ExecutionRevision int    `json:"execution_revision"`
	UpdatedAt         string `json:"updated_at,omitempty"`
}

func (s *Service) TaskExecutionDispatch(ctx context.Context, in TaskExecutionDispatchInput) (TaskExecutionPublicOutput, error) {
	if s.Durability == nil {
		return TaskExecutionPublicOutput{}, fmt.Errorf("shared durability is unavailable")
	}
	if err := model.ValidateProjectIdentifier(in.ProjectID); err != nil {
		return TaskExecutionPublicOutput{}, err
	}
	if err := model.ValidateCanonicalTaskID(in.Key); err != nil {
		return TaskExecutionPublicOutput{}, err
	}
	if _, err := s.TaskAuthoringRead(ctx, in.ProjectID, in.Key); err != nil {
		return TaskExecutionPublicOutput{}, err
	}
	agent, err := s.resolveTaskExecutionAgent(ctx, in.ProjectID, in.Agent)
	if err != nil {
		return TaskExecutionPublicOutput{}, err
	}
	project, err := s.EffectiveProjectConfig(in.ProjectID)
	if err != nil {
		return TaskExecutionPublicOutput{}, err
	}
	head, branch, clean, err := s.Git.CurrentHead(ctx, project)
	if err != nil || !clean || branch == "" || model.ValidateCommitSHA(head) != nil {
		return TaskExecutionPublicOutput{}, fmt.Errorf("Task execution requires a clean committed project head")
	}
	short := strings.ToLower(head[:8])
	worktree := taskExecutionWorktree(in.Key, short)
	now := time.Now().UTC()
	state := model.TaskExecutionState{TaskID: in.Key, ProjectID: in.ProjectID, Status: model.TaskExecutionDispatched, Stage: "dispatch", Worktree: worktree, Head: head, Agent: agent, ExecutionRevision: 1, UpdatedAt: now}
	if existing, found, readErr := s.Durability.ReadTaskExecutionState(ctx, in.ProjectID, in.Key); readErr != nil {
		return TaskExecutionPublicOutput{}, readErr
	} else if found {
		if existing.Agent != agent && in.Agent != "" {
			return TaskExecutionPublicOutput{}, fmt.Errorf("Task is already dispatched to logical Agent %q", existing.Agent)
		}
		return taskExecutionPublicOutput(existing), nil
	}
	if err := s.Durability.CreateTaskExecutionState(ctx, state); err != nil {
		if existing, found, readErr := s.Durability.ReadTaskExecutionState(ctx, in.ProjectID, in.Key); readErr == nil && found {
			return taskExecutionPublicOutput(existing), nil
		}
		return TaskExecutionPublicOutput{}, err
	}
	return taskExecutionPublicOutput(state), nil
}

func (s *Service) TaskExecutionStatus(ctx context.Context, projectID, key string) (TaskExecutionPublicOutput, error) {
	if s.Durability == nil {
		return TaskExecutionPublicOutput{}, fmt.Errorf("shared durability is unavailable")
	}
	if err := model.ValidateProjectIdentifier(projectID); err != nil {
		return TaskExecutionPublicOutput{}, err
	}
	if err := model.ValidateCanonicalTaskID(key); err != nil {
		return TaskExecutionPublicOutput{}, err
	}
	if _, err := s.TaskAuthoringRead(ctx, projectID, key); err != nil {
		return TaskExecutionPublicOutput{}, err
	}
	if state, found, err := s.Durability.ReadTaskExecutionState(ctx, projectID, key); err != nil {
		return TaskExecutionPublicOutput{}, err
	} else if found {
		return taskExecutionPublicOutput(state), nil
	}
	return TaskExecutionPublicOutput{
		Key:    key,
		Status: model.TaskExecutionPlanned,
	}, nil
}

func (s *Service) resolveTaskExecutionAgent(ctx context.Context, projectID, requested string) (string, error) {
	agents, err := s.AgentList(ctx, projectID)
	if err != nil {
		return "", err
	}
	eligible := make([]string, 0, len(agents))
	for _, agent := range agents {
		if agent.Role == model.AgentRoleCoding && agent.Enabled {
			eligible = append(eligible, agent.AgentID)
		}
	}
	if requested != "" {
		for _, agent := range eligible {
			if agent == requested {
				return agent, nil
			}
		}
		return "", fmt.Errorf("AGENT_NOT_AVAILABLE: logical Agent %q is not eligible", requested)
	}
	if len(eligible) == 0 {
		return "", fmt.Errorf("AGENT_NOT_AVAILABLE: no eligible enabled coding Agent exists")
	}
	if len(eligible) > 1 {
		return "", fmt.Errorf("multiple eligible coding Agents exist; explicit agent is required")
	}
	return eligible[0], nil
}

func taskExecutionWorktree(key, head string) string {
	return "WT-TSK" + key[strings.LastIndex(key, "-TSK")+4:] + "-" + head
}

func taskExecutionPublicOutput(state model.TaskExecutionState) TaskExecutionPublicOutput {
	head := ""
	if len(state.Head) >= 8 {
		head = strings.ToLower(state.Head[:8])
	}
	return TaskExecutionPublicOutput{
		Key:               state.TaskID,
		Status:            state.Status,
		Stage:             state.Stage,
		Worktree:          state.Worktree,
		Head:              head,
		Agent:             state.Agent,
		ExecutionRevision: state.ExecutionRevision,
		UpdatedAt:         state.UpdatedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"),
	}
}
