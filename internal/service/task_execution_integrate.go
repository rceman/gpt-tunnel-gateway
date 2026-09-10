package service

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/rceman/gpt-tunnel-gateway/internal/gitx"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

type TaskExecutionIntegrateInput struct {
	ProjectID string
	Key       string
	Comment   string
}

func (s *Service) TaskExecutionIntegrate(ctx context.Context, in TaskExecutionIntegrateInput) (TaskExecutionPublicOutput, error) {
	if err := validateTaskExecutionReviewInput(in.ProjectID, in.Key, "code"); err != nil {
		return TaskExecutionPublicOutput{}, err
	}
	s.taskExecutionMu.Lock()
	defer s.taskExecutionMu.Unlock()
	state, found, err := s.readExecutionForMutation(ctx, in.ProjectID, in.Key)
	if err != nil || !found {
		if err != nil {
			return TaskExecutionPublicOutput{}, err
		}
		return TaskExecutionPublicOutput{}, fmt.Errorf("Task has no execution state")
	}
	if state.Status != model.TaskExecutionReadyForIntegration {
		if state.Status == model.TaskExecutionIntegrated {
			return taskExecutionPublicOutput(state), nil
		}
		return TaskExecutionPublicOutput{}, fmt.Errorf("Task is not ready for integration")
	}
	accepted, found, err := s.Durability.ReadLatestAcceptedTaskExecutionPhase(ctx, in.ProjectID, in.Key, state.Stage)
	if err != nil || !found || accepted.Head != state.Head || accepted.TaskRevisionSHA256 != state.TaskRevisionSHA256 {
		return TaskExecutionPublicOutput{}, fmt.Errorf("Task integration review authority is stale")
	}
	task, err := s.TaskAuthoringRead(ctx, in.ProjectID, in.Key)
	if err != nil {
		return TaskExecutionPublicOutput{}, err
	}
	project, err := s.EffectiveProjectConfig(in.ProjectID)
	if err != nil {
		return TaskExecutionPublicOutput{}, err
	}
	canonical, err := s.Git.RefreshDefaultBranch(ctx, project)
	if err != nil {
		return TaskExecutionPublicOutput{}, err
	}
	if canonical != state.BaseHead {
		return TaskExecutionPublicOutput{}, fmt.Errorf("canonical default branch advanced beyond Task base; rebase is required")
	}
	if _, err := s.Git.SynchronizeDefaultBranchWorktree(ctx, project, canonical); err != nil {
		return TaskExecutionPublicOutput{}, err
	}
	lanePath, err := gitx.TaskWorktreePath(s.Config.StateDir, in.ProjectID, in.Key)
	if err != nil {
		return TaskExecutionPublicOutput{}, err
	}
	lane := project
	lane.Root = lanePath
	message := "Task " + task.ID + ": " + task.Title
	comment := strings.TrimSpace(in.Comment)
	if utf8.RuneCountInString(comment) > 1024 {
		return TaskExecutionPublicOutput{}, fmt.Errorf("integration comment is too long")
	}
	if comment != "" {
		message += " - " + comment
	}
	_, err = s.Git.SquashTaskIntoDefaultBranch(ctx, project, lane, state.BaseHead, state.Head, message)
	if err != nil {
		return TaskExecutionPublicOutput{}, err
	}
	state.Status = model.TaskExecutionIntegrated
	state.ExecutionRevision++
	state.UpdatedAt = s.durableNow()
	if err := s.Durability.UpdateTaskExecutionState(ctx, state, state.ExecutionRevision-1); err != nil {
		return TaskExecutionPublicOutput{}, err
	}
	if err := s.Git.RemoveTaskWorktreeAfterIntegration(ctx, project, s.Config.StateDir, in.ProjectID, in.Key, task.Type, task.Title, state.Head, state.Branch); err != nil {
		return TaskExecutionPublicOutput{}, fmt.Errorf("Task integrated but worktree cleanup failed: %w", err)
	}
	return taskExecutionPublicOutput(state), nil
}
