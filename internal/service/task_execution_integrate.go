package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/rceman/gpt-tunnel-gateway/internal/gitx"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

type TaskExecutionIntegrateInput struct {
	ProjectID string
	Key       string
	Comment   string
}

type taskExecutionIntegrationCapture struct {
	SchemaVersion   int                          `json:"schema_version"`
	ProjectID       string                       `json:"project_id"`
	TaskID          string                       `json:"task_id"`
	BaseHead        string                       `json:"base_head"`
	LaneHead        string                       `json:"lane_head"`
	Branch          string                       `json:"branch"`
	IntegrationHead string                       `json:"integration_head,omitempty"`
	Gates           []model.CompletionGateResult `json:"gates"`
}

func (s *Service) saveTaskExecutionIntegrationCapture(ctx context.Context, capture taskExecutionIntegrationCapture) error {
	operationID := durableMutationOperationID(ctx)
	if operationID == "" {
		return nil
	}
	operation, err := s.readDurableMutation(operationID)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(capture)
	if err != nil {
		return err
	}
	operation.CapturedState = string(raw)
	operation.UpdatedAt = s.durableNow()
	return s.writeDurableMutation(operation)
}

func readTaskExecutionIntegrationCapture(operation durableMutationOperation) (taskExecutionIntegrationCapture, error) {
	if operation.CapturedState == "" {
		return taskExecutionIntegrationCapture{}, nil
	}
	var capture taskExecutionIntegrationCapture
	if err := json.Unmarshal([]byte(operation.CapturedState), &capture); err != nil || capture.SchemaVersion != 1 || capture.ProjectID == "" || capture.TaskID == "" || model.ValidateCommitSHA(capture.BaseHead) != nil || model.ValidateCommitSHA(capture.LaneHead) != nil || model.ValidateBranch(capture.Branch) != nil {
		return taskExecutionIntegrationCapture{}, fmt.Errorf("invalid durable Task integration capture")
	}
	if capture.IntegrationHead != "" && model.ValidateCommitSHA(capture.IntegrationHead) != nil {
		return taskExecutionIntegrationCapture{}, fmt.Errorf("invalid durable Task integration result")
	}
	return capture, nil
}

func (s *Service) failTaskExecutionIntegration(ctx context.Context, state model.TaskExecutionState, cause error) error {
	state.Status = model.TaskExecutionFailed
	state.ExecutionRevision++
	state.UpdatedAt = s.durableNow()
	if err := s.Durability.UpdateTaskExecutionState(ctx, state, state.ExecutionRevision-1); err != nil {
		return fmt.Errorf("Task integration failed and recovery state could not be recorded: %w (original: %v)", err, cause)
	}
	return cause
}

