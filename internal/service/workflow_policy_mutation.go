package service

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s *Service) ProjectWorkflowPolicyAdopt(ctx context.Context, in ProjectWorkflowPolicyInput) (model.ProjectWorkflowPolicy, OperationResult, error) {
	policy := in.Policy
	if err := model.ValidateProjectWorkflowPolicy(policy); err != nil {
		return model.ProjectWorkflowPolicy{}, OperationResult{}, err
	}
	if _, err := s.ProjectRead(ctx, policy.ProjectID); err != nil {
		return model.ProjectWorkflowPolicy{}, OperationResult{}, err
	}
	projectLock, err := lockfile.Acquire(filepath.Join(s.Config.StateDir, "locks"), "project-"+policy.ProjectID)
	if err != nil {
		return model.ProjectWorkflowPolicy{}, OperationResult{}, err
	}
	defer projectLock.Release()
	if err := s.rejectActiveWorkflowExecution(ctx, policy.ProjectID); err != nil {
		return model.ProjectWorkflowPolicy{}, OperationResult{}, err
	}
	if s.Durability != nil {
		current, err := s.ProjectWorkflowPolicyRead(ctx, policy.ProjectID)
		if err != nil {
			return model.ProjectWorkflowPolicy{}, OperationResult{}, err
		}
		if !workflowPolicyLeavesEquivalent(current, policy) {
			return model.ProjectWorkflowPolicy{}, OperationResult{}, fmt.Errorf("workflow policy leaves are governed by canonical Rules; change them through rule/update")
		}
		if !workflowPoliciesEquivalent(current, policy) {
			return model.ProjectWorkflowPolicy{}, OperationResult{}, fmt.Errorf("Shared workflow policy is a read-only projection of canonical Rules")
		}
		_ = s.cacheProjectWorkflowPolicy(current)
		return current, OperationResult{
			ProjectID: policy.ProjectID,
			Status:    "adopted",
		}, nil
	}
	path := s.workflowPolicyPath(policy.ProjectID)
	current, readErr := s.readHubWorkflowPolicy(ctx, policy.ProjectID)
	if readErr != nil && !IsNotFound(readErr) {
		return model.ProjectWorkflowPolicy{}, OperationResult{}, readErr
	}
	expectedPolicyRevision := 0
	status := "adopted"
	if readErr == nil {
		expectedPolicyRevision = current.Revision
		if policy.Revision == current.Revision && workflowPoliciesEquivalent(current, policy) {
			_ = s.cacheProjectWorkflowPolicy(current)
			return current, OperationResult{
				ProjectID: policy.ProjectID,
				Status:    "adopted",
			}, nil
		}
		if policy.Revision != current.Revision+1 {
			return model.ProjectWorkflowPolicy{}, OperationResult{}, fmt.Errorf("workflow policy revision must advance from %d to %d", current.Revision, current.Revision+1)
		}
		status = "updated"
	} else if policy.Revision != 1 {
		return model.ProjectWorkflowPolicy{}, OperationResult{}, fmt.Errorf("initial workflow policy revision must be 1")
	}
	tx, err := s.Hub.Transact(ctx, in.ExpectedHubRevision, "gateway: adopt workflow policy "+policy.ProjectID, func(worktree string) ([]string, error) {
		var latest model.ProjectWorkflowPolicy
		if err := readWorktreeJSON(worktree, path, &latest); err != nil {
			if expectedPolicyRevision != 0 || !IsNotFound(err) {
				return nil, &LifecycleConflictError{
					Code:  "conflict",
					Phase: "workflow_policy.cas",
				}
			}
		} else if err := model.ValidateProjectWorkflowPolicy(latest); err != nil || latest.ProjectID != policy.ProjectID || latest.Revision != expectedPolicyRevision {
			return nil, &LifecycleConflictError{
				Code:  "conflict",
				Phase: "workflow_policy.cas",
			}
		}
		if err := hub.WriteJSON(worktree, path, policy); err != nil {
			return nil, err
		}
		return []string{path}, nil
	})
	if err != nil {
		return model.ProjectWorkflowPolicy{}, OperationResult{}, err
	}
	_ = s.cacheProjectWorkflowPolicy(policy)
	return policy, OperationResult{
		Hub:       tx,
		ProjectID: policy.ProjectID,
		Status:    status,
	}, nil
}

