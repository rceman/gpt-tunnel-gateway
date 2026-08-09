package service

import (
	"context"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s *Service) readTaskStateFromSnapshot(ctx context.Context, snapshot *hub.ReadSnapshot, task model.Task) (model.TaskState, error) {
	var state model.TaskState
	if err := snapshot.ReadJSON(ctx, s.taskStatePath(task.ProjectID, task.ID), &state); err != nil {
		if IsNotFound(err) {
			return model.TaskState{SchemaVersion: model.SchemaVersion, TaskID: task.ID, TaskSHA256: task.SHA256, Status: "created", UpdatedAt: task.CreatedAt}, nil
		}
		return model.TaskState{}, err
	}
	if err := model.ValidateTaskState(state, task); err != nil {
		return model.TaskState{}, err
	}
	return state, nil
}

func (s *Service) checkProjectTaskRunGraph(ctx context.Context, snapshot *hub.ReadSnapshot, projectID string, plan model.Plan, result *StateCheckResult) {
	graph, err := s.readTaskRunGraph(ctx, snapshot, projectID)
	if err != nil {
		result.Issues = append(result.Issues, stateIssue("TASK_GRAPH_UNAVAILABLE", projectID, "", "", "", err.Error()))
		result.OperationalTaskRunGraph = false
		return
	}
	tasks := graph.tasks
	taskByID := map[string]TaskRecord{}
	for _, record := range tasks {
		taskByID[record.Task.ID] = record
	}
	runs := graph.runs
	runByID := map[string]model.Run{}
	runsByTask := map[string][]model.Run{}
	for _, run := range runs {
		runByID[run.ID] = run
		runsByTask[run.TaskID] = append(runsByTask[run.TaskID], run)
		if _, ok := taskByID[run.TaskID]; !ok {
			result.Issues = append(result.Issues, stateIssue("RUN_WITHOUT_TASK", projectID, run.TaskID, run.ID, s.runPath(projectID, run.ID), "run references no task"))
			result.OperationalTaskRunGraph = false
		}
		if run.Historical && plan.ActiveRunID == run.ID {
			result.Issues = append(result.Issues, stateIssue("HISTORY_RUN_ACTIVE", projectID, run.TaskID, run.ID, s.planPath(projectID), "immutable HistoricalRunV1 cannot be current operational state"))
			result.OperationalTaskRunGraph = false
		}
	}
	for _, record := range tasks {
		if record.State.Status == "dispatched" {
			count := 0
			for _, run := range runsByTask[record.Task.ID] {
				if operationalActiveRun(run) {
					count++
				}
			}
			if count != 1 {
				result.Issues = append(result.Issues, stateIssue("DISPATCHED_TASK_RUN_COUNT", projectID, record.Task.ID, "", s.taskPath(projectID, record.Task.ID), fmt.Sprintf("dispatched task has %d operational runs", count)))
				result.OperationalTaskRunGraph = false
			}
		}
	}
	if (plan.ActiveTaskID == "") != (plan.ActiveRunID == "") {
		result.Issues = append(result.Issues, stateIssue("PLAN_POINTER_PAIR_INVALID", projectID, plan.ActiveTaskID, plan.ActiveRunID, s.planPath(projectID), "active task and run pointers must be both empty or both present"))
		result.OperationalTaskRunGraph = false
	}
	if plan.ActiveTaskID != "" {
		record, ok := taskByID[plan.ActiveTaskID]
		if !ok {
			result.Issues = append(result.Issues, stateIssue("ACTIVE_TASK_MISSING", projectID, plan.ActiveTaskID, plan.ActiveRunID, s.planPath(projectID), "active task does not exist"))
			result.OperationalTaskRunGraph = false
		} else if record.State.Status == "completed" || record.State.Status == "cancelled" || record.State.Status == "superseded" {
			result.Issues = append(result.Issues, stateIssue("TERMINAL_TASK_ACTIVE", projectID, plan.ActiveTaskID, plan.ActiveRunID, s.planPath(projectID), "terminal task is active"))
			result.OperationalTaskRunGraph = false
		}
	}
	if plan.ActiveRunID != "" {
		run, ok := runByID[plan.ActiveRunID]
		if !ok {
			result.Issues = append(result.Issues, stateIssue("ACTIVE_RUN_MISSING", projectID, plan.ActiveTaskID, plan.ActiveRunID, s.planPath(projectID), "active run does not exist"))
			result.OperationalTaskRunGraph = false
		} else if run.Historical {
			result.Issues = append(result.Issues, stateIssue("HISTORY_RUN_ACTIVE", projectID, run.TaskID, run.ID, s.planPath(projectID), "history-only run is active"))
			result.OperationalTaskRunGraph = false
		} else if !operationalActiveRun(run) {
			result.Issues = append(result.Issues, stateIssue("TERMINAL_RUN_ACTIVE", projectID, run.TaskID, run.ID, s.planPath(projectID), "terminal run is active"))
			result.OperationalTaskRunGraph = false
		} else if run.TaskID != plan.ActiveTaskID {
			result.Issues = append(result.Issues, stateIssue("PLAN_POINTER_MISMATCH", projectID, plan.ActiveTaskID, plan.ActiveRunID, s.planPath(projectID), "active task and run do not match"))
			result.OperationalTaskRunGraph = false
		}
	}
}
