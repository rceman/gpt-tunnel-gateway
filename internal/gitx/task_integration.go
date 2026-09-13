package gitx

import (
	"context"
	"fmt"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

// PrepareTaskIntegrationCommit creates the exact integration commit object —
// one parent at the verified canonical base and the verified candidate tree —
// without touching any ref, index, or worktree.
func (r Runner) PrepareTaskIntegrationCommit(ctx context.Context, p config.ProjectConfig, tree, parent, message string) (string, error) {
	if model.ValidateCommitSHA(tree) != nil || model.ValidateCommitSHA(parent) != nil || strings.TrimSpace(message) == "" {
		return "", fmt.Errorf("invalid Task integration authority")
	}
	out, err := r.command(ctx, p.Root, false,
		"-c", "user.name=GPT Tunnel Gateway",
		"-c", "user.email=gpt-tunnel-gateway@localhost",
		"commit-tree", tree, "-p", parent, "-m", message)
	if err != nil {
		return "", fmt.Errorf("prepare Task integration commit: %w", err)
	}
	commit := strings.TrimSpace(string(out))
	if err := model.ValidateCommitSHA(commit); err != nil {
		return "", fmt.Errorf("prepared Task integration commit is invalid: %w", err)
	}
	return commit, nil
}

// SyncTaskIntegrationCheckout brings the default-branch checkout to the
// landed canonical head through a fast-forward merge only. The merge itself
// is the byte-safety boundary: unrelated staged or untracked bytes are
// carried forward, conflicting ones make the merge refuse, and the caller
// can retry recoverably — nothing is ever reset, forced, or overwritten.
func (r Runner) SyncTaskIntegrationCheckout(ctx context.Context, p config.ProjectConfig, branch, head string) (WorktreeStatus, error) {
	if err := model.ValidateBranch(branch); err != nil {
		return WorktreeStatus{}, err
	}
	if err := model.ValidateCommitSHA(head); err != nil {
		return WorktreeStatus{}, err
	}
	status, err := r.WorktreeStatus(ctx, p)
	if err != nil {
		return WorktreeStatus{}, err
	}
	if status.Branch != branch {
		return status, fmt.Errorf("default branch checkout is on %q, want %q", status.Branch, branch)
	}
	if status.Head == head {
		return status, nil
	}
	if status.Head == "" || status.Head == "(initial)" {
		return status, fmt.Errorf("default branch checkout has no head")
	}
	if err := r.MaterializeMirrorCommit(ctx, p, branch, head); err != nil {
		return status, err
	}
	ancestor, err := r.IsAncestor(ctx, p.Root, status.Head, head)
	if err != nil {
		return status, err
	}
	if !ancestor {
		return status, fmt.Errorf("default branch checkout head %s is not an ancestor of %s", status.Head, head)
	}
	if _, err := r.command(ctx, p.Root, false, "merge", "--ff-only", head); err != nil {
		return status, fmt.Errorf("default branch checkout synchronization is not clean: %w", err)
	}
	after, err := r.WorktreeStatus(ctx, p)
	if err != nil {
		return status, err
	}
	if after.Branch != branch || after.Head != head {
		return after, fmt.Errorf("default branch checkout did not reach the landed head")
	}
	return after, nil
}

// RemoteBranchHead resolves the exact remote branch head without updating any
// local tracking ref. It accepts exactly one <sha>\t<ref> record.
func (r Runner) RemoteBranchHead(ctx context.Context, p config.ProjectConfig, branch string) (string, error) {
	if err := model.ValidateBranch(branch); err != nil {
		return "", err
	}
	ref := "refs/heads/" + branch
	out, err := r.command(ctx, p.Root, false, "ls-remote", "--exit-code", p.Remote, ref)
	if err != nil {
		return "", fmt.Errorf("remote branch head: %w", err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 1 {
		return "", fmt.Errorf("remote branch %q did not resolve to exactly one ref", branch)
	}
	fields := strings.Split(lines[0], "\t")
	if len(fields) != 2 || fields[1] != ref || model.ValidateCommitSHA(fields[0]) != nil {
		return "", fmt.Errorf("remote branch %q resolved ambiguously", branch)
	}
	return fields[0], nil
}

// MaterializeRemoteBranchObjects fetches the remote branch objects only: no
// refs, FETCH_HEAD, or worktree are updated. The configured remote URL is
// resolved first so the fetch cannot opportunistically update remote-tracking
// refs through the configured fetch refspec.
func (r Runner) MaterializeRemoteBranchObjects(ctx context.Context, p config.ProjectConfig, branch string) error {
	if err := model.ValidateBranch(branch); err != nil {
		return err
	}
	out, err := r.command(ctx, p.Root, false, "remote", "get-url", p.Remote)
	if err != nil {
		return fmt.Errorf("resolve remote URL: %w", err)
	}
	url := strings.TrimSpace(string(out))
	if url == "" {
		return fmt.Errorf("remote %q has no URL", p.Remote)
	}
	if _, err := r.command(ctx, p.Root, false, "fetch", "--no-tags", "--no-write-fetch-head", url, "refs/heads/"+branch); err != nil {
		return fmt.Errorf("materialize remote branch objects: %w", err)
	}
	return nil
}

// InspectTaskIntegrationCommit returns the exact tree and parent list of a
// prepared integration commit.
func (r Runner) InspectTaskIntegrationCommit(ctx context.Context, p config.ProjectConfig, commit string) (string, []string, error) {
	if err := model.ValidateCommitSHA(commit); err != nil {
		return "", nil, err
	}
	out, err := r.command(ctx, p.Root, false, "cat-file", "commit", commit)
	if err != nil {
		return "", nil, fmt.Errorf("inspect Task integration commit: %w", err)
	}
	tree := ""
	var parents []string
	for _, line := range strings.Split(string(out), "\n") {
		if line == "" {
			break
		}
		if rest, ok := strings.CutPrefix(line, "tree "); ok {
			tree = rest
			continue
		}
		if rest, ok := strings.CutPrefix(line, "parent "); ok {
			parents = append(parents, rest)
		}
	}
	if err := model.ValidateCommitSHA(tree); err != nil || len(parents) == 0 {
		return "", nil, fmt.Errorf("Task integration commit has no recorded tree/parents")
	}
	return tree, parents, nil
}
