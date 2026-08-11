package gitx

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (r Runner) WorktreeStatus(ctx context.Context, p config.ProjectConfig) (WorktreeStatus, error) {
	out, err := r.command(ctx, p.Root, false, "status", "--porcelain=v2", "--branch")
	if err != nil {
		return WorktreeStatus{}, err
	}
	text, err := bounded(out, r.MaxReadBytes)
	if err != nil {
		return WorktreeStatus{}, err
	}
	s := WorktreeStatus{
		Porcelain: text,
		Clean:     true,
	}
	for _, line := range strings.Split(text, "\n") {
		switch {
		case strings.HasPrefix(line, "# branch.head "):
			s.Branch = strings.TrimPrefix(line, "# branch.head ")
		case strings.HasPrefix(line, "# branch.oid "):
			s.Head = strings.TrimPrefix(line, "# branch.oid ")
		case strings.HasPrefix(line, "# branch.upstream "):
			s.Upstream = strings.TrimPrefix(line, "# branch.upstream ")
		case strings.HasPrefix(line, "# branch.ab "):
			fmt.Sscanf(line, "# branch.ab +%d -%d", &s.Ahead, &s.Behind)
		case line != "" && !strings.HasPrefix(line, "# "):
			s.Clean = false
		}
	}
	return s, nil
}
func (r Runner) WorktreeDiff(ctx context.Context, p config.ProjectConfig, staged bool) (string, error) {
	args := []string{"diff", "--no-ext-diff", "--no-textconv"}
	if staged {
		args = append(args, "--cached")
	}
	out, err := r.command(ctx, p.Root, false, args...)
	if err != nil {
		return "", err
	}
	return bounded(out, r.MaxDiffBytes)
}
func (r Runner) Resolve(ctx context.Context, root, rev string) (string, error) {
	if err := model.ValidateRevision(rev); err != nil {
		return "", err
	}
	out, err := r.command(ctx, root, false, "rev-parse", "--verify", rev+"^{commit}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

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

// CreateTrainWorktree creates the server-owned isolated checkout for a
// TrainV2 lane. The caller supplies identity, never an arbitrary filesystem
// path; the path is derived from the Gateway-owned state directory.
func (r Runner) CreateTrainWorktree(ctx context.Context, p config.ProjectConfig, stateDir, projectID, trainID, branch, base string) error {
	if err := model.ValidateBranch(branch); err != nil {
		return err
	}
	if err := model.ValidateCommitSHA(base); err != nil {
		return err
	}
	path, err := ownedTrainWorktreePath(stateDir, projectID, trainID)
	if err != nil {
		return err
	}
	root, err := filepath.Abs(p.Root)
	if err != nil {
		return err
	}
	target, err := filepath.Abs(path)
	if err != nil || target == root {
		return fmt.Errorf("train worktree path must be separate from project root")
	}
	if _, err := os.Lstat(target); err == nil {
		return fmt.Errorf("train worktree path already exists")
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	_, err = r.command(ctx, p.Root, false, "worktree", "add", "-b", branch, target, base)
	return err
}

// RemoveTrainWorktree removes only the server-derived TrainV2 worktree path.
func (r Runner) RemoveTrainWorktree(ctx context.Context, p config.ProjectConfig, stateDir, projectID, trainID string) error {
	path, err := ownedTrainWorktreePath(stateDir, projectID, trainID)
	if err != nil {
		return err
	}
	target, err := filepath.Abs(path)
	if err != nil || target == filepath.Clean(p.Root) {
		return fmt.Errorf("invalid train worktree path")
	}
	_, err = r.command(ctx, p.Root, false, "worktree", "remove", target)
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

// TreeID returns the exact Git content tree for the current HEAD. Commit
// identity is intentionally not part of this value so equivalent trees can
// be recognized across commits.
func (r Runner) TreeID(ctx context.Context, p config.ProjectConfig) (string, error) {
	out, err := r.command(ctx, p.Root, false, "rev-parse", "--verify", "HEAD^{tree}")
	if err != nil {
		return "", err
	}
	tree := strings.TrimSpace(string(out))
	if err := model.ValidateRevision(tree); err != nil {
		return "", fmt.Errorf("invalid Git tree identity: %w", err)
	}
	return tree, nil
}

// WorktreeContentID returns the exact prospective Git tree object for the
// current worktree by staging into a private temporary index. The repository's
// real index is never changed.
func (r Runner) WorktreeContentID(ctx context.Context, p config.ProjectConfig) (string, error) {
	tempDir, err := os.MkdirTemp("", "gpt-tunnel-test-index-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tempDir)
	indexPath := filepath.Join(tempDir, "index")
	env := []string{"GIT_INDEX_FILE=" + indexPath}
	if _, err := r.commandWithEnv(ctx, p.Root, false, env, "read-tree", "HEAD"); err != nil {
		return "", err
	}
	if _, err := r.commandWithEnv(ctx, p.Root, false, env, "add", "-A", "--", "."); err != nil {
		return "", err
	}
	out, err := r.commandWithEnv(ctx, p.Root, false, env, "write-tree")
	if err != nil {
		return "", err
	}
	tree := strings.TrimSpace(string(out))
	if err := model.ValidateRevision(tree); err != nil {
		return "", fmt.Errorf("invalid prospective Git tree identity: %w", err)
	}
	return tree, nil
}
