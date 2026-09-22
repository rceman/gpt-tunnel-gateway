package gitx

import (
	"context"
	"fmt"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (r Runner) CommitTree(ctx context.Context, p config.ProjectConfig, commit string) (string, error) {
	if err := model.ValidateCommitSHA(commit); err != nil {
		return "", err
	}
	out, err := r.command(ctx, p.Root, false, "rev-parse", commit+"^{tree}")
	if err != nil {
		return "", fmt.Errorf("resolve Git tree: %w", err)
	}
	tree := strings.TrimSpace(string(out))
	if err := model.ValidateCommitSHA(tree); err != nil {
		return "", fmt.Errorf("resolve Git tree: %w", err)
	}
	return tree, nil
}
