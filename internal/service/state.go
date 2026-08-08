package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

type StateIssue struct {
	Code      string `json:"code"`
	ProjectID string `json:"project_id,omitempty"`
	TaskID    string `json:"task_id,omitempty"`
	RunID     string `json:"run_id,omitempty"`
	Path      string `json:"path,omitempty"`
	Detail    string `json:"detail"`
}

type StatePlan struct {
	ProjectID    string `json:"project_id"`
	Valid        bool   `json:"valid"`
	ActiveTaskID string `json:"active_task_id,omitempty"`
	ActiveRunID  string `json:"active_run_id,omitempty"`
}

type StateCheckResult struct {
	Valid                   bool         `json:"valid"`
	HubRevision             string       `json:"hub_revision,omitempty"`
	ConfiguredProjectIDs    []string     `json:"configured_project_ids"`
	DurableProjectIDs       []string     `json:"durable_project_ids"`
	ValidCurrentPlans       []string     `json:"valid_current_plans"`
	OperationalTaskRunGraph bool         `json:"operational_task_run_graph"`
	Plans                   []StatePlan  `json:"plans"`
	Issues                  []StateIssue `json:"issues"`
}

type StateRepairAction struct {
	Kind        string `json:"kind"`
	ProjectID   string `json:"project_id"`
	TaskID      string `json:"task_id,omitempty"`
	Path        string `json:"path"`
	ClearTaskID bool   `json:"clear_active_task_id"`
	ClearRunID  bool   `json:"clear_active_run_id"`
	OldTaskID   string `json:"old_active_task_id,omitempty"`
	OldRunID    string `json:"old_active_run_id,omitempty"`
	OldStatus   string `json:"old_task_status,omitempty"`
	NewStatus   string `json:"new_task_status,omitempty"`
	Reason      string `json:"reason"`
}

type StateRepairResult struct {
	DryRun       bool                `json:"dry_run"`
	Applied      bool                `json:"applied"`
	OldHubSHA    string              `json:"old_hub_sha,omitempty"`
	NewHubSHA    string              `json:"new_hub_sha,omitempty"`
	Backup       string              `json:"backup,omitempty"`
	ChangedPaths []string            `json:"changed_paths,omitempty"`
	Actions      []StateRepairAction `json:"actions"`
	Check        StateCheckResult    `json:"check"`
}

const historyOnlyTaskRepairReason = "close mutable dispatched state after linked run became immutable workflow-v1 history during protocol cutover"

func stateIssue(code, project, task, run, path, detail string) StateIssue {
	return StateIssue{Code: code, ProjectID: project, TaskID: task, RunID: run, Path: path, Detail: detail}
}

