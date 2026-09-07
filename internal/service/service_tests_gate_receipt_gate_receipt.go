package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"

	"github.com/rceman/gpt-tunnel-gateway/internal/gates"
	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s *Service) invalidateTestPassReceipt(projectID string) error {
	path, err := s.testPassReceiptPath(projectID)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (s *Service) executeProjectGatesWithTestReuse(ctx context.Context, projectID, root string, gateNames []string, scope gates.TestScope) ([]model.CompletionGateResult, error) {
	if !containsGate(gateNames, model.WorkflowGateTest) {
		results, err := s.executeGateNames(ctx, root, gateNames)
		return annotateExecutedGateResults(results), err
	}
	normalizedScope, scopeErr := scope.Normalize()
	tree, _, identityErr := s.currentTestIdentity(ctx, projectID, root)
	contract, contractErr := gates.TestGateCommandContractDigest(gateNames, normalizedScope)
	var reused model.CompletionGateResult
	reusable := false
	if identityErr == nil && scopeErr == nil && contractErr == nil {
		if receipt, receiptDigest, err := s.loadTestPassReceipt(projectID); err == nil && receipt.ProjectID == projectID && receipt.TreeID == tree && receipt.ScopeMode == normalizedScope.Mode && reflect.DeepEqual(receipt.ScopePackages, normalizedScope.Packages) && receipt.ContractDigest == contract {
			reused = model.CompletionGateResult{ID: model.WorkflowGateTest, ExitCode: 0, Execution: "reused", TreeID: receipt.TreeID, ContractDigest: receipt.ContractDigest, ReceiptDigest: receiptDigest}
			reusable = true
		}
	}
	if !reusable {
		if err := s.invalidateTestPassReceipt(projectID); err != nil {
			return nil, err
		}
		results, err := s.executeGateNamesWithScope(ctx, root, gateNames, normalizedScope)
		if err != nil {
			return results, err
		}
		results = annotateExecutedGateResults(results)
		receipt, receiptDigest, err := s.writeTestPassReceiptLocked(ctx, projectID, root, gateNames, normalizedScope)
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

	nonTest := make([]string, 0, len(gateNames)-1)
	for _, name := range gateNames {
		if name != model.WorkflowGateTest {
			nonTest = append(nonTest, name)
		}
	}
	var nonTestResults []model.CompletionGateResult
	if len(nonTest) > 0 {
		results, err := s.executeGateNames(ctx, root, nonTest)
		if err != nil {
			return results, err
		}
		nonTestResults = annotateExecutedGateResults(results)
	}
	results := make([]model.CompletionGateResult, 0, len(gateNames))
	for _, name := range gateNames {
		if name == model.WorkflowGateTest {
			results = append(results, reused)
			continue
		}
		for i := range nonTestResults {
			if nonTestResults[i].ID == name {
				results = append(results, nonTestResults[i])
				break
			}
		}
	}
	return results, nil
}

func containsGate(gates []string, wanted string) bool {
	for _, gate := range gates {
		if gate == wanted {
			return true
		}
	}
	return false
}

func annotateExecutedGateResults(results []model.CompletionGateResult) []model.CompletionGateResult {
	for i := range results {
		results[i].Execution = "executed"
	}
	return results
}

func (s *Service) ExecuteCanonicalTestGate(ctx context.Context, root string) error {
	projectID, err := s.projectIDForRoot(root)
	if err != nil {
		return err
	}
	if projectID == "" {
		_, err := s.executeGateNames(ctx, root, []string{model.WorkflowGateTest})
		return err
	}
	gateNames, err := s.ResolveProjectGates(ctx, projectID, "implementation")
	if err != nil {
		return err
	}
	lock, err := lockfile.Acquire(filepath.Join(s.Config.StateDir, "locks"), "project-"+projectID)
	if err != nil {
		return err
	}
	defer lock.Release()
	if err := s.invalidateTestPassReceipt(projectID); err != nil {
		return err
	}
	if _, err := s.executeGateNames(ctx, root, []string{model.WorkflowGateTest}); err != nil {
		return err
	}
	_, _, _ = s.writeTestPassReceiptLocked(ctx, projectID, root, gateNames, gates.FullTestScope())
	return nil
}
