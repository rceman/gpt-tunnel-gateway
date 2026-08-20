package gitx

import (
	"context"
	"fmt"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

// DeleteTrainBranch removes the exact server-created lane ref after its
// integrated head has been recorded. It never accepts a caller-supplied ref
// namespace or an unconditional ref deletion.
func (r Runner) DeleteTrainBranch(ctx context.Context, p config.ProjectConfig, branch, expectedHead string) error {
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
		return fmt.Errorf("server-owned Train branch is not at the expected integrated head")
	}
	_, err = r.command(ctx, p.Root, false, "update-ref", "-d", "refs/heads/"+branch, expectedHead)
	return err
}

// ReplayTrainCommits mechanically rebases a server-owned Train lane onto a
// refreshed target. The caller supplies only validated commits from the lane
// range. A conflict is aborted before returning and never becomes a merge
// commit or a force update.
func (r Runner) ReplayTrainCommits(ctx context.Context, p config.ProjectConfig, target string, commits []string) (string, map[string]string, error) {
	if err := model.ValidateCommitSHA(target); err != nil {
		return "", nil, err
	}
	for _, commit := range commits {
		if err := model.ValidateCommitSHA(commit); err != nil {
			return "", nil, err
		}
	}
	status, err := r.WorktreeStatus(ctx, p)
	if err != nil {
		return "", nil, err
	}
	if !status.Clean {
		return "", nil, fmt.Errorf("Train worktree is dirty before reconciliation")
	}
	originalHead := status.Head
	if _, err := r.command(ctx, p.Root, false, "reset", "--hard", target); err != nil {
		return "", nil, err
	}
	restore := func() {
		_, _ = r.command(context.Background(), p.Root, false, "cherry-pick", "--abort")
		_, _ = r.command(context.Background(), p.Root, false, "reset", "--hard", originalHead)
	}
	fail := func(err error) (string, map[string]string, error) {
		restore()
		return "", nil, err
	}
	mapping := make(map[string]string, len(commits))
	for _, commit := range commits {
		if _, err := r.command(ctx, p.Root, false, "cherry-pick", "--keep-redundant-commits", commit); err != nil {
			return fail(fmt.Errorf("Train reconciliation conflict at %s: %w", commit, err))
		}
		head, _, clean, headErr := r.CurrentHead(ctx, p)
		if headErr != nil || !clean {
			return fail(fmt.Errorf("reconciled Train lane is not clean"))
		}
		mapping[commit] = head
	}
	head, _, clean, err := r.CurrentHead(ctx, p)
	if err != nil || !clean {
		return fail(fmt.Errorf("reconciled Train lane is not clean"))
	}
	return head, mapping, nil
}

// ResetTrainWorktree discards a completed server-owned replay and leaves the
// lane at the refreshed integration target.  The caller then restarts the
// admitted work from that target; it must not execute the same tasks on top of
// the replayed commits.
func (r Runner) ResetTrainWorktree(ctx context.Context, p config.ProjectConfig, target string) error {
	if err := model.ValidateCommitSHA(target); err != nil {
		return err
	}
	status, err := r.WorktreeStatus(ctx, p)
	if err != nil {
		return err
	}
	if !status.Clean {
		return fmt.Errorf("Train worktree is dirty before replay reset")
	}
	if _, err := r.command(ctx, p.Root, false, "reset", "--hard", target); err != nil {
		return err
	}
	head, _, clean, err := r.CurrentHead(ctx, p)
	if err != nil {
		return err
	}
	if !clean || head != target {
		return fmt.Errorf("Train replay reset did not reach refreshed target")
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
