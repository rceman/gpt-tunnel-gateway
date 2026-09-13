package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/gitx"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s *Service) resolveExactTaskCodeTarget(ctx context.Context, projectID, selector string, live bool) (localCodeTarget, error) {
	if s.Durability == nil {
		return localCodeTarget{}, fmt.Errorf("shared durability is unavailable")
	}
	states, err := s.Durability.ListTaskExecutionStates(ctx, projectID)
	if err != nil {
		return localCodeTarget{}, err
	}
	var state *model.TaskExecutionState
	for index := range states {
		if states[index].Worktree != selector {
			continue
		}
		if state != nil {
			return localCodeTarget{}, fmt.Errorf("ambiguous Task worktree selector %q", selector)
		}
		candidate := states[index]
		state = &candidate
	}
	if state == nil {
		return localCodeTarget{}, &CodeSelectorError{
			Kind:     CodeSelectorNotFound,
			Selector: selector,
		}
	}
	project, err := s.EffectiveProjectConfig(projectID)
	if err != nil {
		return localCodeTarget{}, err
	}
	path, err := gitx.TaskWorktreePath(s.Config.StateDir, projectID, state.TaskID)
	if err != nil {
		return localCodeTarget{}, err
	}
	project.Root = path
	status, err := s.Git.WorktreeStatus(ctx, project)
	if err != nil {
		return localCodeTarget{}, err
	}
	selectorHead := status.Head
	midRebase := false
	if status.Branch != state.Branch {
		onto, ontoErr := s.Git.RebaseOnto(ctx, project)
		branchHead, branchErr := s.Git.BranchHead(ctx, project, state.Branch)
		if ontoErr != nil || branchErr != nil || status.Branch != "(detached)" || onto == "" || onto != state.BaseHead || branchHead != state.Head {
			return localCodeTarget{}, &CodeSelectorError{
				Kind:     CodeSelectorStale,
				Selector: selector,
				Current:  state.Worktree,
			}
		}
		selectorHead = state.Head
		midRebase = true
	}
	if model.ValidateCommitSHA(selectorHead) != nil || !strings.HasSuffix(selector, "-"+strings.ToLower(selectorHead[:8])) {
		return localCodeTarget{}, &CodeSelectorError{
			Kind:     CodeSelectorStale,
			Selector: selector,
			Current:  state.Worktree,
		}
	}
	if !live && !status.Clean {
		return localCodeTarget{}, fmt.Errorf("worktree selector %q is dirty; set live=true for bounded observation", selector)
	}
	ancestor, err := s.Git.IsAncestor(ctx, project.Root, state.BaseHead, status.Head)
	if err != nil || !ancestor {
		return localCodeTarget{}, fmt.Errorf("worktree selector %q has an invalid Task base", selector)
	}
	if midRebase && !live {
		return localCodeTarget{}, fmt.Errorf("worktree selector %q is mid-rebase; set live=true for bounded observation", selector)
	}
	return localCodeTarget{
		CodeIdentity: CodeIdentity{
			ProjectID:   projectID,
			Worktree:    selector,
			Dirty:       !status.Clean,
			Live:        live,
			CurrentHead: status.Head,
		},
		ProjectWorktree: project,
		Kind:            "task",
		TaskID:          state.TaskID,
		DiffBase:        state.BaseHead,
	}, nil
}
