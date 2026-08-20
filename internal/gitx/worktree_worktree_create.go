package gitx

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

// CreateTrainWorktree creates the server-owned isolated checkout for a
// TrainV2 lane. The caller supplies identity, never an arbitrary filesystem
// path; the path is derived from the Gateway-owned state directory.
func (r Runner) CreateTrainWorktree(ctx context.Context, p config.ProjectConfig, stateDir, projectID, trainID, branch, base string) error {
	path, err := ownedTrainWorktreePath(stateDir, projectID, trainID)
	if err != nil {
		return err
	}
	return r.createTrainWorktree(ctx, p, path, branch, base)
}

// CreateTrainWorktreeCompact creates the post-cutover compact worktree path.
func (r Runner) CreateTrainWorktreeCompact(ctx context.Context, p config.ProjectConfig, stateDir, projectCode, trainID, branch, base string) error {
	path, err := ownedCompactTrainWorktreePath(stateDir, projectCode, trainID)
	if err != nil {
		return err
	}
	return r.createTrainWorktree(ctx, p, path, branch, base)
}
func (r Runner) createTrainWorktree(ctx context.Context, p config.ProjectConfig, path, branch, base string) error {
	if err := model.ValidateBranch(branch); err != nil {
		return err
	}
	if err := model.ValidateCommitSHA(base); err != nil {
		return err
	}
	root, err := filepath.Abs(p.Root)
	if err != nil {
		return err
	}
	target, err := filepath.Abs(path)
	if err != nil || target == root {
		return fmt.Errorf("train worktree path must be separate from project root")
	}
	if _, err := os.Lstat(target); err == nil {
		return fmt.Errorf("train worktree path already exists")
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	_, err = r.command(ctx, p.Root, false, "worktree", "add", "-b", branch, target, base)
	return err
}

// RemoveTrainWorktree removes only the server-derived TrainV2 worktree path.
func (r Runner) RemoveTrainWorktree(ctx context.Context, p config.ProjectConfig, stateDir, projectID, trainID string) error {
	path, err := ownedTrainWorktreePath(stateDir, projectID, trainID)
	if err != nil {
		return err
	}
	return r.removeTrainWorktree(ctx, p, path)
}

// RemoveTrainWorktreeCompact removes a post-cutover compact worktree.
func (r Runner) RemoveTrainWorktreeCompact(ctx context.Context, p config.ProjectConfig, stateDir, projectCode, trainID string) error {
	path, err := ownedCompactTrainWorktreePath(stateDir, projectCode, trainID)
	if err != nil {
		return err
	}
	return r.removeTrainWorktree(ctx, p, path)
}
func (r Runner) removeTrainWorktree(ctx context.Context, p config.ProjectConfig, path string) error {
	target, err := filepath.Abs(path)
	if err != nil || target == filepath.Clean(p.Root) {
		return fmt.Errorf("invalid train worktree path")
	}
	if _, statErr := os.Lstat(target); os.IsNotExist(statErr) {
		return nil
	} else if statErr != nil {
		return statErr
	}
	_, err = r.command(ctx, p.Root, false, "worktree", "remove", target)
	return err
}