func (s *Service) rejectActiveWorkflowExecution(ctx context.Context, projectID string) error {
	active, err := s.projectHasActiveTaskExecution(ctx, projectID)
	if err != nil {
		return fmt.Errorf("inspect active Task execution: %w", err)
	}
	if active {
		return fmt.Errorf("workflow policy cannot change while an active Task execution exists")
	}
	return nil
}

func (s *Service) ProjectWorkflowPolicyUpdate(ctx context.Context, in ProjectWorkflowPolicyInput) (model.ProjectWorkflowPolicy, OperationResult, error) {
	return s.ProjectWorkflowPolicyAdopt(ctx, in)
}

func rejectHostedCIGates(gates []string, mode string) error {
	if mode == model.WorkflowCIModeRequire {
		return nil
	}
	for _, gate := range gates {
		lower := strings.ToLower(gate)
		if strings.Contains(lower, "check-github-ci") || strings.Contains(lower, "github actions") || strings.Contains(lower, "hosted ci") || strings.Contains(lower, "wait for ci") || strings.Contains(lower, "require ci") || strings.Contains(lower, "ci required") || strings.Contains(lower, "--policy required") || strings.Contains(lower, "--wait") {
			return fmt.Errorf("required_gates contains hosted-CI wait/require semantics while policy mode is %s", mode)
		}
	}
	return nil
}

func (s *Service) deriveTaskWorkflowPolicy(ctx context.Context, projectID, operationClass string, gates []string) (model.ProjectWorkflowPolicy, model.EffectiveWorkflowPolicy, error) {
	if err := model.ValidateOperationClass(operationClass); err != nil {
		return model.ProjectWorkflowPolicy{}, model.EffectiveWorkflowPolicy{}, err
	}
	configuration, err := s.ProjectConfigurationRead(ctx, projectID)
	if err != nil {
		return model.ProjectWorkflowPolicy{}, model.EffectiveWorkflowPolicy{}, fmt.Errorf("project configuration is required: %w", err)
	}
	policy, err := s.ProjectWorkflowPolicyRead(ctx, projectID)
	if err != nil {
		return model.ProjectWorkflowPolicy{}, model.EffectiveWorkflowPolicy{}, err
	}
	if s.Durability != nil && policy.Revision != configuration.Revision {
		return model.ProjectWorkflowPolicy{}, model.EffectiveWorkflowPolicy{}, fmt.Errorf("project workflow policy adapter revision mismatch")
	}
	effective, err := model.WorkflowPolicyForOperation(policy, operationClass)
	if err != nil {
		return model.ProjectWorkflowPolicy{}, model.EffectiveWorkflowPolicy{}, err
	}
	if err := rejectHostedCIGates(gates, effective.EffectiveCIMode); err != nil {
		return model.ProjectWorkflowPolicy{}, model.EffectiveWorkflowPolicy{}, err
	}
	return policy, effective, nil
}

func workflowPolicyStatus(policy model.ProjectWorkflowPolicy, err error) ProjectWorkflowPolicyStatus {
	effectiveGates := model.EffectiveProjectWorkflowGates(nil)
	if err != nil {
		failClosedCI := model.WorkflowPolicyCI{
			Task:      model.WorkflowCIModeDisabled,
			TaskMerge: model.WorkflowCIModeDisabled,
			Release:   model.WorkflowCIModeDisabled,
		}
		if IsNotFound(err) {
			return ProjectWorkflowPolicyStatus{
				State:            "missing",
				CI:               failClosedCI,
				Gates:            effectiveGates,
				Conflicts:        []string{"workflow_policy_missing"},
				CorrectiveAction: "adopt a durable project workflow policy before creating or superseding tasks",
			}
		}
		return ProjectWorkflowPolicyStatus{
			State:            "invalid",
			CI:               failClosedCI,
			Gates:            effectiveGates,
			Conflicts:        []string{"workflow_policy_invalid"},
			CorrectiveAction: "repair or re-adopt the durable project workflow policy",
		}
	}
	return ProjectWorkflowPolicyStatus{
		State:             "adopted",
		Revision:          policy.Revision,
		WorkflowStage:     policy.WorkflowStage,
		IntegrationBranch: policy.IntegrationBranch,
		AgentWaitForCI:    policy.Agent.WaitForCI,
		CI:                policy.CI,
		Gates:             effectiveGates,
		Conflicts:         []string{},
		CorrectiveAction:  "none",
	}
}
