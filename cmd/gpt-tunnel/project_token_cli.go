package main

import (
	"context"
	"fmt"
	"os"

	"github.com/rceman/gpt-tunnel-gateway/internal/gitx"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func projectTokenGatewayCall(ctx context.Context, s *service.Service, projectCode string) (string, error) {
	input := service.ProjectTokenInput{Project: projectCode}
	if projectCode == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("resolve current directory: %w", err)
		}
		maxReadBytes := s.Config.MaxReadBytes
		if maxReadBytes <= 0 {
			maxReadBytes = 1 << 20
		}
		git := gitx.Runner{MaxReadBytes: maxReadBytes}
		root, err := git.RepositoryRoot(ctx, cwd)
		if err != nil {
			return "", fmt.Errorf("current directory is not a Git repository: %w", err)
		}
		remotes, err := git.RepositoryRemotes(ctx, root)
		if err != nil {
			return "", fmt.Errorf("resolve current repository identity: %w", err)
		}
		input.Root = root
		input.Remotes = remotes
	}
	result, err := operatorCLIRequest[service.ProjectTokenResult](ctx, s.Config, "/operator/project/token", input)
	if err != nil {
		return "", err
	}
	if result.Token == "" {
		return "", fmt.Errorf("project token request returned no token")
	}
	return result.Token, nil
}
