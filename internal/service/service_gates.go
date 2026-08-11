package service

import (
	"context"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/gates"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s *Service) ResolveProjectGates(ctx context.Context, projectID, operationClass string) ([]string, error) {
	if operationClass == "" {
		operationClass = "implementation"
	}
	policy, err := s.ProjectWorkflowPolicyRead(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("project workflow policy is required: %w", err)
	}
	effective, err := model.WorkflowPolicyForOperation(policy, operationClass)
	if err != nil {
		return nil, err
	}
	return append([]string{}, effective.Gates...), nil
}

func (s *Service) ExecuteProjectGates(ctx context.Context, projectID, operationClass, root string) ([]model.CompletionGateResult, error) {
	names, err := s.ResolveProjectGates(ctx, projectID, operationClass)
	if err != nil {
		return nil, err
	}
	return s.executeGateNames(ctx, root, names)
}

func (s *Service) executeGateNames(ctx context.Context, root string, names []string) ([]model.CompletionGateResult, error) {
	if s.gateExecutor == nil {
		return nil, fmt.Errorf("project gate executor is not configured")
	}
	results, err := s.gateExecutor(ctx, root, names)
	if err != nil {
		return results, err
	}
	for i := range results {
		if i >= len(names) || results[i].ID != names[i] {
			return nil, fmt.Errorf("gate executor returned unexpected evidence")
		}
	}
	return results, nil
}

func (s *Service) executeGateNamesWithScope(ctx context.Context, root string, names []string, scope gates.TestScope) ([]model.CompletionGateResult, error) {
	normalized, err := scope.Normalize()
	if err != nil {
		return nil, err
	}
	if normalized.Mode == gates.TestScopeFull || s.gateExecutorWithScope == nil {
		return s.executeGateNames(ctx, root, names)
	}
	results, err := s.gateExecutorWithScope(ctx, root, names, normalized)
	if err != nil {
		return results, err
	}
	for i := range results {
		if i >= len(names) || results[i].ID != names[i] {
			return nil, fmt.Errorf("gate executor returned unexpected evidence")
		}
	}
	return results, nil
}

func validateProjectGateEvidence(results []model.CompletionGateResult, expected []string) error {
	if err := model.ValidateServerGateEvidence(results); err != nil {
		return err
	}
	if len(results) != len(expected) {
		return fmt.Errorf("server gate evidence does not match effective project policy")
	}
	for i := range results {
		if results[i].ID != expected[i] {
			return fmt.Errorf("server gate evidence does not match effective project policy")
		}
	}
	return nil
}
