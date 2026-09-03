package gitx

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
)

func (r Runner) EnsureMirror(ctx context.Context, p config.ProjectConfig) error {
	if _, err := os.Stat(filepath.Join(p.Mirror, "HEAD")); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p.Mirror), 0o700); err != nil {
		return err
	}
	urlOut, err := r.command(ctx, p.Root, false, "remote", "get-url", p.Remote)
	if err != nil {
		return err
	}
	url := strings.TrimSpace(string(urlOut))
	if url == "" {
		return fmt.Errorf("empty project remote URL")
	}
	cmd := exec.CommandContext(ctx, "git", "clone", "--mirror", "--", url, p.Mirror)
	cmd.Env = cleanEnv()
	stderr := boundedCommandBuffer{limit: r.MaxReadBytes}
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if stderr.exceeded {
			return fmt.Errorf("create mirror output exceeds %d bytes", r.MaxReadBytes)
		}
		return fmt.Errorf("create mirror: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	if stderr.exceeded {
		return fmt.Errorf("create mirror output exceeds %d bytes", r.MaxReadBytes)
	}
	return nil
}

// ReconcileManagedMirror creates or reuses only the configured managed mirror,
// verifies its repository identity, refreshes it once, and resolves the exact
// configured default branch. It never writes the source worktree.
func (r Runner) ReconcileManagedMirror(ctx context.Context, p config.ProjectConfig, expectedURL, defaultBranch string) (MirrorVerification, error) {
	info, err := os.Lstat(p.Mirror)
	created := false
	working := p
	temporaryMirror := ""
	if err != nil {
		if !os.IsNotExist(err) {
			return MirrorVerification{}, err
		}
		parent := filepath.Dir(p.Mirror)
		if err := os.MkdirAll(parent, 0o700); err != nil {
			return MirrorVerification{}, err
		}
		parentInfo, err := os.Lstat(parent)
		if err != nil {
			return MirrorVerification{}, err
		}
		if parentInfo.Mode()&os.ModeSymlink != 0 || !parentInfo.IsDir() {
			return MirrorVerification{}, fmt.Errorf("managed mirror parent must be a real directory")
		}
		temporaryMirror, err = os.MkdirTemp(parent, "."+filepath.Base(p.Mirror)+".onboarding-")
		if err != nil {
			return MirrorVerification{}, err
		}
		defer os.RemoveAll(temporaryMirror)
		if err := os.Remove(temporaryMirror); err != nil {
			return MirrorVerification{}, err
		}
		working.Mirror = temporaryMirror
		if err := r.EnsureMirror(ctx, working); err != nil {
			return MirrorVerification{}, err
		}
		created = true
	} else {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return MirrorVerification{}, fmt.Errorf("managed mirror must be a real directory")
		}
		if headInfo, err := os.Stat(filepath.Join(p.Mirror, "HEAD")); err != nil || !headInfo.Mode().IsRegular() {
			return MirrorVerification{}, fmt.Errorf("managed mirror is not a valid Git mirror")
		}
	}
	urlOut, err := r.command(ctx, working.Mirror, true, "remote", "get-url", p.Remote)
	if err != nil {
		return MirrorVerification{}, err
	}
	actualURL := strings.TrimSpace(string(urlOut))
	if actualURL != expectedURL {
		return MirrorVerification{}, fmt.Errorf("managed mirror repository URL mismatch")
	}
	if err := r.Refresh(ctx, working); err != nil {
		return MirrorVerification{}, err
	}
	head, exists, err := r.MirrorBranchHead(ctx, working, defaultBranch)
	if err != nil {
		return MirrorVerification{}, err
	}
	if !exists || !isCommitSHA(head) {
		return MirrorVerification{}, fmt.Errorf("managed mirror default branch is unavailable")
	}
	if temporaryMirror != "" {
		if _, err := os.Lstat(p.Mirror); err == nil {
			return MirrorVerification{}, fmt.Errorf("managed mirror target appeared during atomic activation")
		} else if !os.IsNotExist(err) {
			return MirrorVerification{}, err
		}
		if err := os.Rename(temporaryMirror, p.Mirror); err != nil {
			return MirrorVerification{}, fmt.Errorf("install managed mirror atomically: %w", err)
		}
	}
	return MirrorVerification{
		Path:          filepath.Clean(p.Mirror),
		RepositoryURL: actualURL,
		Head:          head,
		Created:       created,
	}, nil
}

// RemoteURL and RemoteDefaultBranch are bounded metadata reads used by
// target-runtime preflight. They never fetch, update, or mutate a worktree.
func (r Runner) RemoteURL(ctx context.Context, p config.ProjectConfig) (string, error) {
	out, err := r.command(ctx, p.Root, false, "remote", "get-url", p.Remote)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (r Runner) RemoteDefaultBranch(ctx context.Context, p config.ProjectConfig) (string, error) {
	out, err := r.command(ctx, p.Root, false, "symbolic-ref", "--quiet", "refs/remotes/"+p.Remote+"/HEAD")
	if err != nil {
		return "", err
	}
	ref := strings.TrimSpace(string(out))
	prefix := "refs/remotes/" + p.Remote + "/"
	if !strings.HasPrefix(ref, prefix) || strings.TrimPrefix(ref, prefix) == "" {
		return "", fmt.Errorf("remote HEAD is not under %s", prefix)
	}
	return strings.TrimPrefix(ref, prefix), nil
}
func (r Runner) Refresh(ctx context.Context, p config.ProjectConfig) error {
	if err := r.EnsureMirror(ctx, p); err != nil {
		return err
	}
	out, err := r.command(ctx, p.Mirror, true, "remote", "update", "--prune")
	if err != nil {
		return err
	}
	_, err = bounded(out, r.MaxReadBytes)
	return err
}
