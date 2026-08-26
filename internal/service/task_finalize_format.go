package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func (s *Service) taskFinalizeChangedFiles(ctx context.Context, root, baseHead, candidateHead string) ([]string, error) {
	committed, err := s.Git.ChangedFiles(ctx, root, baseHead, candidateHead)
	if err != nil {
		return nil, err
	}
	working, err := s.Git.ChangedWorkingFiles(ctx, root)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(committed)+len(working))
	changed := make([]string, 0, len(committed)+len(working))
	for _, path := range append(committed, working...) {
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		changed = append(changed, path)
	}
	sort.Strings(changed)
	return changed, nil
}

func existingGoFiles(root string, paths []string) ([]string, error) {
	goPaths := make([]string, 0, len(paths))
	for _, path := range paths {
		if !strings.HasSuffix(path, ".go") {
			continue
		}
		info, err := os.Stat(filepath.Join(root, filepath.FromSlash(path)))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("stat changed Go file %q: %w", path, err)
		}
		if info.Mode().IsRegular() {
			goPaths = append(goPaths, path)
		}
	}
	return goPaths, nil
}
