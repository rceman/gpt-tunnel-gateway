package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s *Service) taskTrainPath(project string) string {
	return s.projectPrefix(project) + "/train/current.json"
}

func (s *Service) taskTrainPathFor(project, trainID string) string {
	if model.ValidateProjectIdentifier(project) != nil || model.ValidateObjectIdentifier(trainID) != nil {
		return "../invalid-task-train"
	}
	return s.projectPrefix(project) + "/train/" + trainID + ".json"
}

func normalizeTaskTrain(train *model.TaskTrain) {
	if train.TrainID == "" {
		train.TrainID = train.ID
	}
	train.ID = ""
	if len(train.ExecutionGroups) == 0 {
		train.ExecutionGroups = model.DefaultExecutionGroups(train.TaskIDs, "")
	}
}

func (s *Service) TaskTrainRead(ctx context.Context, project string) (model.TaskTrain, error) {
	if err := model.ValidateProjectIdentifier(project); err != nil {
		return model.TaskTrain{}, err
	}
	var legacy model.TaskTrain
	if err := s.Hub.ReadJSON(ctx, s.taskTrainPath(project), &legacy); err == nil {
		normalizeTaskTrain(&legacy)
		if err := model.ValidateTaskTrain(legacy); err != nil || legacy.ProjectID != project {
			return model.TaskTrain{}, fmt.Errorf("invalid task train")
		}
		return legacy, nil
	} else if !IsNotFound(err) {
		return model.TaskTrain{}, err
	}
	paths, err := s.Hub.List(ctx, s.projectPrefix(project)+"/train", ".json")
	if err != nil {
		return model.TaskTrain{}, err
	}
	if len(paths) == 0 {
		return model.TaskTrain{}, os.ErrNotExist
	}
	if len(paths) > 1 {
		return model.TaskTrain{}, fmt.Errorf("multiple task trains exist; train_id is required")
	}
	return s.TaskTrainReadByID(ctx, project, strings.TrimSuffix(filepathBase(paths[0]), ".json"))
}

func (s *Service) TaskTrainReadByID(ctx context.Context, project, trainID string) (model.TaskTrain, error) {
	if err := model.ValidateProjectIdentifier(project); err != nil {
		return model.TaskTrain{}, err
	}
	if err := model.ValidateObjectIdentifier(trainID); err != nil {
		return model.TaskTrain{}, err
	}
	var train model.TaskTrain
	if err := s.Hub.ReadJSON(ctx, s.taskTrainPathFor(project, trainID), &train); err != nil {
		return model.TaskTrain{}, err
	}
	normalizeTaskTrain(&train)
	if err := model.ValidateTaskTrain(train); err != nil || train.ProjectID != project {
		return model.TaskTrain{}, fmt.Errorf("invalid task train")
	}
	if train.TrainID != trainID {
		return model.TaskTrain{}, fmt.Errorf("task train id mismatch")
	}
	return train, nil
}

func filepathBase(path string) string {
	if index := strings.LastIndex(path, "/"); index >= 0 {
		return path[index+1:]
	}
	return path
}

func (s *Service) TaskTrainList(ctx context.Context, project string) ([]model.TaskTrain, error) {
	if err := model.ValidateProjectIdentifier(project); err != nil {
		return nil, err
	}
	paths, err := s.Hub.List(ctx, s.projectPrefix(project)+"/train", ".json")
	if err != nil {
		return nil, err
	}
	trains := make([]model.TaskTrain, 0, len(paths))
	for _, path := range paths {
		id := strings.TrimSuffix(filepathBase(path), ".json")
		train, err := s.TaskTrainReadByID(ctx, project, id)
		if err != nil {
			return nil, err
		}
		trains = append(trains, train)
	}
	sort.Slice(trains, func(i, j int) bool { return trains[i].TrainID < trains[j].TrainID })
	return trains, nil
}

