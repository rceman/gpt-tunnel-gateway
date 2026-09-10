package service

import (
	"context"
	"fmt"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/gitx"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

// reconcileTaskExecutionBase replays only the immutable commits in the
// server-owned Task lane onto the refreshed canonical base. The resulting
// rebase request is durable, so an Agent can resume by submitting the opened
// rebase stage; callers never select a target or strategy.
func (s *Service) reconcileTaskExecutionBase(ctx context.Context, state model.TaskExecutionState, project config.ProjectConfig, canonical string) error {
	path, err := gitx.TaskWorktreePath(s.Config.StateDir, state.ProjectID, state.TaskID)
	if err != nil {
		return err
	}
	lane := project
	lane.Root = path
	commits, err := s.Git.LocalLog(ctx, path, state.BaseHead, state.Head, 1024)
	if err != nil || len(commits) == 0 {
		if err != nil {
			return fmt.Errorf("read Task lane commits for rebase: %w", err)
		}
		return fmt.Errorf("Task lane has no commits to rebase")
	}
	ids := make([]string, 0, len(commits))
	for _, commit := range commits {
		ids = append(ids, commit.SHA)
	}
	newHead, _, err := s.Git.ReplayTaskCommits(ctx, lane, canonical, ids)
	if err != nil {
		return err
	}
	actual, branch, clean, err := s.Git.CurrentHead(ctx, lane)
	if err != nil || !clean || branch != state.Branch || actual != newHead {
		return fmt.Errorf("rebased Task lane failed exact identity verification")
	}
	state.BaseHead = canonical
	state.Head = actual
	state.Stage = "rebase"
	state.Status = model.TaskExecutionChangesRequested
	state.Worktree = taskExecutionWorktree(state.TaskID, actual[:8])
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
