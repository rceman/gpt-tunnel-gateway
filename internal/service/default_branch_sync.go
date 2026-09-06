package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/gitx"
)

func (s *Service) synchronizeDefaultBranchWorktree(ctx context.Context, project config.ProjectConfig, canonicalHead string) (gitx.WorktreeStatus, error) {
	branch := strings.TrimPrefix(project.DefaultBranch, "refs/heads/")
	if branch == "" {
		branch = "main"
	}
	inventory, err := s.Git.LoadWorktreeInventory(ctx, project)
	if err != nil {
		return gitx.WorktreeStatus{}, fmt.Errorf("read default branch worktree inventory: %w", err)
	}
	worktree, err := inventory.Resolve("refs/heads/" + branch)
	if err != nil {
		return gitx.WorktreeStatus{}, fmt.Errorf("resolve default branch worktree: %w", err)
	}
	return s.Git.SynchronizeDefaultBranchWorktree(ctx, worktree, canonicalHead)
}

func defaultBranchName(branch string) string {
	return strings.TrimPrefix(branch, "refs/heads/")
}
