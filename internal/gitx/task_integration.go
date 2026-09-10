package gitx

import (
	"context"
	"fmt"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

// SquashTaskIntoDefaultBranch integrates a clean server-owned Task lane into
// an exact clean default-branch head and returns the resulting commit.
func (r Runner) SquashTaskIntoDefaultBranch(ctx context.Context, project, lane config.ProjectConfig, base, head, message string) (string, error) {
	if model.ValidateCommitSHA(base) != nil || model.ValidateCommitSHA(head) != nil || strings.TrimSpace(message) == "" {
		return "", fmt.Errorf("invalid Task integration authority")
	}
	target, err := r.WorktreeStatus(ctx, project)
	if err != nil || !target.Clean {
		return "", fmt.Errorf("default branch must be clean before Task integration")
	}
	if target.Head != base || target.Branch != strings.TrimPrefix(project.DefaultBranch, "refs/heads/") {
		return "", fmt.Errorf("default branch is not at the exact Task base")
	}
	source, err := r.WorktreeStatus(ctx, lane)
	if err != nil || !source.Clean || source.Head != head {
		return "", fmt.Errorf("Task lane is not at its exact clean reviewed head")
	}
	if _, err := r.command(ctx, lane.Root, false, "merge-base", "--is-ancestor", base, head); err != nil {
		return "", fmt.Errorf("Task head is not descended from its base")
	}
	if _, err := r.command(ctx, project.Root, false, "merge", "--squash", "--no-commit", source.Branch); err != nil {
		return "", fmt.Errorf("squash Task lane: %w", err)
	}
	if _, err := r.command(ctx, project.Root, false, "commit", "-m", message); err != nil {
		_, _ = r.command(context.Background(), project.Root, false, "reset", "--hard", base)
		return "", fmt.Errorf("commit squashed Task lane: %w", err)
	}
	result, _, clean, err := r.CurrentHead(ctx, project)
	if err != nil || !clean || result == base {
		return "", fmt.Errorf("integrated default branch is not a new clean commit")
	}
	return result, nil
}
