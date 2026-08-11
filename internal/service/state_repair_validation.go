package service

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s *Service) validateStalePlanPointerInWorktree(worktree string, action StateRepairAction) error {
	if action.Kind != "plan_pointer" {
		return fmt.Errorf("invalid plan pointer repair action")
	}
	runHistorical := false
	if action.OldRunID != "" {
		raw, err := os.ReadFile(filepath.Join(worktree, filepath.FromSlash(s.runPath(action.ProjectID, action.OldRunID))))
		if err != nil {
			return err
		}
		run, historical, err := model.DecodeRunRecord(raw)
		if err != nil {
			return err
		}
		if operationalActiveRun(run) {
			return fmt.Errorf("plan pointer repair refused: run %s became operational", action.OldRunID)
		}
		runHistorical = historical && run.Historical
	}
	if action.OldTaskID != "" {
		var task model.Task
		if err := readWorktreeJSON(worktree, s.taskPath(action.ProjectID, action.OldTaskID), &task); err != nil {
			if os.IsNotExist(err) && runHistorical {
				return nil
			}
			return err
		}
		if err := model.ValidateTask(task); err != nil {
			return err
		}
		var state model.TaskState
		if err := readWorktreeJSON(worktree, s.taskStatePath(action.ProjectID, action.OldTaskID), &state); err != nil {
			return err
		}
		if err := model.ValidateTaskState(state, task); err != nil {
			return err
		}
	}
	if action.OldRunID == "" {
		var train model.TaskTrain
		if err := readWorktreeJSON(worktree, s.taskTrainPath(action.ProjectID), &train); err == nil {
			if model.ValidateTaskTrain(train) == nil && train.Status == model.TaskTrainActive && train.CurrentTaskID == action.OldTaskID && train.CurrentRunID == "" {
				return fmt.Errorf("plan pointer repair refused: task train now owns pending task %s", action.OldTaskID)
			}
		} else if !IsNotFound(err) {
			return err
		}
	}
	return nil
}

func (s *Service) validateHistoricalOnlyTaskRepair(worktree string, action StateRepairAction, projects []string) error {
	linkedHistorical := 0
	linkedRunIDs := map[string]bool{}
	root := filepath.Join(worktree, filepath.FromSlash(s.projectPrefix(action.ProjectID)+"/runs"))
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || entry.Name() != "run.json" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		run, historical, err := model.DecodeRunRecord(data)
		if err != nil {
			return err
		}
		if run.TaskID != action.TaskID {
			return nil
		}
		if !historical || !run.Historical {
			return fmt.Errorf("task %s has a non-historical linked run", action.TaskID)
		}
		linkedHistorical++
		linkedRunIDs[run.ID] = true
		return nil
	})
	if err != nil {
		return err
	}
	if linkedHistorical == 0 {
		return fmt.Errorf("task %s has no linked HistoricalRunV1 record", action.TaskID)
	}
	for _, projectID := range projects {
		var plan model.Plan
		if err := readWorktreeJSON(worktree, s.planPath(projectID), &plan); err != nil {
			return err
		}
		if err := model.ValidatePlan(plan); err != nil {
			return err
		}
		if plan.ActiveTaskID == action.TaskID || linkedRunIDs[plan.ActiveRunID] {
			return fmt.Errorf("task %s or linked history is referenced by active plan %s", action.TaskID, projectID)
		}
	}
	return nil
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func nowUTC() time.Time { return time.Now().UTC() }
