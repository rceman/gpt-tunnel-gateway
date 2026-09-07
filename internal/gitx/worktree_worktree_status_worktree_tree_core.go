package gitx

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (r Runner) PrepareBranch(ctx context.Context, p config.ProjectConfig, branch, base string) error {
	if err := model.ValidateBranch(branch); err != nil {
		return err
	}
	if _, err := r.command(ctx, p.Root, false, "check-ref-format", "--branch", branch); err != nil {
		return fmt.Errorf("invalid Git branch: %w", err)
	}
	status, err := r.WorktreeStatus(ctx, p)
	if err != nil {
		return err
	}
	if !status.Clean {
		return fmt.Errorf("project worktree is dirty")
	}
	resolved, err := r.Resolve(ctx, p.Root, base)
	if err != nil {
		return err
	}
	if resolved != base {
		return fmt.Errorf("base revision did not resolve exactly")
	}
	_, err = r.command(ctx, p.Root, false, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	if err == nil {
		if _, e := r.command(ctx, p.Root, false, "merge-base", "--is-ancestor", base, branch); e != nil {
			return fmt.Errorf("existing branch is not based on task base")
		}
		_, err = r.command(ctx, p.Root, false, "switch", branch)
		return err
	}
	_, err = r.command(ctx, p.Root, false, "switch", "-c", branch, base)
	return err
}

func ownedTrainWorktreePath(stateDir, projectID, trainID string) (string, error) {
	if err := model.ValidateProjectIdentifier(projectID); err != nil {
		return "", err
	}
	if _, _, err := model.ParseTrainV2ID(trainID); err != nil {
		return "", err
	}
	if stateDir == "" || strings.ContainsAny(stateDir, "\x00\r\n") {
		return "", fmt.Errorf("invalid train runtime state directory")
	}
	return filepath.Join(stateDir, "train-worktrees", projectID, trainID), nil
}

func ownedCompactTrainWorktreePath(stateDir, projectCode, trainID string) (string, error) {
	if err := model.ValidateProjectCode(projectCode); err != nil {
		return "", err
	}
	code, _, err := model.ParseTrainV2ID(trainID)
	if err != nil || code != projectCode {
		return "", fmt.Errorf("train ID project code does not match project code")
	}
	if stateDir == "" || strings.ContainsAny(stateDir, "\x00\r\n") {
		return "", fmt.Errorf("invalid train runtime state directory")
	}
	return filepath.Join(stateDir, "work", projectCode, trainID[len(projectCode)+1:]), nil
}
