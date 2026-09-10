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
	task, err := s.TaskAuthoringRead(ctx, in.ProjectID, in.Key)
	if err != nil {
		return TaskExecutionPublicOutput{}, err
	}
	if task.Status == model.TaskAuthoringArchived {
		return TaskExecutionPublicOutput{}, fmt.Errorf("archived Task is not dispatchable")
	}
	if task.Status != model.TaskAuthoringPlanned && task.Status != model.TaskAuthoringReady {
		return TaskExecutionPublicOutput{}, fmt.Errorf("Task is not dispatchable in status %q", task.Status)
	}
	taskHash, err := model.HashTaskAuthoring(task)
	if err != nil || taskHash != task.RevisionSHA256 {
		return TaskExecutionPublicOutput{}, fmt.Errorf("Task authoring revision hash is invalid")
	}
	s.taskExecutionMu.Lock()
	defer s.taskExecutionMu.Unlock()
	if existing, found, readErr := s.Durability.ReadTaskExecutionState(ctx, in.ProjectID, in.Key); readErr != nil {
		return TaskExecutionPublicOutput{}, readErr
	} else if found {
		if in.Agent != "" && existing.Agent != in.Agent {
			return TaskExecutionPublicOutput{}, fmt.Errorf("Task is already dispatched to logical Agent %q", existing.Agent)
		}
		return taskExecutionPublicOutput(existing), nil
	}
	agent, err := s.resolveTaskExecutionAgent(ctx, in.ProjectID, in.Agent)
	if err != nil {
		return TaskExecutionPublicOutput{}, err
	}
	project, err := s.EffectiveProjectConfig(in.ProjectID)
	if err != nil {
		return TaskExecutionPublicOutput{}, err
	}
	base, err := s.Git.RefreshDefaultBranch(ctx, project)
	if err != nil {
		return TaskExecutionPublicOutput{}, err
	}
	short := strings.ToLower(base[:8])
	lane, _, branch, err := s.Git.CreateTaskWorktree(ctx, project, s.Config.StateDir, in.ProjectID, in.Key, task.Type, task.Title, base)
	if err != nil {
		return TaskExecutionPublicOutput{}, fmt.Errorf("create Task worktree: %w", err)
	}
	actual, actualBranch, clean, err := s.Git.CurrentHead(ctx, lane)
	if err != nil || !clean || actualBranch != branch || actual != base {
		_ = s.Git.RemoveTaskWorktree(ctx, project, s.Config.StateDir, in.ProjectID, in.Key, task.Type, task.Title, base)
		return TaskExecutionPublicOutput{}, fmt.Errorf("created Task worktree failed identity validation")
	}
	worktree := taskExecutionWorktree(in.Key, short)
	now := time.Now().UTC()
	state := model.TaskExecutionState{TaskID: in.Key, ProjectID: in.ProjectID, TaskRevision: task.Revision, TaskRevisionSHA256: task.RevisionSHA256, Status: model.TaskExecutionDispatched, Stage: "code", Worktree: worktree, BaseHead: base, Head: actual, Branch: branch, Agent: agent, ExecutionRevision: 1, UpdatedAt: now}
	if err := s.Durability.CreateTaskExecutionState(ctx, state); err != nil {
		if existing, found, readErr := s.Durability.ReadTaskExecutionState(ctx, in.ProjectID, in.Key); readErr == nil && found {
			if in.Agent != "" && existing.Agent != in.Agent {
				return TaskExecutionPublicOutput{}, fmt.Errorf("concurrent Task dispatch selected logical Agent %q", existing.Agent)
			}
			return taskExecutionPublicOutput(existing), nil
		}
		_ = s.Git.RemoveTaskWorktree(ctx, project, s.Config.StateDir, in.ProjectID, in.Key, task.Type, task.Title, base)
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
	resolved, err := s.ResolveAgent(ctx, AgentResolveInput{
		ProjectID:       projectID,
		Role:            model.AgentRoleCoding,
		AgentID:         requested,
		RequireUnique:   requested == "",
		RequireAttached: true,
	})
	if err != nil {
		return "", err
	}
	return resolved.AgentID, nil
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
