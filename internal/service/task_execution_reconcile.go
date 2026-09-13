package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/gitx"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

// reconcileTaskExecutionBase rebases the server-owned Task lane onto the
// refreshed canonical base. A conflict leaves the lane mid-rebase with the
// durable conflict state recording the exact canonical target; the owning
// Agent resolves the conflicted bytes, continues the in-progress rebase, and
// submits the rebase stage for Planner review. Other failures restore the
// original lane exactly. The resulting rebase request is durable, so an Agent
// can resume by submitting the opened rebase stage; callers never select a
// target or strategy.
func (s *Service) reconcileTaskExecutionBase(ctx context.Context, state model.TaskExecutionState, project config.ProjectConfig, canonical string) error {
	path, err := gitx.TaskWorktreePath(s.Config.StateDir, state.ProjectID, state.TaskID)
	if err != nil {
		return err
	}
	lane := project
	lane.Root = path
	base, err := s.Git.MergeBaseInWorktree(ctx, path, state.Head, canonical)
	if err != nil || base == "" {
		if err != nil {
			return fmt.Errorf("read Task lane divergence for rebase: %w", err)
		}
		return fmt.Errorf("Task lane has no common history with the refreshed canonical")
	}
	newHead, err := s.Git.ReconcileTaskLane(ctx, lane, canonical, base)
	if err != nil {
		state.Stage = "rebase"
		state.Status = model.TaskExecutionChangesRequested
		state.BaseHead = canonical
		state.ExecutionRevision++
		state.UpdatedAt = time.Now().UTC()
		var conflict *gitx.TaskReconcileConflictError
		var comment string
		if errors.As(err, &conflict) {
			comment = fmt.Sprintf("controlled rebase conflict replaying %s onto canonical %s; owning Agent resolves and Planner review is required", conflict.Commit[:8], conflict.Target[:8])
		} else {
			comment = fmt.Sprintf("controlled rebase onto canonical %s failed; Planner authorization required", canonical[:8])
		}
		phase := sqlitestore.TaskExecutionPhase{TaskID: state.TaskID, ProjectID: state.ProjectID, ExecutionRevision: state.ExecutionRevision, Stage: state.Stage, Status: state.Status, Head: state.Head, Branch: state.Branch, TaskRevisionSHA256: state.TaskRevisionSHA256, EventKind: "rework", Comment: comment, CreatedAt: state.UpdatedAt}
		if persistErr := s.Durability.TransitionTaskExecutionState(ctx, state, state.ExecutionRevision-1, phase); persistErr != nil {
			return fmt.Errorf("rebase failure and recovery state could not be recorded: %w (original: %v)", persistErr, err)
		}
		return fmt.Errorf("controlled Task rebase could not complete; durable rebase state recorded: %w", err)
	}
	state.BaseHead = canonical
	state.Head = newHead
	state.Stage = "rebase"
	state.Status = model.TaskExecutionChangesRequested
	state.Worktree = taskExecutionWorktree(state.TaskID, newHead[:8])
	state.ExecutionRevision++
	state.UpdatedAt = time.Now().UTC()
	phase := sqlitestore.TaskExecutionPhase{
		TaskID: state.TaskID, ProjectID: state.ProjectID, ExecutionRevision: state.ExecutionRevision,
		Stage: state.Stage, Status: state.Status, Head: state.Head, Branch: state.Branch,
		TaskRevisionSHA256: state.TaskRevisionSHA256, EventKind: "rework", Comment: "canonical main advanced; controlled rebase required", CreatedAt: state.UpdatedAt,
	}
	if err := s.Durability.TransitionTaskExecutionState(ctx, state, state.ExecutionRevision-1, phase); err != nil {
		return fmt.Errorf("persist controlled Task rebase state: %w", err)
	}
	return nil
}
