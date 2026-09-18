package service

import (
	"context"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

// validateTaskDependencies is the Hub-path dependency admission: a dependency
// is satisfied when canonical evidence shows its implementation integrated —
// a terminal integrated/done Task execution, a completion lifecycle event,
// or the dependency Task's own done status.
func (s *Service) validateTaskDependencies(ctx context.Context, projectID string, task model.TaskAuthoring) error {
	for _, dependencyID := range task.Dependencies {
		integrated := false
		if s.Durability != nil {
			if state, found, err := s.Durability.ReadTaskExecutionState(ctx, projectID, dependencyID); err != nil {
				return err
			} else if found && (state.Status == model.TaskExecutionIntegrated || state.Status == model.TaskExecutionDone) {
				integrated = true
			}
			if !integrated {
				if _, found, err := s.Durability.ReadTaskCompletionEvent(ctx, projectID, dependencyID); err != nil {
					return err
				} else if found {
					integrated = true
				}
			}
		}
		if !integrated {
			dependency, err := s.TaskAuthoringRead(ctx, projectID, dependencyID)
			if err != nil {
				return fmt.Errorf("dependency-not-integrated: Task %q depends on %q: %w", task.ID, dependencyID, err)
			}
			integrated = dependency.Status == model.TaskAuthoringDone
		}
		if !integrated {
			return fmt.Errorf("dependency-not-integrated: Task %q depends on %q without canonical integrated implementation", task.ID, dependencyID)
		}
	}
	return nil
}

// validateTaskDependenciesInWorktree is the transact-time leg of dependency
// admission for the Hub path: the dependency Task must read done from the
// transact worktree.
func (s *Service) validateTaskDependenciesInWorktree(worktree, projectID string, tasks []model.TaskAuthoring) error {
	for _, task := range tasks {
		for _, dependencyID := range task.Dependencies {
			var dependency model.TaskAuthoring
			if err := readWorktreeJSON(worktree, s.taskAuthoringPath(projectID, dependencyID), &dependency); err != nil {
				return fmt.Errorf("dependency-not-integrated: Task %q depends on %q: %w", task.ID, dependencyID, err)
			}
			if dependency.Status != model.TaskAuthoringDone {
				return fmt.Errorf("dependency-not-integrated: Task %q depends on %q without canonical integrated implementation", task.ID, dependencyID)
			}
		}
	}
	return nil
}
