package gitx

import (
	"context"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

// RefreshDefaultBranch refreshes the configured managed mirror and returns
// the exact remote default-branch commit. The mirror is the server-owned
// remote view; the configured source worktree is never used as authority.
func (r Runner) RefreshDefaultBranch(ctx context.Context, p config.ProjectConfig) (string, error) {
	if err := model.ValidateBranch(p.DefaultBranch); err != nil {
		return "", fmt.Errorf("default branch: %w", err)
	}
	if err := r.Refresh(ctx, p); err != nil {
		return "", fmt.Errorf("refresh canonical remote: %w", err)
	}
	head, exists, err := r.MirrorBranchHead(ctx, p, p.DefaultBranch)
	if err != nil {
		return "", err
	}
	if !exists || model.ValidateCommitSHA(head) != nil {
		return "", fmt.Errorf("canonical origin/%s is unavailable", p.DefaultBranch)
	}
	return head, nil
}

// MaterializeMirrorCommit makes an exact mirror-authoritative commit
// available to the configured source repository without moving any local
// branch. This is required when the source clone has stale object storage.
func (r Runner) MaterializeMirrorCommit(ctx context.Context, p config.ProjectConfig, branch, commit string) error {
	if err := model.ValidateBranch(branch); err != nil {
		return err
	}
	if err := model.ValidateCommitSHA(commit); err != nil {
		return err
	}
	if resolved, err := r.Resolve(ctx, p.Root, commit); err == nil && resolved == commit {
		return nil
	}
	if p.Mirror == "" {
		return fmt.Errorf("managed mirror is required to materialize base")
	}
	if _, err := r.command(ctx, p.Root, false, "fetch", "--no-tags", p.Mirror, "refs/heads/"+branch); err != nil {
		return fmt.Errorf("materialize mirror commit: %w", err)
	}
	resolved, err := r.Resolve(ctx, p.Root, commit)
	if err != nil {
		return fmt.Errorf("materialized base is not available: %w", err)
	}
	if resolved != commit {
		return fmt.Errorf("materialized base resolved to %s, want %s", resolved, commit)
	}
	return nil
}
