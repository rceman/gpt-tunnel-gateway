package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
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
	if testMode == "train" {
		results, err := s.executeProjectTrainGatesWithReceiptReuse(ctx, projectID, root, names, configuration.Workflow.GateCommands, scope)
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

func withoutFormatGate(names []string) []string {
	remaining := make([]string, 0, len(names))
	for _, name := range names {
		if name != model.WorkflowGateFormat {
			remaining = append(remaining, name)
		}
	}
	return remaining
}

func mergeGateResults(expected []string, formatResults, remaining []model.CompletionGateResult, aggregateMS int64) ([]model.CompletionGateResult, error) {
	byID := make(map[string]model.CompletionGateResult, len(formatResults)+len(remaining))
	for _, result := range append(append([]model.CompletionGateResult{}, formatResults...), remaining...) {
		if !containsGate(expected, result.ID) {
			return nil, fmt.Errorf("gate evidence contains unexpected gate %q", result.ID)
		}
		if _, exists := byID[result.ID]; exists {
			return nil, fmt.Errorf("gate evidence contains duplicate gate %q", result.ID)
		}
		result.AggregateMS = 0
		warnings := make([]string, 0, len(result.Warnings))
		for _, warning := range result.Warnings {
			if strings.HasPrefix(warning, gates.GateOptimizationWarning+": aggregate_ms=") {
				continue
			}
			warnings = append(warnings, warning)
		}
		result.Warnings = warnings
		byID[result.ID] = result
	}
	merged := make([]model.CompletionGateResult, 0, len(expected))
	for _, name := range expected {
		result, ok := byID[name]
		if !ok {
			return nil, fmt.Errorf("gate evidence is missing gate %q", name)
		}
		merged = append(merged, result)
	}
	if len(merged) > 0 {
		merged[0].AggregateMS = aggregateMS
	}
	return merged, nil
}

type verificationGateSnapshot struct {
	head      string
	branch    string
	clean     bool
	porcelain string
	tree      string
	contentID string
}

func (s *Service) captureVerificationSnapshot(ctx context.Context, project config.ProjectConfig) (verificationGateSnapshot, error) {
	status, err := s.Git.WorktreeStatus(ctx, project)
	if err != nil {
		return verificationGateSnapshot{}, err
	}
	tree, err := s.Git.TreeID(ctx, project)
	if err != nil {
		return verificationGateSnapshot{}, err
	}
	contentID, err := s.Git.WorktreeContentID(ctx, project)
	if err != nil {
		return verificationGateSnapshot{}, err
	}
	return verificationGateSnapshot{
		head:      status.Head,
		branch:    status.Branch,
		clean:     status.Clean,
		porcelain: status.Porcelain,
		tree:      tree,
		contentID: contentID,
	}, nil
}

func validateVerificationSnapshot(before, after verificationGateSnapshot) error {
	if before.head != after.head || before.branch != after.branch || before.clean != after.clean || before.porcelain != after.porcelain || before.tree != after.tree || before.contentID != after.contentID {
		return fmt.Errorf("verification gate mutated repository state: before head=%s branch=%s clean=%t tree=%s content_id=%s; after head=%s branch=%s clean=%t tree=%s content_id=%s", before.head, before.branch, before.clean, before.tree, before.contentID, after.head, after.branch, after.clean, after.tree, after.contentID)
	}
	return nil
}

func (s *Service) executeTrainGatesWithScopedFormat(ctx context.Context, projectID string, project config.ProjectConfig, baseHead, candidateHead string) ([]model.CompletionGateResult, error) {
	before, err := s.captureVerificationSnapshot(ctx, project)
	if err != nil {
		return nil, err
	}
	if !before.clean || before.head != candidateHead {
		return nil, fmt.Errorf("Train gate candidate changed before verification")
	}
	changed, err := s.Git.ChangedFiles(ctx, project.Root, baseHead, candidateHead)
	if err != nil {
		return nil, err
	}
	names, err := s.ResolveProjectGates(ctx, projectID, "integration")
	if err != nil {
		return nil, err
	}
	started := time.Now()
	formatResults := make([]model.CompletionGateResult, 0, 1)
	var gateErr error
	if containsGate(names, model.WorkflowGateFormat) {
		if s.formatExecutor == nil {
			gateErr = fmt.Errorf("canonical formatter is not configured")
		} else {
			formatStarted := time.Now()
			if err := s.formatExecutor(ctx, project.Root, changedGoFiles(changed)); err != nil {
				gateErr = fmt.Errorf("Train scoped formatting failed: %w", err)
			} else {
				formatResults = append(formatResults, model.CompletionGateResult{ID: model.WorkflowGateFormat, ExitCode: 0, Execution: "executed", DurationMS: time.Since(formatStarted).Milliseconds()})
			}
		}
	}
	remainingNames := withoutFormatGate(names)
	remaining := make([]model.CompletionGateResult, 0, len(remainingNames))
	if gateErr == nil && len(remainingNames) > 0 {
		remaining, gateErr = s.executeProjectGatesWithProjectCommands(ctx, projectID, project.Root, remainingNames, "train")
	}
	after, snapshotErr := s.captureVerificationSnapshot(ctx, project)
	if snapshotErr != nil {
		return nil, snapshotErr
	}
	if err := validateVerificationSnapshot(before, after); err != nil {
		return nil, err
	}
	if gateErr != nil {
		return nil, gateErr
	}
	merged, err := mergeGateResults(names, formatResults, remaining, time.Since(started).Milliseconds())
	if err != nil {
		return nil, err
	}
	if err := validateProjectGateEvidence(merged, names); err != nil {
		return nil, err
	}
	return merged, nil
}

func (s *Service) executeTaskFinalizeGatesWithSnapshot(ctx context.Context, projectID string, project config.ProjectConfig, names, changed []string, scope gates.TestScope) ([]model.CompletionGateResult, verificationGateSnapshot, error) {
	before, err := s.captureVerificationSnapshot(ctx, project)
	if err != nil {
		return nil, verificationGateSnapshot{}, err
	}
	started := time.Now()
	formatResults := make([]model.CompletionGateResult, 0, 1)
	var gateErr error
	if containsGate(names, model.WorkflowGateFormat) {
		if s.formatExecutor == nil {
			gateErr = fmt.Errorf("canonical formatter is not configured")
		} else {
			formatStarted := time.Now()
			if err := s.formatExecutor(ctx, project.Root, changedGoFiles(changed)); err != nil {
				gateErr = fmt.Errorf("canonical formatting failed: %w", err)
			} else {
				formatResults = append(formatResults, model.CompletionGateResult{ID: model.WorkflowGateFormat, ExitCode: 0, Execution: "executed", DurationMS: time.Since(formatStarted).Milliseconds()})
			}
		}
	}
	remainingNames := withoutFormatGate(names)
	remaining := make([]model.CompletionGateResult, 0, len(remainingNames))
	if gateErr == nil && len(remainingNames) > 0 {
		remaining, gateErr = s.executeProjectGatesWithProjectCommandsAndScope(ctx, projectID, project.Root, remainingNames, "task", scope)
	}
	after, snapshotErr := s.captureVerificationSnapshot(ctx, project)
	if snapshotErr != nil {
		return nil, verificationGateSnapshot{}, snapshotErr
	}
	if err := validateVerificationSnapshot(before, after); err != nil {
		return nil, verificationGateSnapshot{}, err
	}
	if gateErr != nil {
		return nil, verificationGateSnapshot{}, gateErr
	}
	merged, err := mergeGateResults(names, formatResults, remaining, time.Since(started).Milliseconds())
	if err != nil {
		return nil, verificationGateSnapshot{}, err
	}
	if err := validateProjectGateEvidence(merged, names); err != nil {
		return nil, verificationGateSnapshot{}, err
	}
	return merged, after, nil
}
