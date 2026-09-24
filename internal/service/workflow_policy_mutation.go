package service

import (
	"context"
	"fmt"
	"os"
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
	configuration, configurationErr := s.ProjectConfigurationRead(ctx, policy.ProjectID)
	if configurationErr != nil && !IsNotFound(configurationErr) {
		return model.ProjectWorkflowPolicy{}, OperationResult{}, configurationErr
	}
	status := "adopted"
	var currentPolicy model.ProjectWorkflowPolicy
	if configurationErr == nil {
		current, err := s.workflowPolicyFromAuthority(ctx, configuration)
		if err != nil {
			return model.ProjectWorkflowPolicy{}, OperationResult{}, err
		}
		currentPolicy = current
		// Under Shared durability the six machine leaves are governed by the
		// named-rule effective set: this legacy path may only write the
		// non-rule configuration fields and provenance metadata. A policy
		// that diverges from rule authority is rejected — leaf changes must
		// be routed through rule/update.
		if s.Durability != nil && !workflowPolicyLeavesEquivalent(current, policy) {
			return model.ProjectWorkflowPolicy{}, OperationResult{}, fmt.Errorf("workflow policy leaf fields are governed by durable rules; change them through rule/update")
		}
		if configuration.Revision != policy.Revision && configuration.Revision+1 != policy.Revision {
			return model.ProjectWorkflowPolicy{}, OperationResult{}, fmt.Errorf("workflow policy revision must advance from %d to %d", configuration.Revision, policy.Revision)
		}
		if configuration.Revision == policy.Revision && workflowPoliciesEquivalent(current, policy) {
			status = "adopted"
		} else if configuration.Revision == policy.Revision && configuration.Revision == 1 {
			status = "adopted"
		} else {
			status = "updated"
		}
	} else {
		if s.Durability != nil {
			// Under Shared durability this legacy path cannot establish a
			// configuration without the seeded machine-policy leaf Rules —
			// a configured project must never have a vacuous effective set.
			return model.ProjectWorkflowPolicy{}, OperationResult{}, configurationErr
		}
		if policy.Revision != 1 && policy.Revision < 1 {
			return model.ProjectWorkflowPolicy{}, OperationResult{}, fmt.Errorf("initial workflow policy revision must be 1")
		}
		configuration = model.DefaultProjectConfiguration(policy.ProjectID, policy.UpdatedAt)
	}
	configuration.SchemaVersion = model.ProjectConfigurationSchemaVersion
	configuration.ProjectID = policy.ProjectID
	configuration.Revision = policy.Revision
	// The workflow leaf fields written here are provenance only under Shared
	// durability (the named-rule effective set is the authority); under the
	// legacy-compatibility path they remain the leaf authority.
	configuration.Workflow.WorkflowStage = policy.WorkflowStage
	configuration.Workflow.IntegrationBranch = policy.IntegrationBranch
	configuration.Workflow.CI = policy.CI
	configuration.Workflow.Gates = append([]string{}, policy.Gates...)
	configuration.Workflow.WaitForCI = policy.Agent.WaitForCI
	configuration.UpdatedBy = policy.UpdatedBy
	configuration.UpdatedAt = policy.UpdatedAt
	if err := model.ValidateProjectConfiguration(configuration); err != nil {
		return model.ProjectWorkflowPolicy{}, OperationResult{}, err
	}
	if s.Durability != nil {
		if currentPolicy.Revision == policy.Revision && workflowPoliciesEquivalent(currentPolicy, policy) {
			_ = s.cacheProjectWorkflowPolicy(currentPolicy)
			return currentPolicy, OperationResult{
				ProjectID: policy.ProjectID,
				Status:    "adopted",
			}, nil
		}
		if currentPolicy.Revision+1 != policy.Revision {
			return model.ProjectWorkflowPolicy{}, OperationResult{}, fmt.Errorf("workflow policy revision must advance from %d to %d", currentPolicy.Revision, currentPolicy.Revision+1)
		}
		workflow := configuration.Workflow
		updated, result, err := s.ProjectConfigurationUpdate(ctx, ProjectConfigurationUpdateInput{
			ProjectID:        policy.ProjectID,
			ExpectedRevision: currentPolicy.Revision,
			Patch: ProjectConfigurationPatch{
				Workflow: &workflow,
			},
			UpdatedBy: policy.UpdatedBy,
		})
		if err != nil {
			return model.ProjectWorkflowPolicy{}, OperationResult{}, err
		}
		updatedPolicy, err := s.workflowPolicyFromAuthority(ctx, updated)
		if err != nil {
			return model.ProjectWorkflowPolicy{}, OperationResult{}, err
		}
		_ = s.cacheProjectWorkflowPolicy(updatedPolicy)
		result.Status = status
		return updatedPolicy, result, nil
	}
	configurationPath := s.projectConfigurationPath(policy.ProjectID)
	legacyPath := s.workflowPolicyPath(policy.ProjectID)
	tx, err := s.Hub.Transact(ctx, in.ExpectedHubRevision, "gateway: adopt workflow policy "+policy.ProjectID, func(worktree string) ([]string, error) {
		if err := s.rejectActiveWorkflowExecution(ctx, policy.ProjectID); err != nil {
			return nil, err
		}
		projectPath := s.projectPath(policy.ProjectID)
		var project model.Project
		if err := readWorktreeJSON(worktree, projectPath, &project); err != nil {
			return nil, fmt.Errorf("project %q is not durable: %w", policy.ProjectID, err)
		}
		if err := model.ValidateProject(project); err != nil || project.ID != policy.ProjectID {
			if err == nil {
				err = fmt.Errorf("project ID mismatch")
			}
			return nil, fmt.Errorf("project %q is invalid: %w", policy.ProjectID, err)
		}
		if err := hub.WriteJSON(worktree, configurationPath, configuration); err != nil {
			return nil, err
		}
		paths := []string{configurationPath}
		if err := os.Remove(filepath.Join(worktree, filepath.FromSlash(legacyPath))); err == nil {
			paths = append(paths, legacyPath)
		} else if !os.IsNotExist(err) {
			return nil, err
		}
		return paths, nil
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
	policy, _, err := s.projectWorkflowPolicyReadDetailed(ctx, projectID)
	if err != nil {
		return model.ProjectWorkflowPolicy{}, model.EffectiveWorkflowPolicy{}, err
	}
	if policy.Revision != configuration.Revision {
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
	status := ProjectWorkflowPolicyStatus{
		State:             "adopted",
		Revision:          policy.Revision,
		WorkflowStage:     policy.WorkflowStage,
		IntegrationBranch: policy.IntegrationBranch,
		AgentWaitForCI:    policy.Agent.WaitForCI,
		CI:                policy.CI,
		Gates:             model.EffectiveProjectWorkflowGates(policy.Gates),
		Conflicts:         []string{},
		CorrectiveAction:  "none",
	}
	return status
}
