package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/gates"
	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

const (
	testPassReceiptLegacySchemaVersion = 2
	testPassReceiptSchemaVersion       = 3
)

type testPassReceipt struct {
	SchemaVersion  int                          `json:"schema_version"`
	ProjectID      string                       `json:"project_id"`
	TreeID         string                       `json:"tree_id"`
	Head           string                       `json:"head"`
	GateNames      []string                     `json:"gate_names"`
	ScopeMode      string                       `json:"scope_mode"`
	ScopePackages  []string                     `json:"scope_packages,omitempty"`
	RunnerContract string                       `json:"runner_contract_version"`
	ContractDigest string                       `json:"contract_digest"`
	TestMode       string                       `json:"test_mode,omitempty"`
	GateResults    []model.CompletionGateResult `json:"gate_results,omitempty"`
	CommandDigests map[string]string            `json:"command_digests,omitempty"`
	RecordedAt     time.Time                    `json:"recorded_at"`
}

func (s *Service) testPassReceiptPath(projectID string) (string, error) {
	if err := model.ValidateProjectIdentifier(projectID); err != nil {
		return "", err
	}
	return filepath.Join(s.Config.StateDir, "gate-receipts", projectID, "test-pass.json"), nil
}

func (s *Service) projectIDForRoot(root string) (string, error) {
	root = filepath.Clean(root)
	projectID := ""
	for id, project := range s.Config.Projects {
		if filepath.Clean(project.Root) != root {
			continue
		}
		if projectID != "" {
			return "", fmt.Errorf("multiple configured projects resolve to root %s", root)
		}
		projectID = id
	}
	return projectID, nil
}

func normalizeReceiptScope(mode string, packages []string) (gates.TestScope, error) {
	scope, err := (gates.TestScope{Mode: mode, Packages: packages}).Normalize()
	if err != nil {
		return gates.TestScope{}, err
	}
	if scope.Mode == gates.TestScopeFull && len(packages) > 0 {
		return gates.TestScope{}, fmt.Errorf("full test receipt cannot contain package targets")
	}
	return scope, nil
}

func validateTestPassReceipt(receipt testPassReceipt) error {
	if (receipt.SchemaVersion != testPassReceiptLegacySchemaVersion && receipt.SchemaVersion != testPassReceiptSchemaVersion) || model.ValidateProjectIdentifier(receipt.ProjectID) != nil || model.ValidateRevision(receipt.TreeID) != nil || model.ValidateRevision(receipt.Head) != nil || receipt.RecordedAt.IsZero() {
		return fmt.Errorf("invalid test pass receipt identity")
	}
	if len(receipt.GateNames) == 0 || len(receipt.GateNames) > 3 {
		return fmt.Errorf("invalid test pass receipt gate names")
	}
	seen := map[string]bool{}
	for _, name := range receipt.GateNames {
		if name != model.WorkflowGateFormat && name != model.WorkflowGateCheck && name != model.WorkflowGateTest || seen[name] {
			return fmt.Errorf("invalid test pass receipt gate names")
		}
		seen[name] = true
	}
	if !seen[model.WorkflowGateTest] || receipt.RunnerContract != gates.TestGateRunnerContractVersion || len(receipt.ContractDigest) != sha256.Size*2 {
		return fmt.Errorf("invalid test pass receipt contract")
	}
	if _, err := hex.DecodeString(receipt.ContractDigest); err != nil {
		return fmt.Errorf("invalid test pass receipt contract")
	}
	scope, err := normalizeReceiptScope(receipt.ScopeMode, receipt.ScopePackages)
	if err != nil {
		return fmt.Errorf("invalid test pass receipt scope: %w", err)
	}
	digest, err := gates.TestGateCommandContractDigest(receipt.GateNames, scope)
	if err != nil || digest != receipt.ContractDigest {
		return fmt.Errorf("test pass receipt contract mismatch")
	}
	normalizedGates, err := gates.Resolve(receipt.GateNames)
	if err != nil || !reflect.DeepEqual(normalizedGates, receipt.GateNames) {
		return fmt.Errorf("invalid test pass receipt gate names")
	}
	if receipt.SchemaVersion == testPassReceiptSchemaVersion {
		if receipt.TestMode != "task" && receipt.TestMode != "train" {
			return fmt.Errorf("invalid test pass receipt mode")
		}
		for gate, digest := range receipt.CommandDigests {
			if gate != model.WorkflowGateFormat && gate != model.WorkflowGateCheck && gate != model.WorkflowGateTest || len(digest) != sha256.Size*2 {
				return fmt.Errorf("invalid test pass receipt command identity")
			}
			if _, err := hex.DecodeString(digest); err != nil {
				return fmt.Errorf("invalid test pass receipt command identity")
			}
		}
		for _, result := range receipt.GateResults {
			if result.ExitCode != 0 || result.TreeID != receipt.TreeID {
				return fmt.Errorf("invalid test pass receipt gate result")
			}
		}
	}
	return nil
}