func (s *Service) TaskTrainCreate(ctx context.Context, in TaskTrainCreateInput) (model.TaskTrain, OperationResult, error) {
	if err := model.ValidateProjectIdentifier(in.ProjectID); err != nil {
		return model.TaskTrain{}, OperationResult{}, err
	}
	if len(in.TaskIDs) < 1 || len(in.TaskIDs) > model.MaxTaskTrainTasks {
		return model.TaskTrain{}, OperationResult{}, fmt.Errorf("task train must contain 1-%d tasks", model.MaxTaskTrainTasks)
	}
	if in.CreatedBy == "" {
		return model.TaskTrain{}, OperationResult{}, fmt.Errorf("created_by is required")
	}
	trainID := in.TrainID
	if trainID == "" {
		trainID = "train-" + in.TaskIDs[0]
	}
	if trainID == "current" {
		return model.TaskTrain{}, OperationResult{}, fmt.Errorf("new task trains must use a stable train_id")
	}
	if err := model.ValidateObjectIdentifier(trainID); err != nil {
		return model.TaskTrain{}, OperationResult{}, err
	}
	if _, err := s.ProjectRead(ctx, in.ProjectID); err != nil {
		return model.TaskTrain{}, OperationResult{}, err
	}
	for _, id := range in.TaskIDs {
		if err := model.ValidateObjectIdentifier(id); err != nil {
			return model.TaskTrain{}, OperationResult{}, err
		}
	}
	configuration, err := s.ProjectConfigurationRead(ctx, in.ProjectID)
	if err != nil {
		return model.TaskTrain{}, OperationResult{}, fmt.Errorf("read project configuration: %w", err)
	}
	groups := append([]model.ExecutionGroup{}, in.ExecutionGroups...)
	if len(groups) == 0 {
		recommended := configuration.AgentRouting.SingletonRecommendedReasoning
		if len(in.TaskIDs) > 1 {
			recommended = configuration.AgentRouting.GroupRecommendedReasoning
		}
		groups = model.DefaultExecutionGroups(in.TaskIDs, recommended)
	} else {
		for i := range groups {
			if groups[i].RecommendedReasoning == "" {
				groups[i].RecommendedReasoning = configuration.AgentRouting.GroupRecommendedReasoning
				if len(groups[i].TaskIDs) == 1 {
					groups[i].RecommendedReasoning = configuration.AgentRouting.SingletonRecommendedReasoning
				}
			}
		}
	}
	baseRevision, laneBranch, err := s.resolveTaskTrainLane(ctx, in.ProjectID, trainID, in.BaseRevision, in.LaneBranch)
	if err != nil {
		return model.TaskTrain{}, OperationResult{}, err
	}
	train := model.TaskTrain{
		SchemaVersion:   model.TaskTrainSchemaVersion,
		TrainID:         trainID,
		ProjectID:       in.ProjectID,
		TaskIDs:         append([]string{}, in.TaskIDs...),
		ExecutionGroups: groups,
		BaseRevision:    baseRevision,
		LaneBranch:      laneBranch,
		CurrentTaskID:   in.TaskIDs[0],
		Status:          model.TaskTrainActive,
		UpdatedAt:       time.Now().UTC(),
	}
	if err := model.ValidateTaskTrain(train); err != nil {
		return model.TaskTrain{}, OperationResult{}, err
	}
	expected := in.ExpectedHubRevision
	if expected == "" {
		var err error
		expected, err = s.hubRevision(ctx)
		if err != nil {
			return model.TaskTrain{}, OperationResult{}, err
		}
	}
	tx, err := s.Hub.Transact(ctx, expected, "watcher: create task train", func(worktree string) ([]string, error) {
		trainPath := s.taskTrainPathFor(in.ProjectID, train.TrainID)
		var existing model.TaskTrain
		if err := readWorktreeJSON(worktree, trainPath, &existing); err == nil {
			return nil, fmt.Errorf("task train already exists")
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		if err := rejectTrainLaneCollision(worktree, s.projectPrefix(in.ProjectID)+"/train", train); err != nil {
			return nil, err
		}
		var currentPlan model.Plan
		if err := readWorktreeJSON(worktree, s.planPath(in.ProjectID), &currentPlan); err != nil {
			return nil, err
		}
		for i, id := range train.TaskIDs {
			var task model.Task
			if err := readWorktreeJSON(worktree, s.taskPath(in.ProjectID, id), &task); err != nil {
				return nil, err
			}
			if task.ProjectID != in.ProjectID || model.ValidateTask(task) != nil {
				return nil, fmt.Errorf("task train task %q is invalid or belongs to another project", id)
			}
			var state model.TaskState
			if err := readWorktreeJSON(worktree, s.taskStatePath(in.ProjectID, id), &state); err != nil {
				return nil, err
			}
			if err := model.ValidateTaskState(state, task); err != nil {
				return nil, err
			}
			if i == 0 && state.Status != "created" && state.Status != "ready" {
				return nil, fmt.Errorf("first task is not dispatchable: %s", state.Status)
			}
		}
		paths := []string{trainPath}
		if currentPlan.ActiveTaskID == "" && currentPlan.ActiveRunID == "" {
			currentPlan.ActiveTaskID = train.CurrentTaskID
			currentPlan.UpdatedBy = in.CreatedBy
			currentPlan.UpdatedAt = train.UpdatedAt
			currentPlan.Revision++
			if err := model.ValidatePlan(currentPlan); err != nil {
				return nil, err
			}
			paths = append(paths, s.planPath(in.ProjectID))
		}
		if err := hub.WriteJSON(worktree, trainPath, train); err != nil {
			return nil, err
		}
		if len(paths) == 2 {
			if err := hub.WriteJSON(worktree, s.planPath(in.ProjectID), currentPlan); err != nil {
				return nil, err
			}
		}
		return paths, nil
	})
	if err != nil {
		return model.TaskTrain{}, OperationResult{}, err
	}
	return train, OperationResult{
		Hub:       tx,
		ProjectID: in.ProjectID,
		Status:    model.TaskTrainActive,
	}, nil
}

func (s *Service) resolveTaskTrainLane(ctx context.Context, projectID, trainID, baseRevision, laneBranch string) (string, string, error) {
	if baseRevision != "" {
		if err := model.ValidateRevision(baseRevision); err != nil {
			return "", "", err
		}
	} else {
		project, err := s.ProjectRead(ctx, projectID)
		if err != nil {
			return "", "", err
		}
		branch := project.DefaultBranch
		if policy, policyErr := s.ProjectWorkflowPolicyRead(ctx, projectID); policyErr == nil && policy.IntegrationBranch != "" {
			branch = policy.IntegrationBranch
		} else if policyErr != nil && !IsNotFound(policyErr) {
			return "", "", policyErr
		}
		local, err := s.projectConfig(projectID)
		if err != nil {
			return "", "", err
		}
		head, exists, err := s.Git.MirrorBranchHead(ctx, local, branch)
		if err != nil || !exists || head == "" {
			return "", "", fmt.Errorf("resolve task train lane base: branch %q is unavailable", branch)
		}
		baseRevision = head
	}
	if laneBranch == "" {
		laneBranch = "train/" + trainID
	}
	if err := model.ValidateBranch(laneBranch); err != nil {
		return "", "", err
	}
	return baseRevision, laneBranch, nil
}

func rejectTrainLaneCollision(worktree, trainRoot string, candidate model.TaskTrain) error {
	root := filepath.Join(worktree, filepath.FromSlash(trainRoot))
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		var existing model.TaskTrain
		if err := readWorktreeJSON(worktree, filepath.ToSlash(filepath.Join(trainRoot, entry.Name())), &existing); err != nil {
			continue
		}
		normalizeTaskTrain(&existing)
		if model.ValidateTaskTrain(existing) != nil || existing.TrainID == candidate.TrainID || existing.Status == model.TaskTrainCompleted || existing.Status == model.TaskTrainBlocked {
			continue
		}
		if existing.LaneBranch != "" && existing.LaneBranch == candidate.LaneBranch {
			return fmt.Errorf("task train lane %q is already active", candidate.LaneBranch)
		}
	}
	return nil
}

