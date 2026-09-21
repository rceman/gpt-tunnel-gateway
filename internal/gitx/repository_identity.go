package gitx

import (
	"context"
	"fmt"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
)

func (r Runner) RepositoryRemoteURL(ctx context.Context, root, remote string) (string, error) {
	if strings.TrimSpace(root) == "" || strings.TrimSpace(remote) == "" {
		return "", fmt.Errorf("repository identity requires root and remote")
	}
	return r.RemoteURL(ctx, config.ProjectConfig{Root: root, Remote: remote})
}

func (r Runner) RepositoryRemotes(ctx context.Context, root string) (map[string]string, error) {
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("repository identity requires root")
	}
	out, err := r.repositoryRemoteNames(ctx, root)
	if err != nil {
		return nil, err
	}
	remotes := make(map[string]string, len(out))
	for _, name := range out {
		url, err := r.RepositoryRemoteURL(ctx, root, name)
		if err != nil {
			return nil, err
		}
		remotes[name] = strings.TrimSpace(url)
	}
	if len(remotes) == 0 {
		return nil, fmt.Errorf("repository has no remotes")
	}
	return remotes, nil
}

func (r Runner) repositoryRemoteNames(ctx context.Context, root string) ([]string, error) {
	out, err := r.command(ctx, root, false, "remote")
	if err != nil {
		return nil, fmt.Errorf("list Git remotes: %w", err)
	}
	return strings.Fields(string(out)), nil
}
