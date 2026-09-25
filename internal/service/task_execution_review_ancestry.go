package service

import (
	"context"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/gitx"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

func (s *Service) taskExecutionReviewSelection(ctx context.Context, projectID, key, stage string, state model.TaskExecutionState) (sqlitestore.TaskExecutionPhase, string, error) {
	if s.Durability == nil {
		return sqlitestore.TaskExecutionPhase{}, "", fmt.Errorf("shared durability is unavailable")
	}
	if state.ProjectID != projectID || state.TaskID != key || state.Status != model.TaskExecutionAwaitingReview || state.Stage != stage {
		return sqlitestore.TaskExecutionPhase{}, "", fmt.Errorf("Task is not awaiting %s review", stage)
	}
	phase, found, err := s.Durability.ReadLatestTaskExecutionPhase(ctx, projectID, key, stage)
	if err != nil || !found {
		if err != nil {
			return sqlitestore.TaskExecutionPhase{}, "", err
		}
		return sqlitestore.TaskExecutionPhase{}, "", fmt.Errorf("Task has no %s submission", stage)
	}
	if phase.ProjectID != projectID || phase.TaskID != key || phase.Stage != stage ||
		phase.EventKind != "submission" || phase.Status != model.TaskExecutionAwaitingReview || phase.Decision != "" || phase.Comment != "" ||
		phase.ExecutionRevision != state.ExecutionRevision || phase.TaskRevisionSHA256 != state.TaskRevisionSHA256 ||
		phase.Head != state.Head || phase.Branch != state.Branch {
		return sqlitestore.TaskExecutionPhase{}, "", fmt.Errorf("Task review is stale")
	}
	if model.ValidateCommitSHA(phase.Head) != nil {
		return sqlitestore.TaskExecutionPhase{}, "", fmt.Errorf("Task review has invalid head authority")
	}
	if state.Worktree != taskExecutionWorktree(key, phase.Head[:8]) {
		return sqlitestore.TaskExecutionPhase{}, "", fmt.Errorf("Task review has invalid worktree authority")
	}
	actual, branch, clean, err := s.taskExecutionLaneHead(ctx, projectID, key, state)
	if err != nil || !clean || branch != state.Branch || actual != phase.Head {
		return sqlitestore.TaskExecutionPhase{}, "", fmt.Errorf("assigned Task lane must be clean on the submitted head")
	}
	comparisonBase := state.BaseHead
	ancestryBase := state.BaseHead
	if stage != "code" {
		previous := "code"
		if stage == "rebase" {
			if _, testsAccepted, testsErr := s.Durability.ReadLatestAcceptedTaskExecutionPhase(ctx, projectID, key, "tests"); testsErr != nil {
				return sqlitestore.TaskExecutionPhase{}, "", testsErr
			} else if testsAccepted {
				previous = "tests"
			}
		}
		accepted, acceptedFound, acceptedErr := s.Durability.ReadLatestAcceptedTaskExecutionPhase(ctx, projectID, key, previous)
		if acceptedErr != nil {
			return sqlitestore.TaskExecutionPhase{}, "", acceptedErr
		}
		acceptedStatus := accepted.Status == model.TaskExecutionReadyForVerification || (previous == "code" && accepted.Status == model.TaskExecutionDispatched)
		if !acceptedFound || accepted.ProjectID != projectID || accepted.TaskID != key || accepted.Stage != previous ||
			accepted.EventKind != "review" || accepted.Decision != "accept" || !acceptedStatus ||
			accepted.ExecutionRevision >= phase.ExecutionRevision ||
			accepted.TaskRevisionSHA256 != state.TaskRevisionSHA256 || model.ValidateCommitSHA(accepted.Head) != nil || accepted.Branch != state.Branch {
			return sqlitestore.TaskExecutionPhase{}, "", fmt.Errorf("accepted %s submission is required for review", previous)
		}
		comparisonBase = accepted.Head
		if stage == "rebase" {
			ancestryBase = state.BaseHead
		} else {
			ancestryBase = accepted.Head
		}
	}
	lanePath, err := gitx.TaskWorktreePath(s.Config.StateDir, state.ProjectID, state.TaskID)
	if err != nil {
		return sqlitestore.TaskExecutionPhase{}, "", err
	}
	ancestor, err := s.Git.IsAncestor(ctx, lanePath, ancestryBase, phase.Head)
	if err != nil || !ancestor {
		return sqlitestore.TaskExecutionPhase{}, "", fmt.Errorf("Task %s review head is not descended from its required base", stage)
	}
	return phase, comparisonBase, nil
}
