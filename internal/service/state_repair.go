package service

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s *Service) StateRepair(ctx context.Context, apply bool) (StateRepairResult, error) {
	check, err := s.StateCheck(ctx)
	if err != nil {
		return StateRepairResult{}, err
	}
	result := StateRepairResult{
		DryRun:    !apply,
		OldHubSHA: check.HubRevision,
		Check:     check,
		Actions:   []StateRepairAction{},
	}
	graphSnapshot, snapshotErr := s.Hub.ReadSnapshot(ctx)
	if snapshotErr != nil {
		return result, snapshotErr
	}
	graphs := map[string]taskRunGraphSnapshot{}
	graphFor := func(projectID string) (taskRunGraphSnapshot, error) {
		if graph, ok := graphs[projectID]; ok {
			return graph, nil
		}
		graph, err := s.readTaskRunGraph(ctx, graphSnapshot, projectID)
		if err != nil {
			return taskRunGraphSnapshot{}, err
		}
		graphs[projectID] = graph
		return graph, nil
	}
	plans := map[string]StatePlan{}
	for _, plan := range check.Plans {
		plans[plan.ProjectID] = plan
		if !plan.Valid || plan.ActiveRunID == "" {
			continue
		}
		graph, listErr := graphFor(plan.ProjectID)
		if listErr != nil {
			continue
		}
		for _, run := range graph.runs {
			if run.ID != plan.ActiveRunID || operationalActiveRun(run) {
				continue
			}
			result.Actions = append(result.Actions, StateRepairAction{
				Kind:        "plan_pointer",
				ProjectID:   plan.ProjectID,
				Path:        s.planPath(plan.ProjectID),
				ClearTaskID: true,
				ClearRunID:  true,
				OldTaskID:   plan.ActiveTaskID,
				OldRunID:    plan.ActiveRunID,
				Reason:      "clear obsolete active pointer to history-only or terminal run",
			})
			break
		}
	}
	for _, projectID := range check.ConfiguredProjectIDs {
		plan, ok := plans[projectID]
		if !ok || !plan.Valid {
			continue
		}
		graph, graphErr := graphFor(projectID)
		if graphErr != nil {
			continue
		}
		runsByTask := map[string][]model.Run{}
		for _, run := range graph.runs {
			runsByTask[run.TaskID] = append(runsByTask[run.TaskID], run)
		}
		for _, record := range graph.tasks {
			if !historicalOnlyTaskRepairEligible(record, runsByTask[record.Task.ID], plan) {
				continue
			}
			result.Actions = append(result.Actions, StateRepairAction{
				Kind:      "task_state",
				ProjectID: projectID,
				TaskID:    record.Task.ID,
				Path:      s.taskStatePath(projectID, record.Task.ID),
				OldStatus: "dispatched",
				NewStatus: "cancelled",
				Reason:    historyOnlyTaskRepairReason,
			})
		}
	}
	sort.Slice(result.Actions, func(i, j int) bool {
		if result.Actions[i].ProjectID != result.Actions[j].ProjectID {
			return result.Actions[i].ProjectID < result.Actions[j].ProjectID
		}
		if result.Actions[i].Kind != result.Actions[j].Kind {
			return result.Actions[i].Kind < result.Actions[j].Kind
		}
		return result.Actions[i].Path < result.Actions[j].Path
	})
	if err := graphSnapshot.Close(); err != nil {
		return result, err
	}
	if !apply || len(result.Actions) == 0 {
		result.Applied = false
		return result, nil
	}
	backup, backupErr := s.Hub.Backup(ctx, "state-repair")
	if backupErr != nil {
		return result, backupErr
	}
	result.Backup = backup.Path
	reasons := []string{}
	for _, action := range result.Actions {
		if !containsString(reasons, action.Reason) {
			reasons = append(reasons, action.Reason)
		}
	}
	tx, txErr := s.Hub.Transact(ctx, check.HubRevision, "gateway: repair durable state: "+strings.Join(reasons, "; "), func(worktree string) ([]string, error) {
		paths := []string{}
		for _, action := range result.Actions {
			if action.Kind != "plan_pointer" {
				continue
			}
			var plan model.Plan
			if err := readWorktreeJSON(worktree, action.Path, &plan); err != nil {
				return nil, err
			}
			if err := model.ValidatePlan(plan); err != nil {
				return nil, err
			}
			if plan.ActiveTaskID != action.OldTaskID || plan.ActiveRunID != action.OldRunID {
				return nil, fmt.Errorf("plan pointer changed before repair: %s", action.Path)
			}
			if action.ClearTaskID {
				plan.ActiveTaskID = ""
			}
			if action.ClearRunID {
				plan.ActiveRunID = ""
			}
			plan.Revision++
			plan.UpdatedBy = s.Config.GatewayID
			plan.UpdatedAt = nowUTC()
			if err := model.ValidatePlan(plan); err != nil {
				return nil, err
			}
			if err := hub.WriteJSON(worktree, action.Path, plan); err != nil {
				return nil, err
			}
			paths = append(paths, action.Path)
		}
		for _, action := range result.Actions {
			if action.Kind != "task_state" {
				continue
			}
			var task model.Task
			taskPath := s.taskPath(action.ProjectID, action.TaskID)
			if err := readWorktreeJSON(worktree, taskPath, &task); err != nil {
				return nil, err
			}
			if err := model.ValidateTask(task); err != nil {
				return nil, err
			}
			var state model.TaskState
			if err := readWorktreeJSON(worktree, action.Path, &state); err != nil {
				return nil, err
			}
			if err := model.ValidateTaskState(state, task); err != nil {
				return nil, err
			}
			if state.Status != action.OldStatus {
				return nil, fmt.Errorf("task state changed before repair: %s", action.Path)
			}
			if err := s.validateHistoricalOnlyTaskRepair(worktree, action, check.ConfiguredProjectIDs); err != nil {
				return nil, err
			}
			state.Status = action.NewStatus
			state.UpdatedAt = nowUTC()
			if err := model.ValidateTaskState(state, task); err != nil {
				return nil, err
			}
			if err := hub.WriteJSON(worktree, action.Path, state); err != nil {
				return nil, err
			}
			paths = append(paths, action.Path)
		}
		return paths, nil
	})
	if txErr != nil {
		return result, txErr
	}
	result.Applied = true
	result.NewHubSHA = tx.After
	result.ChangedPaths = append([]string{}, tx.Paths...)
	after, afterErr := s.StateCheck(ctx)
	if afterErr != nil {
		return result, afterErr
	}
	result.Check = after
	if !after.Valid {
		return result, fmt.Errorf("state repair validation failed: %d issue(s)", len(after.Issues))
	}
	return result, nil
}

func historicalOnlyTaskRepairEligible(record TaskRecord, runs []model.Run, plan StatePlan) bool {
	if record.State.Status != "dispatched" || !plan.Valid {
		return false
	}
	if plan.ActiveTaskID == record.Task.ID {
		return false
	}
	historical := 0
	for _, run := range runs {
		if run.TaskID != record.Task.ID {
			continue
		}
		if run.Historical {
			historical++
			if run.ID == plan.ActiveRunID {
				return false
			}
			continue
		}
		return false
	}
	return historical > 0 && plan.ActiveRunID == ""
}