func testPassReceiptDigest(receipt testPassReceipt) (string, error) {
	data, err := json.Marshal(receipt)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func (s *Service) loadTestPassReceipt(projectID string) (testPassReceipt, string, error) {
	path, err := s.testPassReceiptPath(projectID)
	if err != nil {
		return testPassReceipt{}, "", err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return testPassReceipt{}, "", err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return testPassReceipt{}, "", fmt.Errorf("test pass receipt is not a regular file")
	}
	var receipt testPassReceipt
	if err := fsutil.ReadJSONBounded(path, 64<<10, &receipt); err != nil {
		return testPassReceipt{}, "", err
	}
	if err := validateTestPassReceipt(receipt); err != nil {
		return testPassReceipt{}, "", err
	}
	digest, err := testPassReceiptDigest(receipt)
	if err != nil {
		return testPassReceipt{}, "", err
	}
	return receipt, digest, nil
}

func (s *Service) currentTestIdentity(ctx context.Context, projectID, root string) (string, string, error) {
	project, err := s.projectConfig(projectID)
	if err != nil {
		return "", "", err
	}
	if filepath.Clean(root) == "." || filepath.IsAbs(filepath.Clean(root)) == false {
		return "", "", fmt.Errorf("test root does not match configured project")
	}
	// Finalization may run against the server-owned Train worktree rather than
	// the configured checkout. The caller has already bound that runtime path
	// to the current Attempt; use it for the exact receipt identity.
	project.Root = filepath.Clean(root)
	status, err := s.Git.WorktreeStatus(ctx, project)
	if err != nil {
		return "", "", err
	}
	tree, err := s.Git.WorktreeContentID(ctx, project)
	if err != nil {
		return "", "", err
	}
	return tree, status.Head, nil
}

func (s *Service) writeTestPassReceiptLocked(ctx context.Context, projectID, root string, gateNames []string, scope gates.TestScope) (testPassReceipt, string, error) {
	tree, head, err := s.currentTestIdentity(ctx, projectID, root)
	if err != nil {
		return testPassReceipt{}, "", err
	}
	normalizedScope, err := scope.Normalize()
	if err != nil {
		return testPassReceipt{}, "", err
	}
	normalizedGates, err := gates.Resolve(gateNames)
	if err != nil {
		return testPassReceipt{}, "", err
	}
	contract, err := gates.TestGateCommandContractDigest(normalizedGates, normalizedScope)
	if err != nil {
		return testPassReceipt{}, "", err
	}
	receipt := testPassReceipt{
		SchemaVersion:  testPassReceiptSchemaVersion,
		ProjectID:      projectID,
		TreeID:         tree,
		Head:           head,
		GateNames:      append([]string{}, normalizedGates...),
		ScopeMode:      normalizedScope.Mode,
		ScopePackages:  append([]string{}, normalizedScope.Packages...),
		RunnerContract: gates.TestGateRunnerContractVersion,
		ContractDigest: contract,
		TestMode:       "task",
		CommandDigests: map[string]string{},
		RecordedAt:     s.durableNow(),
	}
	configuration, err := s.ProjectConfigurationRead(ctx, projectID)
	if err != nil {
		return testPassReceipt{}, "", err
	}
	digest, err := gates.ProjectGateCommandDigest(configuration.Workflow.GateCommands, model.WorkflowGateTest, "task", normalizedScope)
	if err != nil {
		return testPassReceipt{}, "", err
	}
	receipt.CommandDigests[model.WorkflowGateTest] = digest
	path, err := s.testPassReceiptPath(projectID)
	if err != nil {
		return testPassReceipt{}, "", err
	}
	if err := fsutil.WriteJSONAtomic(path, receipt, 0o600); err != nil {
		return testPassReceipt{}, "", err
	}
	receiptDigest, err := testPassReceiptDigest(receipt)
	return receipt, receiptDigest, err
}

func (s *Service) writeProjectGatePassReceiptLocked(ctx context.Context, projectID, root string, gateNames []string, commands model.ProjectGateCommands, testMode string, scope gates.TestScope, results []model.CompletionGateResult) (testPassReceipt, string, error) {
	tree, head, err := s.currentTestIdentity(ctx, projectID, root)
	if err != nil {
		return testPassReceipt{}, "", err
	}
	normalized, err := scope.Normalize()
	if err != nil {
		return testPassReceipt{}, "", err
	}
	normalizedGates, err := gates.Resolve(gateNames)
	if err != nil {
		return testPassReceipt{}, "", err
	}
	contract, err := gates.TestGateCommandContractDigest(normalizedGates, normalized)
	if err != nil {
		return testPassReceipt{}, "", err
	}
	digests := make(map[string]string, len(normalizedGates))
	stored := make([]model.CompletionGateResult, 0, len(results))
	for _, name := range normalizedGates {
		digest, digestErr := gates.ProjectGateCommandDigest(commands, name, testMode, normalized)
		if digestErr != nil {
			return testPassReceipt{}, "", digestErr
		}
		digests[name] = digest
		for _, result := range results {
			if result.ID == name {
				copy := result
				copy.TreeID = tree
				stored = append(stored, copy)
				break
			}
		}
	}
	if len(stored) != len(normalizedGates) {
		return testPassReceipt{}, "", fmt.Errorf("gate receipt is missing required evidence")
	}
	receipt := testPassReceipt{
		SchemaVersion:  testPassReceiptSchemaVersion,
		ProjectID:      projectID,
		TreeID:         tree,
		Head:           head,
		GateNames:      append([]string{}, normalizedGates...),
		ScopeMode:      normalized.Mode,
		ScopePackages:  append([]string{}, normalized.Packages...),
		RunnerContract: gates.TestGateRunnerContractVersion,
		ContractDigest: contract,
		TestMode:       testMode,
		GateResults:    stored,
		CommandDigests: digests,
		RecordedAt:     s.durableNow(),
	}
	path, err := s.testPassReceiptPath(projectID)
	if err != nil {
		return testPassReceipt{}, "", err
	}
	if err := fsutil.WriteJSONAtomic(path, receipt, 0o600); err != nil {
		return testPassReceipt{}, "", err
	}
	digest, err := testPassReceiptDigest(receipt)
	return receipt, digest, err
}

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
