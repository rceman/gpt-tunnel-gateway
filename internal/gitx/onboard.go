package gitx

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
)

func (r Runner) Clone(ctx context.Context, repositoryURL, destination string) error {
	if repositoryURL == "" || destination == "" || !filepath.IsAbs(destination) || filepath.Clean(destination) != destination {
		return fmt.Errorf("invalid clone destination")
	}
	if _, err := os.Lstat(destination); err == nil {
		return fmt.Errorf("clone destination already exists")
	} else if !os.IsNotExist(err) {
		return err
	}
	parent := filepath.Dir(destination)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return fmt.Errorf("prepare clone root: %w", err)
	}
	_, err := r.command(ctx, parent, false, "clone", "--origin", "origin", repositoryURL, destination)
	if err != nil {
		return fmt.Errorf("clone repository: %w", err)
	}
	return nil
}

func (r Runner) RepositoryHasCommit(ctx context.Context, p config.ProjectConfig) (bool, error) {
	if _, err := r.command(ctx, p.Root, false, "rev-parse", "--verify", "HEAD"); err == nil {
		return true, nil
	}
	out, err := r.command(ctx, p.Root, false, "rev-parse", "--is-inside-work-tree")
	if err != nil || strings.TrimSpace(string(out)) != "true" {
		if err != nil {
			return false, err
		}
		return false, fmt.Errorf("repository is not a valid worktree")
	}
	return false, nil
}

func (r Runner) BootstrapEmpty(ctx context.Context, p config.ProjectConfig, readme string) error {
	if p.Root == "" || !filepath.IsAbs(p.Root) || filepath.Clean(p.Root) != p.Root || p.Remote == "" {
		return fmt.Errorf("invalid empty repository bootstrap target")
	}
	hasCommit, err := r.RepositoryHasCommit(ctx, p)
	if err != nil {
		return fmt.Errorf("inspect empty repository: %w", err)
	}
	if hasCommit {
		return fmt.Errorf("repository is not empty")
	}
	entries, err := os.ReadDir(p.Root)
	if err != nil {
		return fmt.Errorf("inspect empty repository files: %w", err)
	}
	for _, entry := range entries {
		if entry.Name() != ".git" {
			return fmt.Errorf("empty repository contains unexpected files")
		}
	}
	if err := os.WriteFile(filepath.Join(p.Root, "README.md"), []byte(readme), 0o644); err != nil {
		return fmt.Errorf("write bootstrap README: %w", err)
	}
	if _, err := r.command(ctx, p.Root, false, "switch", "--orphan", "main"); err != nil {
		return fmt.Errorf("create main branch: %w", err)
	}
	if _, err := r.command(ctx, p.Root, false, "add", "--", "README.md"); err != nil {
		return fmt.Errorf("stage bootstrap README: %w", err)
	}
	if _, err := r.command(ctx, p.Root, false, "-c", "user.name=GPT Tunnel Gateway", "-c", "user.email=gpt-tunnel-gateway@localhost.invalid", "commit", "--only", "-m", "Initialize repository", "--", "README.md"); err != nil {
		return fmt.Errorf("create bootstrap commit: %w", err)
	}
	if _, err := r.command(ctx, p.Root, false, "push", p.Remote, "HEAD:refs/heads/main"); err != nil {
		return fmt.Errorf("publish bootstrap commit: %w", err)
	}
	if _, err := r.command(ctx, p.Root, false, "remote", "set-head", p.Remote, "main"); err != nil {
		return fmt.Errorf("set bootstrap default branch: %w", err)
	}
	return nil
}
