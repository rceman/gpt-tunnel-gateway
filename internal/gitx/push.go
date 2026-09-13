package gitx

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

// PushFastForward publishes one exact commit through the configured remote.
// The server rechecks expectedRemote immediately before calling this method;
// the non-forced update remains the race-safe fast-forward guard — a remote
// that is not an ancestor of the commit is always rejected.
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

// PushTaskIntegrationCAS publishes the prepared Task integration commit
// through one explicit expected-old compare-and-swap. The
// --force-with-lease=<ref>:<verifiedBase> form is permitted only because the
// checks below independently prove the prepared commit is a strict child of
// the immutable verified base — exact sole parent, exact verified tree, and
// strict descendant reachability — so the lease can only reject a moved
// remote, never rewrite an unrelated head. There is no implicit lease, no
// tracking-derived expectation, no fallback, and no retry.
func (r Runner) PushTaskIntegrationCAS(ctx context.Context, p config.ProjectConfig, branch, verifiedBase, verifiedTree, prepared string) error {
	if err := model.ValidateBranch(branch); err != nil {
		return err
	}
	if err := model.ValidateCommitSHA(verifiedBase); err != nil {
		return fmt.Errorf("verified base head: %w", err)
	}
	if err := model.ValidateCommitSHA(verifiedTree); err != nil {
		return fmt.Errorf("verified candidate tree: %w", err)
	}
	if err := model.ValidateCommitSHA(prepared); err != nil {
		return fmt.Errorf("prepared integration head: %w", err)
	}
	tree, parents, err := r.InspectTaskIntegrationCommit(ctx, p, prepared)
	if err != nil {
		return err
	}
	if tree != verifiedTree || len(parents) != 1 || parents[0] != verifiedBase {
		return fmt.Errorf("prepared Task integration commit does not match the verified tree and base parent")
	}
	if prepared == verifiedBase {
		return fmt.Errorf("prepared Task integration commit equals the verified base")
	}
	ancestor, err := r.IsAncestor(ctx, p.Root, verifiedBase, prepared)
	if err != nil {
		return err
	}
	if !ancestor {
		return fmt.Errorf("verified base is not an ancestor of the prepared Task integration commit")
	}
	ref := "refs/heads/" + branch
	out, err := r.command(ctx, p.Root, false, "push", "--porcelain", "--force-with-lease="+ref+":"+verifiedBase, p.Remote, prepared+":"+ref)
	if err != nil {
		if taskIntegrationPushStaleInfo(out, prepared+":"+ref) {
			return fmt.Errorf("%w: %v", ErrTaskIntegrationExpectedOldMismatch, err)
		}
		return fmt.Errorf("Task integration compare-and-swap failed: %w", err)
	}
	return nil
}

// ErrTaskIntegrationExpectedOldMismatch marks the conclusive push porcelain
// result where the remote ref no longer holds the expected-old base. Opaque
// transport failures never carry it.
var ErrTaskIntegrationExpectedOldMismatch = errors.New("Task integration expected-old mismatch")

// taskIntegrationPushStaleInfo reports whether failed --porcelain push output
// records the exact rejection of this source/ref pair as stale expected-old
// information.
func taskIntegrationPushStaleInfo(out []byte, refspec string) bool {
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) == 3 && fields[0] == "!" && fields[1] == refspec && fields[2] == "[rejected] (stale info)" {
			return true
		}
	}
	return false
}
