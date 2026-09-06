package gitx

import (
	"context"
	"fmt"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

// SynchronizeDefaultBranchWorktree makes the configured physical default
// branch checkout match the mirror-authoritative commit using only a strict
// fast-forward. It returns the status observed after synchronization.
func (r Runner) SynchronizeDefaultBranchWorktree(ctx context.Context, p config.ProjectConfig, canonicalHead string) (WorktreeStatus, error) {
	branch := strings.TrimPrefix(p.DefaultBranch, "refs/heads/")
	if err := model.ValidateBranch(branch); err != nil {
		return WorktreeStatus{}, fmt.Errorf("default branch: %w", err)
	}
	if err := model.ValidateCommitSHA(canonicalHead); err != nil {
		return WorktreeStatus{}, fmt.Errorf("canonical default branch head: %w", err)
	}
	status, err := r.WorktreeStatus(ctx, p)
	if err != nil {
		return WorktreeStatus{}, fmt.Errorf("read default branch worktree status: %w", err)
	}
	if status.Branch != branch {
		return WorktreeStatus{}, fmt.Errorf("default branch worktree is on %q, want %q", status.Branch, branch)
	}
	if !status.Clean {
		return WorktreeStatus{}, fmt.Errorf("default branch worktree is dirty")
	}
	if status.Head == canonicalHead {
		return status, nil
	}
	if err := model.ValidateCommitSHA(status.Head); err != nil {
		return WorktreeStatus{}, fmt.Errorf("default branch worktree head: %w", err)
	}
	if err := r.MaterializeMirrorCommit(ctx, p, branch, canonicalHead); err != nil {
		return WorktreeStatus{}, fmt.Errorf("materialize canonical default branch head: %w", err)
	}
	ancestor, err := r.IsAncestor(ctx, p.Root, status.Head, canonicalHead)
	if err != nil {
		return WorktreeStatus{}, fmt.Errorf("check default branch fast-forward: %w", err)
	}
	if !ancestor {
		return WorktreeStatus{}, fmt.Errorf("default branch worktree head %s diverges from canonical head %s", status.Head, canonicalHead)
	}
	if _, err := r.command(ctx, p.Root, false, "merge", "--ff-only", canonicalHead); err != nil {
		return WorktreeStatus{}, fmt.Errorf("fast-forward default branch worktree: %w", err)
	}
	status, err = r.WorktreeStatus(ctx, p)
	if err != nil {
		return WorktreeStatus{}, fmt.Errorf("re-read default branch worktree status: %w", err)
	}
	if status.Branch != branch || status.Head != canonicalHead || !status.Clean {
		return WorktreeStatus{}, fmt.Errorf("default branch worktree did not reach canonical clean head")
	}
	return status, nil
}