func (s *Service) TaskExecutionIntegrate(ctx context.Context, in TaskExecutionIntegrateInput) (TaskExecutionPublicOutput, error) {
	if err := validateTaskExecutionReviewInput(in.ProjectID, in.Key, "code"); err != nil {
		return TaskExecutionPublicOutput{}, err
	}
	s.taskExecutionMu.Lock()
	defer s.taskExecutionMu.Unlock()
	state, found, err := s.readExecutionForMutation(ctx, in.ProjectID, in.Key)
	if err != nil || !found {
		if err != nil {
			return TaskExecutionPublicOutput{}, err
		}
		return TaskExecutionPublicOutput{}, fmt.Errorf("Task has no execution state")
	}
	if state.Status != model.TaskExecutionReadyForIntegration && state.Status != model.TaskExecutionIntegrating {
		if state.Status == model.TaskExecutionIntegrated {
			return taskExecutionPublicOutput(state), nil
		}
		return TaskExecutionPublicOutput{}, fmt.Errorf("Task is not ready for integration")
	}
	accepted, found, err := s.Durability.ReadLatestAcceptedTaskExecutionPhase(ctx, in.ProjectID, in.Key, state.Stage)
	if err != nil || !found || accepted.Head != state.Head || accepted.TaskRevisionSHA256 != state.TaskRevisionSHA256 {
		return TaskExecutionPublicOutput{}, fmt.Errorf("Task integration review authority is stale")
	}
	task, err := s.TaskAuthoringRead(ctx, in.ProjectID, in.Key)
	if err != nil {
		return TaskExecutionPublicOutput{}, err
	}
	project, err := s.EffectiveProjectConfig(in.ProjectID)
	if err != nil {
		return TaskExecutionPublicOutput{}, err
	}
	lanePath, err := gitx.TaskWorktreePath(s.Config.StateDir, in.ProjectID, in.Key)
	if err != nil {
		return TaskExecutionPublicOutput{}, err
	}
	lane := project
	lane.Root = lanePath
	operationCapture := taskExecutionIntegrationCapture{
		SchemaVersion: 1,
		ProjectID:     in.ProjectID,
		TaskID:        in.Key,
		BaseHead:      state.BaseHead,
		LaneHead:      state.Head,
		Branch:        state.Branch,
	}
	if operationID := durableMutationOperationID(ctx); operationID != "" {
		operation, readErr := s.readDurableMutation(operationID)
		if readErr != nil {
			return TaskExecutionPublicOutput{}, readErr
		}
		operationCapture, err = readTaskExecutionIntegrationCapture(operation)
		if err != nil {
			return TaskExecutionPublicOutput{}, err
		}
		if operationCapture.SchemaVersion == 0 {
			operationCapture = taskExecutionIntegrationCapture{
				SchemaVersion: 1,
				ProjectID:     in.ProjectID,
				TaskID:        in.Key,
				BaseHead:      state.BaseHead,
				LaneHead:      state.Head,
				Branch:        state.Branch,
			}
		}
	}
	if operationCapture.ProjectID != in.ProjectID || operationCapture.TaskID != in.Key || operationCapture.BaseHead != state.BaseHead || operationCapture.LaneHead != state.Head || operationCapture.Branch != state.Branch {
		return TaskExecutionPublicOutput{}, fmt.Errorf("durable Task integration identity does not match execution state")
	}
	if operationCapture.IntegrationHead == "" {
		canonical, refreshErr := s.Git.RefreshDefaultBranch(ctx, project)
		if refreshErr != nil {
			return TaskExecutionPublicOutput{}, refreshErr
		}
		if canonical != state.BaseHead {
			return TaskExecutionPublicOutput{}, fmt.Errorf("canonical default branch advanced beyond Task base; rebase is required")
		}
		if _, syncErr := s.Git.SynchronizeDefaultBranchWorktree(ctx, project, canonical); syncErr != nil {
			return TaskExecutionPublicOutput{}, syncErr
		}
	}
	integrationState := state
	if state.Status == model.TaskExecutionReadyForIntegration {
		gates, gateErr := s.ExecuteProjectGates(ctx, in.ProjectID, lane.Root, "integration")
		if gateErr != nil {
			return TaskExecutionPublicOutput{}, gateErr
		}
		operationCapture.Gates = gates
		state.Status = model.TaskExecutionIntegrating
		state.ExecutionRevision++
		state.UpdatedAt = s.durableNow()
		if err := s.Durability.UpdateTaskExecutionState(ctx, state, state.ExecutionRevision-1); err != nil {
			return TaskExecutionPublicOutput{}, err
		}
		if err := s.saveTaskExecutionIntegrationCapture(ctx, operationCapture); err != nil {
			return TaskExecutionPublicOutput{}, s.failTaskExecutionIntegration(ctx, state, err)
		}
		integrationState = state
	}
	if operationCapture.IntegrationHead != "" {
		if current, statusErr := s.Git.WorktreeStatus(ctx, project); statusErr != nil || current.Head != operationCapture.IntegrationHead || !current.Clean {
			return TaskExecutionPublicOutput{}, s.failTaskExecutionIntegration(ctx, state, fmt.Errorf("durable Task integration result does not match canonical clean head"))
		}
	} else {
		message := "Task " + task.ID + ": " + task.Title
		comment := strings.TrimSpace(in.Comment)
		if utf8.RuneCountInString(comment) > 1024 {
			return TaskExecutionPublicOutput{}, fmt.Errorf("integration comment is too long")
		}
		if comment != "" {
			message += " - " + comment
		}
		_, err = s.Git.SquashTaskIntoDefaultBranch(ctx, project, lane, state.BaseHead, state.Head, message)
		if err != nil {
			return TaskExecutionPublicOutput{}, s.failTaskExecutionIntegration(ctx, state, err)
		}
		current, statusErr := s.Git.WorktreeStatus(ctx, project)
		if statusErr != nil || !current.Clean || model.ValidateCommitSHA(current.Head) != nil {
			return TaskExecutionPublicOutput{}, s.failTaskExecutionIntegration(ctx, state, fmt.Errorf("integrated canonical branch failed final verification"))
		}
		operationCapture.IntegrationHead = current.Head
		if err := s.saveTaskExecutionIntegrationCapture(ctx, operationCapture); err != nil {
			return TaskExecutionPublicOutput{}, s.failTaskExecutionIntegration(ctx, state, err)
		}
	}
	if _, err := s.TaskLifecycleArchive(ctx, in.ProjectID, in.Key, "gateway", "Task execution integrated"); err != nil {
		return TaskExecutionPublicOutput{}, s.failTaskExecutionIntegration(ctx, integrationState, fmt.Errorf("archive canonical Task after integration: %w", err))
	}
	if err := s.Git.RemoveTaskWorktreeAfterIntegration(ctx, project, s.Config.StateDir, in.ProjectID, in.Key, task.Type, task.Title, state.Head, state.Branch); err != nil {
		return TaskExecutionPublicOutput{}, s.failTaskExecutionIntegration(ctx, integrationState, fmt.Errorf("Task worktree cleanup failed: %w", err))
	}
	state.Status = model.TaskExecutionIntegrated
	state.ExecutionRevision++
	state.UpdatedAt = s.durableNow()
	if err := s.Durability.UpdateTaskExecutionState(ctx, state, state.ExecutionRevision-1); err != nil {
		return TaskExecutionPublicOutput{}, s.failTaskExecutionIntegration(ctx, integrationState, err)
	}
	return taskExecutionPublicOutput(state), nil
}
