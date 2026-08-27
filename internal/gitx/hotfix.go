package gitx

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

const hotfixIdentityMaxBytes = 4 << 10

// HotfixIdentity is the server-owned create record used to authenticate the
// base of a later integration. Callers cannot supply or replace BaseSHA.
type HotfixIdentity struct {
	ProjectID string `json:"project_id"`
	HotfixRef string `json:"hotfix_ref"`
	BaseSHA   string `json:"base_sha"`
}

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

// RecordHotfixIdentity persists the exact create identity in Gateway-owned
// state. It is create-once; an existing identity is never overwritten.
func (r Runner) RecordHotfixIdentity(stateDir string, identity HotfixIdentity) error {
	slug, err := hotfixSlugFromRef(identity.HotfixRef)
	if err != nil {
		return err
	}
	if err := model.ValidateProjectIdentifier(identity.ProjectID); err != nil {
		return err
	}
	if err := model.ValidateCommitSHA(identity.BaseSHA); err != nil {
		return fmt.Errorf("hotfix base: %w", err)
	}
	path, err := hotfixIdentityPath(stateDir, identity.ProjectID, slug)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("hotfix identity already exists")
	} else if !os.IsNotExist(err) {
		return err
	}
	return fsutil.WriteJSONAtomic(path, identity, 0o600)
}

// ReadHotfixIdentity reads the immutable server-owned create identity.
func (r Runner) ReadHotfixIdentity(stateDir, projectID, ref string) (HotfixIdentity, error) {
	slug, err := hotfixSlugFromRef(ref)
	if err != nil {
		return HotfixIdentity{}, err
	}
	if err := model.ValidateProjectIdentifier(projectID); err != nil {
		return HotfixIdentity{}, err
	}
	path, err := hotfixIdentityPath(stateDir, projectID, slug)
	if err != nil {
		return HotfixIdentity{}, err
	}
	var identity HotfixIdentity
	if err := fsutil.ReadJSONBounded(path, hotfixIdentityMaxBytes, &identity); err != nil {
		return HotfixIdentity{}, err
	}
	if identity.ProjectID != projectID || identity.HotfixRef != ref || model.ValidateCommitSHA(identity.BaseSHA) != nil {
		return HotfixIdentity{}, fmt.Errorf("hotfix identity is invalid or mismatched")
	}
	return identity, nil
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

func hotfixIdentityPath(stateDir, projectID, slug string) (string, error) {
	if stateDir == "" || !filepath.IsAbs(stateDir) || strings.ContainsAny(stateDir, "\x00\r\n") {
		return "", fmt.Errorf("invalid hotfix state directory")
	}
	return filepath.Join(stateDir, "hotfix-identities", projectID, slug+".json"), nil
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
