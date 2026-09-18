package gitx

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (r Runner) createManagedWorktree(ctx context.Context, p config.ProjectConfig, path, branch, base string) error {
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
		return fmt.Errorf("managed worktree path must be separate from project root")
	}
	if _, err := os.Lstat(target); err == nil {
		return fmt.Errorf("managed worktree path already exists")
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	_, err = r.command(ctx, p.Root, false, "worktree", "add", "-b", branch, target, base)
	return err
}

func (r Runner) removeManagedWorktree(ctx context.Context, p config.ProjectConfig, path string) error {
	target, err := filepath.Abs(path)
	if err != nil || target == filepath.Clean(p.Root) {
		return fmt.Errorf("invalid managed worktree path")
	}
	if _, statErr := os.Lstat(target); os.IsNotExist(statErr) {
		return nil
	} else if statErr != nil {
		return statErr
	}
	_, err = r.command(ctx, p.Root, false, "worktree", "remove", target)
	return err
}
