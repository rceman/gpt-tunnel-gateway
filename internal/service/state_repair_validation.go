package service

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

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