func (s *Service) StateCheck(ctx context.Context) (StateCheckResult, error) {
	result := StateCheckResult{ConfiguredProjectIDs: []string{}, DurableProjectIDs: []string{}, ValidCurrentPlans: []string{}, Plans: []StatePlan{}, Issues: []StateIssue{}, OperationalTaskRunGraph: true}
	configuredIDs, resolution, err := s.effectiveProjectIDs()
	if err != nil {
		result.Issues = append(result.Issues, stateIssue("CONFIGURED_PROJECTS_INVALID", "", "", "", "", err.Error()))
		result.Valid = false
		result.OperationalTaskRunGraph = false
		return result, nil
	}
	result.ConfiguredProjectIDs = append(result.ConfiguredProjectIDs, configuredIDs...)
	revision, err := s.Hub.RemoteRevision(ctx)
	if err != nil {
		result.Issues = append(result.Issues, stateIssue("HUB_UNAVAILABLE", "", "", "", "", err.Error()))
		result.Valid = false
		return result, nil
	}
	result.HubRevision = revision
	projects, err := s.ProjectList(ctx)
	if err != nil {
		result.Issues = append(result.Issues, stateIssue("DURABLE_PROJECTS_UNAVAILABLE", "", "", "", "", err.Error()))
		result.Valid = false
		return result, nil
	}
	durable := map[string]model.Project{}
	for _, project := range projects {
		if err := model.ValidateProject(project); err != nil {
			result.Issues = append(result.Issues, stateIssue("INVALID_DURABLE_PROJECT", project.ID, "", "", s.projectPath(project.ID), err.Error()))
			continue
		}
		if _, exists := durable[project.ID]; exists {
			result.Issues = append(result.Issues, stateIssue("DUPLICATE_DURABLE_PROJECT", project.ID, "", "", s.projectPath(project.ID), "duplicate project ID"))
			continue
		}
		durable[project.ID] = project
		result.DurableProjectIDs = append(result.DurableProjectIDs, project.ID)
	}
	sort.Strings(result.DurableProjectIDs)
	for _, project := range projects {
		if project.Status == "active" {
			if _, configured := resolution.Projects[project.ID]; !configured {
				result.Issues = append(result.Issues, stateIssue("DURABLE_PROJECT_NOT_CONFIGURED", project.ID, "", "", s.projectPath(project.ID), "active durable project is not configured"))
			}
		}
	}
	for _, id := range result.ConfiguredProjectIDs {
		project, exists := durable[id]
		if !exists {
			result.Issues = append(result.Issues, stateIssue("CONFIGURED_PROJECT_MISSING", id, "", "", s.projectPath(id), "configured project has no durable project record"))
			continue
		}
		if project.Status != "active" {
			result.Issues = append(result.Issues, stateIssue("CONFIGURED_PROJECT_NOT_ACTIVE", id, "", "", s.projectPath(id), "configured project is not active"))
		}
		plan, planErr := s.PlanRead(ctx, id)
		if planErr != nil {
			if raw, rawErr := s.Hub.ReadFile(ctx, s.planPath(id)); rawErr == nil {
				var object map[string]any
				if json.Unmarshal(raw, &object) == nil {
					if _, hasBody := object["body"]; hasBody {
						result.Issues = append(result.Issues, stateIssue("LEGACY_PLAN_BODY", id, "", "", s.planPath(id), "workflow-v1 plan contains obsolete body field"))
					}
				}
			}
			result.Issues = append(result.Issues, stateIssue("CURRENT_PLAN_INVALID", id, "", "", s.planPath(id), planErr.Error()))
			result.Plans = append(result.Plans, StatePlan{ProjectID: id, Valid: false})
			continue
		}
		result.ValidCurrentPlans = append(result.ValidCurrentPlans, id)
		result.Plans = append(result.Plans, StatePlan{ProjectID: id, Valid: true, ActiveTaskID: plan.ActiveTaskID, ActiveRunID: plan.ActiveRunID})
	}
	graphSnapshot, snapshotErr := s.Hub.ReadSnapshot(ctx)
	if snapshotErr != nil {
		result.Issues = append(result.Issues, stateIssue("HUB_UNAVAILABLE", "", "", "", "", snapshotErr.Error()))
		result.OperationalTaskRunGraph = false
	} else {
		result.HubRevision = graphSnapshot.Revision()
		for _, plan := range result.Plans {
			s.checkProjectTaskRunGraph(ctx, graphSnapshot, plan.ProjectID, model.Plan{ProjectID: plan.ProjectID, ActiveTaskID: plan.ActiveTaskID, ActiveRunID: plan.ActiveRunID}, &result)
		}
		if err := graphSnapshot.Close(); err != nil {
			result.Issues = append(result.Issues, stateIssue("HUB_UNAVAILABLE", "", "", "", "", err.Error()))
			result.OperationalTaskRunGraph = false
		}
	}
	sort.Strings(result.ValidCurrentPlans)
	result.Valid = len(result.Issues) == 0
	result.OperationalTaskRunGraph = result.OperationalTaskRunGraph && result.Valid
	return result, nil
}

type taskRunGraphSnapshot struct {
	tasks []TaskRecord
	runs  []model.Run
}

func (s *Service) readTaskRunGraph(ctx context.Context, snapshot *hub.ReadSnapshot, project string) (taskRunGraphSnapshot, error) {
	taskPaths, err := snapshot.List(ctx, s.projectPrefix(project)+"/tasks", ".json")
	if err != nil {
		return taskRunGraphSnapshot{}, err
	}
	items := make([]TaskRecord, 0, len(taskPaths))
	for _, path := range taskPaths {
		if strings.HasSuffix(path, ".state.json") || strings.HasSuffix(path, ".run-counter.json") || strings.Contains(path, "/revisions/") {
			continue
		}
		var task model.Task
		if err := snapshot.ReadJSON(ctx, path, &task); err != nil {
			return taskRunGraphSnapshot{}, err
		}
		state, err := s.readTaskStateFromSnapshot(ctx, snapshot, task)
		if err != nil {
			return taskRunGraphSnapshot{}, err
		}
		items = append(items, TaskRecord{Task: task, State: state})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Task.CreatedAt.After(items[j].Task.CreatedAt) })

	runPaths, err := snapshot.List(ctx, s.projectPrefix(project)+"/runs", "/run.json")
	if err != nil {
		return taskRunGraphSnapshot{}, err
	}
	runs := make([]model.Run, 0, len(runPaths))
	for _, path := range runPaths {
		raw, err := snapshot.ReadFile(ctx, path)
		if err != nil {
			return taskRunGraphSnapshot{}, err
		}
		run, _, err := model.DecodeRunRecord(raw)
		if err != nil {
			return taskRunGraphSnapshot{}, err
		}
		runs = append(runs, run)
	}
	sort.Slice(runs, func(i, j int) bool {
		if runs[i].CreatedAt.Equal(runs[j].CreatedAt) {
			return runs[i].ID > runs[j].ID
		}
		return runs[i].CreatedAt.After(runs[j].CreatedAt)
	})
	return taskRunGraphSnapshot{tasks: items, runs: runs}, nil
}

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

func (s *Service) StateRepair(ctx context.Context, apply bool) (StateRepairResult, error) {
	check, err := s.StateCheck(ctx)
	if err != nil {
		return StateRepairResult{}, err
	}
	result := StateRepairResult{DryRun: !apply, OldHubSHA: check.HubRevision, Check: check, Actions: []StateRepairAction{}}
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
