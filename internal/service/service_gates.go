package service

import (
	"context"
	"fmt"
	"reflect"

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
	mode := "train"
	if operationClass == "" || operationClass == "implementation" || operationClass == "correction" {
		mode = "task"
	}
	return s.executeProjectGatesWithProjectCommands(ctx, projectID, root, names, mode)
}

func (s *Service) executeProjectGatesWithProjectCommands(ctx context.Context, projectID, root string, names []string, testMode string) ([]model.CompletionGateResult, error) {
	return s.executeProjectGatesWithProjectCommandsAndScope(ctx, projectID, root, names, testMode, gates.FullTestScope())
}

func (s *Service) executeProjectGatesWithProjectCommandsAndScope(ctx context.Context, projectID, root string, names []string, testMode string, scope gates.TestScope) ([]model.CompletionGateResult, error) {
	configuration, err := s.ProjectConfigurationRead(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if s.gateExecutorWithProjectCommands == nil && s.gateExecutorWithProjectCommandsAndScope == nil {
		return nil, fmt.Errorf("project gate executor is not configured")
	}
	if testMode == "task" && containsGate(names, model.WorkflowGateTest) {
		results, err := s.executeProjectTaskGatesWithTestReuse(ctx, projectID, root, names, configuration.Workflow.GateCommands, scope)
		if err != nil {
			return results, err
		}
		if err := validateProjectGateEvidence(results, names); err != nil {
			return nil, err
		}
		return results, nil
	}
	results, err := s.executeProjectGatesCommandSet(ctx, root, names, configuration.Workflow.GateCommands, testMode, scope)
	if err != nil {
		return results, err
	}
	if err := validateProjectGateEvidence(results, names); err != nil {
		return nil, err
	}
	return results, nil
}

func (s *Service) executeProjectGatesCommandSet(ctx context.Context, root string, names []string, commands model.ProjectGateCommands, testMode string, scope gates.TestScope) ([]model.CompletionGateResult, error) {
	normalized, err := scope.Normalize()
	if err != nil {
		return nil, err
	}
	if normalized.Mode != gates.TestScopeFull && s.gateExecutorWithProjectCommandsAndScope != nil {
		return s.gateExecutorWithProjectCommandsAndScope(ctx, root, names, commands, testMode, scope)
	}
	if s.gateExecutorWithProjectCommands == nil {
		return nil, fmt.Errorf("project gate executor is not configured")
	}
	return s.gateExecutorWithProjectCommands(ctx, root, names, commands, testMode)
}

func (s *Service) executeProjectTaskGatesWithTestReuse(ctx context.Context, projectID, root string, names []string, commands model.ProjectGateCommands, scope gates.TestScope) ([]model.CompletionGateResult, error) {
	normalized, scopeErr := scope.Normalize()
	if scopeErr != nil {
		normalized = gates.FullTestScope()
	}
	tree, _, identityErr := s.currentTestIdentity(ctx, projectID, root)
	contract, contractErr := gates.TestGateCommandContractDigest(names, normalized)
	var reused model.CompletionGateResult
	if identityErr == nil && scopeErr == nil && contractErr == nil {
		if receipt, receiptDigest, err := s.loadTestPassReceipt(projectID); err == nil && receipt.ProjectID == projectID && receipt.TreeID == tree && receipt.ScopeMode == normalized.Mode && reflect.DeepEqual(receipt.ScopePackages, normalized.Packages) && receipt.ContractDigest == contract {
			reused = model.CompletionGateResult{ID: model.WorkflowGateTest, ExitCode: 0, Execution: "reused", TreeID: receipt.TreeID, ContractDigest: receipt.ContractDigest, ReceiptDigest: receiptDigest}
		}
	}
	if reused.ID == "" {
		if err := s.invalidateTestPassReceipt(projectID); err != nil {
			return nil, err
		}
		results, err := s.executeProjectGatesCommandSet(ctx, root, names, commands, "task", normalized)
		if err != nil {
			return results, err
		}
		results = annotateExecutedGateResults(results)
		receipt, receiptDigest, err := s.writeTestPassReceiptLocked(ctx, projectID, root, names, normalized)
		if err != nil {
			return nil, fmt.Errorf("store test pass receipt: %w", err)
		}
		for i := range results {
			if results[i].ID == model.WorkflowGateTest {
				results[i].TreeID = receipt.TreeID
				results[i].ContractDigest = receipt.ContractDigest
				results[i].ReceiptDigest = receiptDigest
			}
		}
		return results, nil
	}
	nonTest := make([]string, 0, len(names)-1)
	for _, name := range names {
		if name != model.WorkflowGateTest {
			nonTest = append(nonTest, name)
		}
	}
	var nonTestResults []model.CompletionGateResult
	if len(nonTest) > 0 {
		results, err := s.executeProjectGatesCommandSet(ctx, root, nonTest, commands, "task", gates.FullTestScope())
		if err != nil {
			return results, err
		}
		nonTestResults = annotateExecutedGateResults(results)
	}
	results := make([]model.CompletionGateResult, 0, len(names))
	for _, name := range names {
		if name == model.WorkflowGateTest {
			results = append(results, reused)
			continue
		}
		for _, result := range nonTestResults {
			if result.ID == name {
				results = append(results, result)
				break
			}
		}
	}
	return results, nil
}

// resolveFinalizationTestScope is the server-owned policy boundary for
// ordinary Task finalization. Broad operation classes never use a package
// subset; implementation and correction tasks may use the conservative
// changed-file resolver, which returns full-suite scope on uncertainty.
func resolveFinalizationTestScope(ctx context.Context, operationClass, root string, changedFiles []string) gates.TestScope {
	switch operationClass {
	case "", "implementation", "correction":
	default:
		return gates.FullTestScope()
	}
	scope, err := gates.ResolveTestScope(ctx, root, changedFiles)
	if err != nil {
		return gates.FullTestScope()
	}
	return scope
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
	if normalized.Mode == gates.TestScopeFull {
		return s.executeGateNames(ctx, root, names)
	}
	if s.gateExecutorWithScope == nil {
		return nil, fmt.Errorf("scoped gate executor is not configured")
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
