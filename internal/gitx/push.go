package gitx

import (
	"context"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

// PushFastForward publishes one exact commit through the configured remote.
// The server rechecks expectedRemote immediately before calling this method;
// the non-force push remains the final race-safe fast-forward guard.
func (r Runner) PushFastForward(ctx context.Context, p config.ProjectConfig, branch, expectedRemote, commit string) error {
	if err := model.ValidateBranch(branch); err != nil {
		return err
	}
	if err := model.ValidateCommitSHA(expectedRemote); err != nil {
		return fmt.Errorf("expected remote head: %w", err)
	}
	if err := model.ValidateCommitSHA(commit); err != nil {
		return fmt.Errorf("integration head: %w", err)
	}
	_, err := r.command(ctx, p.Root, false, "push", p.Remote, commit+":refs/heads/"+branch)
	return err
}
