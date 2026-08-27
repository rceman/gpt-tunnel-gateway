package gitx

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

// RefreshDefaultBranch refreshes the configured managed mirror and returns
// the exact remote default-branch commit. The mirror is the server-owned
// remote view; the configured source worktree is never used as authority.
func (r Runner) RefreshDefaultBranch(ctx context.Context, p config.ProjectConfig) (string, error) {
	if err := model.ValidateBranch(p.DefaultBranch); err != nil {
		return "", fmt.Errorf("default branch: %w", err)
	}
	if err := r.Refresh(ctx, p); err != nil {
		return "", fmt.Errorf("refresh canonical remote: %w", err)
	}
	head, exists, err := r.MirrorBranchHead(ctx, p, p.DefaultBranch)
	if err != nil {
		return "", err
	}
	if !exists || model.ValidateCommitSHA(head) != nil {
		return "", fmt.Errorf("canonical origin/%s is unavailable", p.DefaultBranch)
	}
	return head, nil
}

// CreateHotfixWorktree creates the server-derived hotfix checkout. Callers
// provide identity, never a filesystem path.
func (r Runner) CreateHotfixWorktree(ctx context.Context, p config.ProjectConfig, stateDir, projectID, slug, base string) (config.ProjectConfig, error) {
	path, branch, err := hotfixWorktreePath(stateDir, projectID, slug)
	if err != nil {
		return config.ProjectConfig{}, err
	}
	if err := r.createTrainWorktree(ctx, p, path, branch, base); err != nil {
		return config.ProjectConfig{}, err
	}
	result := p
	result.Root = path
	return result, nil
}

// ResolveHotfixWorktree accepts only a server-derived hotfix branch and its
// corresponding state-owned checkout. A same-named arbitrary worktree cannot
// be used as a hotfix lane.
func (r Runner) ResolveHotfixWorktree(ctx context.Context, p config.ProjectConfig, stateDir, projectID, ref string) (config.ProjectConfig, error) {
	slug, err := hotfixSlugFromRef(ref)
	if err != nil {
		return config.ProjectConfig{}, err
	}
	expected, _, err := hotfixWorktreePath(stateDir, projectID, slug)
	if err != nil {
		return config.ProjectConfig{}, err
	}
	worktree, err := r.ResolveWorktree(ctx, p, ref)
	if err != nil {
		return config.ProjectConfig{}, err
	}
	actual, err := filepath.Abs(worktree.Root)
	if err != nil || filepath.Clean(actual) != filepath.Clean(expected) {
		return config.ProjectConfig{}, fmt.Errorf("hotfix worktree is not server-owned")
	}
	worktree.Root = actual
	return worktree, nil
}

func hotfixWorktreePath(stateDir, projectID, slug string) (string, string, error) {
	if err := model.ValidateProjectIdentifier(projectID); err != nil {
		return "", "", err
	}
	if err := model.ValidateTaskSlug(slug); err != nil {
		return "", "", err
	}
	if stateDir == "" || !filepath.IsAbs(stateDir) || strings.ContainsAny(stateDir, "\x00\r\n") {
		return "", "", fmt.Errorf("invalid hotfix state directory")
	}
	branch := "hotfix/" + slug
	if err := model.ValidateBranch(branch); err != nil {
		return "", "", err
	}
	return filepath.Join(stateDir, "hotfix-worktrees", projectID, slug), branch, nil
}

func hotfixSlugFromRef(ref string) (string, error) {
	const prefix = "refs/heads/hotfix/"
	if !strings.HasPrefix(ref, prefix) {
		return "", fmt.Errorf("hotfix_ref must be a server-owned hotfix branch ref")
	}
	slug := strings.TrimPrefix(ref, prefix)
	if strings.ContainsRune(slug, '/') {
		return "", fmt.Errorf("hotfix_ref must contain one bounded slug")
	}
	if err := model.ValidateTaskSlug(slug); err != nil {
		return "", err
	}
	if err := model.ValidateBranch(ref); err != nil {
		return "", err
	}
	return slug, nil
}