func (s *Service) TaskTrainStatus(ctx context.Context, project string) (TaskTrainStatus, error) {
	train, err := s.TaskTrainRead(ctx, project)
	if err != nil {
		return TaskTrainStatus{}, err
	}
	return s.taskTrainStatus(ctx, train)
}

func (s *Service) TaskTrainStatusByID(ctx context.Context, project, trainID string) (TaskTrainStatus, error) {
	train, err := s.TaskTrainReadByID(ctx, project, trainID)
	if err != nil {
		return TaskTrainStatus{}, err
	}
	return s.taskTrainStatus(ctx, train)
}

func (s *Service) taskTrainStatus(ctx context.Context, train model.TaskTrain) (TaskTrainStatus, error) {
	result := TaskTrainStatus{
		ProjectID:     train.ProjectID,
		TrainID:       model.CanonicalTaskTrainID(train),
		Status:        train.Status,
		CurrentIndex:  train.CurrentIndex,
		TaskCount:     len(train.TaskIDs),
		CurrentTaskID: train.CurrentTaskID,
		CurrentRunID:  train.CurrentRunID,
		WaitReason:    train.WaitReason,
	}
	if train.CurrentIndex+1 < len(train.TaskIDs) {
		result.NextTaskID = train.TaskIDs[train.CurrentIndex+1]
	}
	task, err := s.findTask(ctx, train.CurrentTaskID)
	if err != nil {
		return TaskTrainStatus{}, err
	}
	state, err := s.taskState(ctx, task)
	if err != nil {
		return TaskTrainStatus{}, err
	}
	result.CurrentTaskState = state.Status
	runs, err := s.RunList(ctx, train.ProjectID)
	if err != nil {
		return TaskTrainStatus{}, err
	}
	for _, run := range runs {
		if run.TaskID != task.ID || run.Historical {
			continue
		}
		if train.CurrentRunID == run.ID || (result.CurrentRunID == "" && operationalActiveRun(run)) {
			result.CurrentRunID, result.CurrentRunStatus = run.ID, run.Status
			break
		}
	}
	return result, nil
}
