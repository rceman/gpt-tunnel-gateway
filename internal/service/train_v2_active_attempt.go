package service

import (
	"context"
	"os"
	"path/filepath"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

// activeTrainAttemptInWorktree is the post-cutover replacement for the old
// /runs scan. It reads only canonical Train-v2 items and treats an active
// Attempt as execution ownership that must not be invalidated by a
// configuration mutation.
func activeTrainAttemptInWorktree(worktree, projectID string) (bool, error) {
	root := filepath.Join(worktree, filepath.FromSlash(hubTrainRoot(projectID)))
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	for _, entry := range entries {
		if entry.IsDir() || !canonicalTrainV2RecordName(entry.Name()) {
			continue
		}
		var train model.TrainV2
		if err := readWorktreeJSON(worktree, filepath.ToSlash(filepath.Join(hubTrainRoot(projectID), entry.Name())), &train); err != nil {
			return false, err
		}
		if err := model.ValidateTrainV2(train); err != nil {
			return false, err
		}
		for _, item := range train.Items {
			for _, attempt := range item.Attempts {
				if attempt.Status == model.TrainV2AttemptRunning {
					return true, nil
				}
			}
		}
	}
	return false, nil
}

func hubTrainRoot(projectID string) string {
	return "gpt-tunnel/v1/projects/" + projectID + "/trains-v2"
}

func (s *Service) projectHasActiveTrainAttempt(ctx context.Context, projectID string) (bool, error) {
	paths, err := s.Hub.List(ctx, s.trainV2Root(projectID), ".json")
	if err != nil {
		if IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	for _, path := range paths {
		if !canonicalTrainV2RecordName(filepath.Base(path)) {
			continue
		}
		var train model.TrainV2
		if err := s.Hub.ReadJSON(ctx, path, &train); err != nil {
			return false, err
		}
		if err := model.ValidateTrainV2(train); err != nil {
			return false, err
		}
		for _, item := range train.Items {
			for _, attempt := range item.Attempts {
				if attempt.Status == model.TrainV2AttemptRunning {
					return true, nil
				}
			}
		}
	}
	return false, nil
}
