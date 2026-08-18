package gitx

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (r Runner) revisionRoot(ctx context.Context, p config.ProjectConfig, revision string) (string, error) {
	if root, found, err := r.localRevisionRoot(ctx, p, []string{revision}); err != nil {
		return "", err
	} else if found {
		return root, nil
	}
	if err := r.EnsureMirror(ctx, p); err != nil {
		return "", err
	}
	return p.Mirror, nil
}

func (r Runner) revisionsRoot(ctx context.Context, p config.ProjectConfig, revisions ...string) (string, error) {
	allExact := len(revisions) > 0
	for _, revision := range revisions {
		if model.ValidateCommitSHA(revision) != nil {
			allExact = false
			break
		}
	}
	if allExact {
		if root, found, err := r.localRevisionRoot(ctx, p, revisions); err != nil {
			return "", err
		} else if found {
			return root, nil
		}
	}
	if err := r.EnsureMirror(ctx, p); err != nil {
		return "", err
	}
	return p.Mirror, nil
}

func (r Runner) localRevisionRoot(ctx context.Context, p config.ProjectConfig, revisions []string) (string, bool, error) {
	if r.StateDir == "" || len(revisions) == 0 {
		return "", false, nil
	}
	for _, revision := range revisions {
		if model.ValidateCommitSHA(revision) != nil {
			return "", false, nil
		}
	}
	out, err := r.command(ctx, p.Root, false, "worktree", "list", "--porcelain")
	if err != nil {
		return "", false, err
	}
	if int64(len(out)) > r.MaxReadBytes {
		return "", false, fmt.Errorf("local Train worktree listing exceeds read limit")
	}
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.HasPrefix(line, "worktree ") {
			continue
		}
		root := filepath.Clean(strings.TrimPrefix(line, "worktree "))
		if !isManagedTrainWorktree(r.StateDir, root) {
			continue
		}
		ok, err := r.localWorktreeContains(ctx, root, revisions)
		if err != nil {
			return "", false, err
		}
		if ok {
			return root, true, nil
		}
	}
	return "", false, nil
}

func isManagedTrainWorktree(stateDir, root string) bool {
	rel, err := filepath.Rel(filepath.Clean(stateDir), filepath.Clean(root))
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	return len(parts) >= 3 && (parts[0] == "train-worktrees" || parts[0] == "work")
}

func (r Runner) localWorktreeContains(ctx context.Context, root string, revisions []string) (bool, error) {
	for _, revision := range revisions {
		resolved, err := r.command(ctx, root, false, "rev-parse", "--verify", revision+"^{commit}")
		if err != nil || strings.TrimSpace(string(resolved)) != revision {
			return false, nil
		}
		head, err := r.command(ctx, root, false, "rev-parse", "--verify", "HEAD^{commit}")
		if err != nil {
			return false, err
		}
		if _, err := r.command(ctx, root, false, "merge-base", "--is-ancestor", revision, strings.TrimSpace(string(head))); err != nil {
			return false, nil
		}
	}
	return true, nil
}
