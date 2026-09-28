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
	return r.DeleteManagedBranchAtExpectedHead(ctx, p, branch, expectedHead)
}

func (r Runner) ManagedBranchHead(ctx context.Context, p config.ProjectConfig, branch string) (string, bool, error) {
	if err := model.ValidateBranch(branch); err != nil {
		return "", false, err
	}
	out, err := r.command(ctx, p.Root, false, "for-each-ref", "--format=%(objectname)", "refs/heads/"+branch)
	if err != nil {
		return "", false, err
	}
	resolved := strings.TrimSpace(string(out))
	if resolved == "" {
		return "", false, nil
	}
	if strings.ContainsAny(resolved, "\r\n") || model.ValidateCommitSHA(resolved) != nil {
		return "", false, fmt.Errorf("invalid server-owned managed branch head")
	}
	return resolved, true, nil
}

func (r Runner) DeleteManagedBranchAtExpectedHead(ctx context.Context, p config.ProjectConfig, branch, expectedHead string) error {
	if err := model.ValidateBranch(branch); err != nil {
		return err
	}
	if err := model.ValidateCommitSHA(expectedHead); err != nil {
		return err
	}
	resolved, found, err := r.ManagedBranchHead(ctx, p, branch)
	if err != nil || !found {
		return err
	}
	if resolved != expectedHead {
		return fmt.Errorf("server-owned managed branch is not at the expected head")
	}
	if _, err := r.command(ctx, p.Root, false, "update-ref", "-d", "refs/heads/"+branch, expectedHead); err != nil {
		return err
	}
	_, found, err = r.ManagedBranchHead(ctx, p, branch)
	if err != nil {
		return err
	}
	if found {
		return fmt.Errorf("server-owned managed branch remains after deletion")
	}
	return nil
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
