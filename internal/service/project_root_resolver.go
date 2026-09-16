package service

import (
	"context"
	"fmt"
	"path/filepath"
)

func (s *Service) ProjectIDForRoot(ctx context.Context, root string) (string, error) {
	if root == "" {
		var err error
		root, err = filepath.Abs(".")
		if err != nil {
			return "", err
		}
	}
	resolved, err := s.Git.RepositoryRoot(ctx, root)
	if err != nil {
		return "", err
	}
	resolved, err = filepath.EvalSymlinks(resolved)
	if err != nil {
		return "", fmt.Errorf("canonicalize repository root: %w", err)
	}
	resolution, err := s.EffectiveProjectSnapshot()
	if err != nil {
		return "", err
	}
	projectID := ""
	for id, project := range resolution.Projects {
		projectRoot, rootErr := filepath.EvalSymlinks(project.Root)
		if rootErr != nil {
			continue
		}
		if projectRoot != resolved {
			continue
		}
		if projectID != "" && projectID != id {
			return "", fmt.Errorf("repository root is ambiguously bound to projects %q and %q", projectID, id)
		}
		projectID = id
	}
	if projectID == "" {
		return "", fmt.Errorf("current repository is not an onboarded project")
	}
	return projectID, nil
}
