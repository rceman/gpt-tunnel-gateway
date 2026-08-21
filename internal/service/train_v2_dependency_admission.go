package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

// validateTaskDependenciesInWorktree checks dependency authority against the
// same immutable Hub snapshot used by Train admission. Review, finalization,
// and a FullProof alone are not integration authority: the matching completed
// IntegrationReceipt must bind the integrated head to that FullProof.
func (s *Service) validateTaskDependenciesInWorktree(worktree, projectID string, tasks []model.TaskAuthoring) error {
	if len(tasks) == 0 {
		return nil
	}
	root := filepath.Join(worktree, filepath.FromSlash(s.trainV2Root(projectID)))
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return validateTaskDependenciesAgainstTrains(worktree, projectID, nil, tasks)
		}
		return err
	}
	trains := make([]model.TrainV2, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !canonicalTrainV2RecordName(entry.Name()) {
			continue
		}
		var train model.TrainV2
		path := filepath.ToSlash(filepath.Join(s.trainV2Root(projectID), entry.Name()))
		if err := readWorktreeJSON(worktree, path, &train); err != nil {
			return err
		}
		if err := model.ValidateTrainV2(train); err != nil {
			return err
		}
		trains = append(trains, train)
	}
	return validateTaskDependenciesAgainstTrains(worktree, projectID, trains, tasks)
}

func validateTaskDependenciesAgainstTrains(worktree, projectID string, trains []model.TrainV2, tasks []model.TaskAuthoring) error {
	for _, task := range tasks {
		for _, dependencyID := range task.Dependencies {
			integrated, err := dependencyHasCanonicalIntegration(worktree, projectID, trains, dependencyID)
			if err != nil {
				return fmt.Errorf("dependency-not-integrated: Task %q depends on %q: %w", task.ID, dependencyID, err)
			}
			if !integrated {
				return fmt.Errorf("dependency-not-integrated: Task %q depends on %q without canonical integrated implementation", task.ID, dependencyID)
			}
		}
	}
	return nil
}

func dependencyHasCanonicalIntegration(worktree, projectID string, trains []model.TrainV2, dependencyID string) (bool, error) {
	for _, train := range trains {
		for _, item := range train.Items {
			if item.TaskID != dependencyID {
				continue
			}
			if train.Status != model.TrainV2Completed || train.FullProof == nil || item.Proof == nil || item.Proof.ImplementationSHA == "" || item.Proof.ImplementationSHA != train.FullProof.CandidateHead {
				continue
			}
			var receipt trainv2.IntegrationReceipt
			path := filepath.ToSlash(filepath.Join("gpt-tunnel/v1/projects", projectID, "trains-v2", train.ID+".integration.json"))
			if err := readWorktreeJSON(worktree, path, &receipt); err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return false, err
			}
			if err := trainv2.ValidateIntegrationReceipt(receipt); err != nil {
				return false, err
			}
			if receipt.ProjectID == projectID && receipt.TrainID == train.ID && receipt.Status == "completed" && receipt.IntegrationHead == train.FullProof.CandidateHead {
				return true, nil
			}
		}
	}
	return false, nil
}

func (s *Service) validateTaskDependencies(ctx context.Context, projectID string, task model.TaskAuthoring) error {
	if s.Durability != nil {
		return s.validateTaskDependenciesShared(ctx, projectID, task)
	}
	if len(task.Dependencies) == 0 {
		return nil
	}
	trains, err := s.readTrainV2Records(ctx, projectID)
	if err != nil {
		return err
	}
	for _, dependencyID := range task.Dependencies {
		integrated, err := s.dependencyHasCanonicalIntegration(ctx, projectID, trains, dependencyID)
		if err != nil {
			return fmt.Errorf("dependency-not-integrated: Task %q depends on %q: %w", task.ID, dependencyID, err)
		}
		if !integrated {
			return fmt.Errorf("dependency-not-integrated: Task %q depends on %q without canonical integrated implementation", task.ID, dependencyID)
		}
	}
	return nil
}

func (s *Service) dependencyHasCanonicalIntegration(ctx context.Context, projectID string, trains []model.TrainV2, dependencyID string) (bool, error) {
	for _, train := range trains {
		for _, item := range train.Items {
			if item.TaskID != dependencyID || train.Status != model.TrainV2Completed || train.FullProof == nil || item.Proof == nil || item.Proof.ImplementationSHA != train.FullProof.CandidateHead {
				continue
			}
			receipt, err := s.readTrainV2IntegrationReceipt(ctx, projectID, train.ID)
			if err != nil {
				if IsNotFound(err) {
					continue
				}
				return false, err
			}
			if receipt.Status == "completed" && receipt.IntegrationHead == train.FullProof.CandidateHead {
				return true, nil
			}
		}
	}
	return false, nil
}
