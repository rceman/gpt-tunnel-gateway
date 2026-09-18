package gitx

import (
	"context"
	"fmt"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

// DeleteManagedBranch removes the exact server-created lane ref after its
// integrated head has been recorded. It never accepts a caller-supplied ref
// namespace or an unconditional ref deletion.
func (r Runner) DeleteManagedBranch(ctx context.Context, p config.ProjectConfig, branch, expectedHead string) error {
	if err := model.ValidateBranch(branch); err != nil {
		return err
	}
	if err := model.ValidateCommitSHA(expectedHead); err != nil {
		return err
	}
	ref := "refs/heads/" + branch
	if _, err := r.command(ctx, p.Root, false, "show-ref", "--verify", "--quiet", ref); err != nil {
		return nil
	}
	resolved, err := r.Resolve(ctx, p.Root, ref)
	if err != nil || resolved != expectedHead {
		return fmt.Errorf("server-owned managed branch is not at the expected integrated head")
	}
	_, err = r.command(ctx, p.Root, false, "update-ref", "-d", "refs/heads/"+branch, expectedHead)
	return err
}
func (r Runner) IsAncestor(ctx context.Context, root, ancestor, descendant string) (bool, error) {
	if err := model.ValidateRevision(ancestor); err != nil {
		return false, err
	}
	if err := model.ValidateRevision(descendant); err != nil {
		return false, err
	}
	_, err := r.command(ctx, root, false, "merge-base", "--is-ancestor", ancestor, descendant)
	if err == nil {
		return true, nil
	}
	if strings.Contains(err.Error(), "exit status 1") {
		return false, nil
	}
	return false, err
}
func (r Runner) CurrentHead(ctx context.Context, p config.ProjectConfig) (string, string, bool, error) {
	s, err := r.WorktreeStatus(ctx, p)
	return s.Head, s.Branch, s.Clean, err
}
