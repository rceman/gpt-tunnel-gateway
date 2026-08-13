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
	if err := RequireWorkflowPolicyAuthority(ctx); err != nil {
		return model.ProjectWorkflowPolicy{}, OperationResult{}, err
	}
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
	if configurationErr == nil {
		current, err := workflowPolicyFromConfiguration(configuration)
		if err != nil {
			return model.ProjectWorkflowPolicy{}, OperationResult{}, err
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
		if policy.Revision != 1 && policy.Revision < 1 {
			return model.ProjectWorkflowPolicy{}, OperationResult{}, fmt.Errorf("initial workflow policy revision must be 1")
		}
		configuration = model.DefaultProjectConfiguration(policy.ProjectID, policy.UpdatedAt)
	}
	configuration.SchemaVersion = model.ProjectConfigurationSchemaVersion
	configuration.ProjectID = policy.ProjectID
	configuration.Revision = policy.Revision
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
	configurationPath := s.projectConfigurationPath(policy.ProjectID)
	legacyPath := s.workflowPolicyPath(policy.ProjectID)
	tx, err := s.Hub.Transact(ctx, in.ExpectedHubRevision, "gateway: adopt workflow policy "+policy.ProjectID, func(worktree string) ([]string, error) {
		if err := s.rejectActiveWorkflowExecutionInWorktree(worktree, policy.ProjectID); err != nil {
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
	return policy, OperationResult{
		Hub:       tx,
		ProjectID: policy.ProjectID,
		Status:    status,
	}, nil
}

func (s *Service) rejectActiveWorkflowExecution(ctx context.Context, projectID string) error {
	active, err := s.projectHasActiveTrainAttempt(ctx, projectID)
	if err != nil {
		return fmt.Errorf("inspect active Train Attempt: %w", err)
	}
	if active {
		return fmt.Errorf("workflow policy cannot change while an active Train Attempt exists")
	}
	return nil
}

func (s *Service) rejectActiveWorkflowExecutionInWorktree(worktree, projectID string) error {
	active, err := activeTrainAttemptInWorktree(worktree, projectID)
	if err != nil {
		return fmt.Errorf("inspect active Train Attempt: %w", err)
	}
	if active {
		return fmt.Errorf("workflow policy cannot change while an active Train Attempt exists")
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

func workflowPolicyStatus(policy model.ProjectWorkflowPolicy, err error, tasks []TaskRecord) ProjectWorkflowPolicyStatus {
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
	for _, item := range tasks {
		if item.Task.OperationClass == "" || (item.State.Status != "dispatched" && item.State.Status != "running" && item.State.Status != "ready") {
			continue
		}
		// The task projection is immutable evidence of the policy used when the
		// task was created. Do not recompute it from the current policy: a later
		// policy revision must remain visible separately above without changing
		// the meaning of an active task.
		status.ActiveOperationClass = item.Task.OperationClass
		status.ActiveCIMode = item.Task.EffectiveCIMode
		status.CIBlocking = item.Task.CIBlocking
	}
	return status
}
