package service

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

// projectHasActiveTaskExecution reports whether the project owns a
// nonterminal canonical Task execution. The Task-execution state table is the
// only execution-ownership authority; historical Train/Attempt records are
// evidence and are never consulted.
func (s *Service) projectHasActiveTaskExecution(ctx context.Context, projectID string) (bool, error) {
	if s.Durability == nil {
		return false, nil
	}
	states, err := s.Durability.ListTaskExecutionStates(ctx, projectID)
	if err != nil {
		return false, err
	}
	for _, state := range states {
		if model.IsTaskExecutionNonTerminal(state.Status) {
			return true, nil
		}
	}
	return false, nil
}

// projectOperationalOperation projects the newest nonterminal durable
// operation for the project status surface.
func (s *Service) projectOperationalOperation(projectID string) *ProjectOperationalOperation {
	dir := filepath.Join(s.Config.StateDir, "operations", "mutations")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var candidates []durableMutationOperation
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		operation, readErr := s.readDurableMutation(strings.TrimSuffix(entry.Name(), ".json"))
		if readErr != nil || operation.ProjectID != projectID || operation.Status == "completed" || operation.Status == "failed" || operation.Status == "outcome_unknown" {
			continue
		}
		candidates = append(candidates, operation)
	}
	if len(candidates) == 0 {
		return nil
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].UpdatedAt.After(candidates[j].UpdatedAt) })
	return &ProjectOperationalOperation{
		Kind:        candidates[0].Kind,
		OperationID: candidates[0].OperationID,
		Status:      candidates[0].Status,
	}
}
