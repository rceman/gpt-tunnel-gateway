package service

import (
	"context"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/gitx"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

func (s *Service) validateTaskExecutionReviewAncestry(ctx context.Context, state model.TaskExecutionState, phase sqlitestore.TaskExecutionPhase) error {
	if s.Durability == nil {
		return fmt.Errorf("shared durability is unavailable")
	}
	base := state.BaseHead
	if phase.Stage != "code" {
		previous := "code"
		if phase.Stage == "rebase" {
			previous = "tests"
		}
		accepted, found, err := s.Durability.ReadLatestAcceptedTaskExecutionPhase(ctx, state.ProjectID, state.TaskID, previous)
		if err != nil {
			return err
		}
		if !found || accepted.Head == "" {
			return fmt.Errorf("accepted %s submission is required for review", previous)
		}
		base = accepted.Head
	}
	project, err := s.EffectiveProjectConfig(state.ProjectID)
	if err != nil {
		return err
	}
	lanePath, err := gitx.TaskWorktreePath(s.Config.StateDir, state.ProjectID, state.TaskID)
	if err != nil {
		return err
	}
	project.Root = lanePath
	ancestor, err := s.Git.IsAncestor(ctx, project.Root, base, phase.Head)
	if err != nil || !ancestor {
		return fmt.Errorf("Task %s review head is not descended from its required base", phase.Stage)
	}
	return nil
}
