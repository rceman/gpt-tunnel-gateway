package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/airelay"
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/gitx"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

type Service struct {
	Config  config.Config
	Hub     hub.Store
	Git     gitx.Runner
	Airelay airelay.Client
}

func New(c config.Config) *Service {
	return &Service{Config: c, Hub: hub.Store{Config: c}, Git: gitx.Runner{MaxReadBytes: c.MaxReadBytes, MaxDiffBytes: c.MaxDiffBytes, MaxListItems: c.MaxListItems}, Airelay: airelay.Client{Command: c.AirelayCommand, Timeout: time.Duration(c.DispatchTimeoutSeconds) * time.Second, MaxMessageBytes: 256}}
}

type WriteOptions struct {
	ExpectedHubRevision string `json:"expected_hub_revision"`
}
type ProjectRegisterInput struct {
	Project model.Project `json:"project"`
	WriteOptions
}
type PlanUpdateInput struct {
	ProjectID    string `json:"project_id"`
	Summary      string `json:"summary"`
	Body         string `json:"body"`
	ActiveTaskID string `json:"active_task_id,omitempty"`
	ActiveRunID  string `json:"active_run_id,omitempty"`
	UpdatedBy    string `json:"updated_by"`
	WriteOptions
}
type ADRCreateInput struct {
	ADR model.ADR `json:"adr"`
	WriteOptions
}
type TaskCreateInput struct {
	ProjectID          string   `json:"project_id"`
	Title              string   `json:"title"`
	Objective          string   `json:"objective"`
	Branch             string   `json:"branch"`
	BaseRevision       string   `json:"base_revision"`
	AcceptanceCriteria []string `json:"acceptance_criteria"`
	Constraints        []string `json:"constraints"`
	RequiredGates      []string `json:"required_gates,omitempty"`
	CreatedBy          string   `json:"created_by"`
	Supersedes         string   `json:"supersedes,omitempty"`
	WriteOptions
}
type DispatchInput struct {
	TaskID string `json:"task_id"`
	WriteOptions
}
type FinalizeInput struct {
	RunID        string `json:"run_id"`
	ResultFile   string `json:"result_file,omitempty"`
	EvidenceFile string `json:"evidence_file,omitempty"`
	WriteOptions
}

type OperationResult struct {
	Hub       hub.TransactionResult `json:"hub"`
	ProjectID string                `json:"project_id,omitempty"`
	TaskID    string                `json:"task_id,omitempty"`
	RunID     string                `json:"run_id,omitempty"`
	Status    string                `json:"status"`
}

type ProjectStatus struct {
	Project     model.Project        `json:"project"`
	Local       config.ProjectConfig `json:"local"`
	Worktree    gitx.WorktreeStatus  `json:"worktree"`
	HubRevision string               `json:"hub_revision"`
}

type TaskPacket struct {
	Task            model.Task    `json:"task"`
	Run             model.Run     `json:"run"`
	Project         model.Project `json:"project"`
	Plan            model.Plan    `json:"plan"`
	RepositoryRoot  string        `json:"repository_root"`
	ResultPath      string        `json:"result_path"`
	EvidencePath    string        `json:"evidence_path"`
	FinalizeCommand string        `json:"finalize_command"`
	Text            string        `json:"text"`
}

func (s *Service) projectPrefix(id string) string {
	return filepath.ToSlash(filepath.Join(s.Config.Hub.ProtocolRoot, "projects", id))
}
func (s *Service) projectPath(id string) string { return s.projectPrefix(id) + "/project.json" }
func (s *Service) planPath(id string) string    { return s.projectPrefix(id) + "/plan/current.json" }
func (s *Service) adrPath(project, id string) string {
	return s.projectPrefix(project) + "/adrs/" + id + ".json"
}
func (s *Service) taskPath(project, id string) string {
	return s.projectPrefix(project) + "/tasks/" + id + ".json"
}
func (s *Service) taskStatePath(project, id string) string {
	return s.projectPrefix(project) + "/tasks/" + id + ".state.json"
}
func (s *Service) runPrefix(project, id string) string {
	return s.projectPrefix(project) + "/runs/" + id
}
func (s *Service) runPath(project, id string) string { return s.runPrefix(project, id) + "/run.json" }
func (s *Service) resultPath(project, id string) string {
	return s.runPrefix(project, id) + "/agent-result.json"
}
func (s *Service) evidencePath(project, id string) string {
	return s.runPrefix(project, id) + "/evidence.json"
}
func (s *Service) reportPath(project, id string) string {
	return s.runPrefix(project, id) + "/report.json"
}

func decodeStrict(data []byte, out any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	return dec.Decode(out)
}
func (s *Service) projectConfig(id string) (config.ProjectConfig, error) {
	p, ok := s.Config.Projects[id]
	if !ok {
		return config.ProjectConfig{}, fmt.Errorf("unknown local project %q", id)
	}
	return p, nil
}
func (s *Service) hubRevision(ctx context.Context) (string, error) { return s.Hub.RemoteRevision(ctx) }

func (s *Service) ProjectList(ctx context.Context) ([]model.Project, error) {
	paths, err := s.Hub.List(ctx, s.Config.Hub.ProtocolRoot+"/projects", "/project.json")
	if err != nil {
		return nil, err
	}
	items := []model.Project{}
	for _, path := range paths {
		var p model.Project
		if err := s.Hub.ReadJSON(ctx, path, &p); err != nil {
			return nil, err
		}
		items = append(items, p)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items, nil
}
func (s *Service) ProjectRead(ctx context.Context, id string) (model.Project, error) {
	var p model.Project
	err := s.Hub.ReadJSON(ctx, s.projectPath(id), &p)
	return p, err
}
func (s *Service) ProjectRegister(ctx context.Context, in ProjectRegisterInput) (OperationResult, error) {
	p := in.Project
	now := time.Now().UTC()
	p.SchemaVersion = model.SchemaVersion
	if p.CreatedAt.IsZero() {
		p.CreatedAt = now
	}
	p.UpdatedAt = now
	if p.Status == "" {
		p.Status = "active"
	}
	if err := model.ValidateProject(p); err != nil {
		return OperationResult{}, err
	}
	if _, err := s.projectConfig(p.ID); err != nil {
		return OperationResult{}, err
	}
	tx, err := s.Hub.Transact(ctx, in.ExpectedHubRevision, "gateway: register project "+p.ID, func(w string) ([]string, error) {
		path := s.projectPath(p.ID)
		if _, err := os.Stat(filepath.Join(w, filepath.FromSlash(path))); err == nil {
			return nil, fmt.Errorf("project already exists")
		}
		if err := hub.WriteJSON(w, path, p); err != nil {
			return nil, err
		}
		return []string{path}, nil
	})
	if err != nil {
		return OperationResult{}, err
	}
	return OperationResult{Hub: tx, ProjectID: p.ID, Status: "registered"}, nil
}
func (s *Service) ProjectStatus(ctx context.Context, id string) (ProjectStatus, error) {
	p, err := s.ProjectRead(ctx, id)
	if err != nil {
		return ProjectStatus{}, err
	}
	local, err := s.projectConfig(id)
	if err != nil {
		return ProjectStatus{}, err
	}
	wt, err := s.Git.WorktreeStatus(ctx, local)
	if err != nil {
		return ProjectStatus{}, err
	}
	rev, err := s.hubRevision(ctx)
	if err != nil {
		return ProjectStatus{}, err
	}
	return ProjectStatus{Project: p, Local: local, Worktree: wt, HubRevision: rev}, nil
}

func (s *Service) PlanRead(ctx context.Context, project string) (model.Plan, error) {
	var p model.Plan
	err := s.Hub.ReadJSON(ctx, s.planPath(project), &p)
	return p, err
}
func (s *Service) PlanUpdate(ctx context.Context, in PlanUpdateInput) (OperationResult, error) {
	if _, err := s.ProjectRead(ctx, in.ProjectID); err != nil {
		return OperationResult{}, err
	}
	old := model.Plan{}
	_ = s.Hub.ReadJSON(ctx, s.planPath(in.ProjectID), &old)
	plan := model.Plan{SchemaVersion: model.SchemaVersion, ProjectID: in.ProjectID, Revision: old.Revision + 1, Summary: in.Summary, Body: in.Body, ActiveTaskID: in.ActiveTaskID, ActiveRunID: in.ActiveRunID, UpdatedBy: in.UpdatedBy, UpdatedAt: time.Now().UTC()}
	if err := model.ValidatePlan(plan); err != nil {
		return OperationResult{}, err
	}
	tx, err := s.Hub.Transact(ctx, in.ExpectedHubRevision, "gateway: update plan "+in.ProjectID, func(w string) ([]string, error) {
		path := s.planPath(in.ProjectID)
		if err := hub.WriteJSON(w, path, plan); err != nil {
			return nil, err
		}
		return []string{path}, nil
	})
	if err != nil {
		return OperationResult{}, err
	}
	return OperationResult{Hub: tx, ProjectID: in.ProjectID, Status: "updated"}, nil
}
func (s *Service) PlanHistory(ctx context.Context, project string, limit int) ([]map[string]string, error) {
	return s.Hub.History(ctx, s.planPath(project), limit)
}

func (s *Service) ADRList(ctx context.Context, project string) ([]model.ADR, error) {
	paths, err := s.Hub.List(ctx, s.projectPrefix(project)+"/adrs", ".json")
	if err != nil {
		return nil, err
	}
	items := []model.ADR{}
	for _, path := range paths {
		var v model.ADR
		if err := s.Hub.ReadJSON(ctx, path, &v); err != nil {
			return nil, err
		}
		items = append(items, v)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items, nil
}
func (s *Service) ADRRead(ctx context.Context, project, id string) (model.ADR, error) {
	var v model.ADR
	err := s.Hub.ReadJSON(ctx, s.adrPath(project, id), &v)
	return v, err
}
func (s *Service) ADRCreate(ctx context.Context, in ADRCreateInput) (OperationResult, error) {
	v := in.ADR
	v.SchemaVersion = model.SchemaVersion
	if v.ID == "" {
		id, err := model.NewID()
		if err != nil {
			return OperationResult{}, err
		}
		v.ID = "ADR-" + strings.ToUpper(strings.ReplaceAll(id[:8], "-", ""))
	}
	v.CreatedAt = time.Now().UTC()
	if v.Status == "" {
		v.Status = "accepted"
	}
	if err := model.ValidateADR(v); err != nil {
		return OperationResult{}, err
	}
	if _, err := s.ProjectRead(ctx, v.ProjectID); err != nil {
		return OperationResult{}, err
	}
	tx, err := s.Hub.Transact(ctx, in.ExpectedHubRevision, "gateway: create ADR "+v.ID, func(w string) ([]string, error) {
		path := s.adrPath(v.ProjectID, v.ID)
		if _, err := os.Stat(filepath.Join(w, filepath.FromSlash(path))); err == nil {
			return nil, fmt.Errorf("ADR already exists")
		}
		if err := hub.WriteJSON(w, path, v); err != nil {
			return nil, err
		}
		return []string{path}, nil
	})
	if err != nil {
		return OperationResult{}, err
	}
	return OperationResult{Hub: tx, ProjectID: v.ProjectID, Status: "created"}, nil
}

func (s *Service) TaskCreate(ctx context.Context, in TaskCreateInput) (model.Task, OperationResult, error) {
	if _, err := s.ProjectRead(ctx, in.ProjectID); err != nil {
		return model.Task{}, OperationResult{}, err
	}
	id, err := model.NewID()
	if err != nil {
		return model.Task{}, OperationResult{}, err
	}
	task := model.Task{SchemaVersion: model.SchemaVersion, ID: id, ProjectID: in.ProjectID, Title: in.Title, Objective: in.Objective, Branch: in.Branch, BaseRevision: strings.ToLower(in.BaseRevision), AcceptanceCriteria: append([]string{}, in.AcceptanceCriteria...), Constraints: append([]string{}, in.Constraints...), RequiredGates: append([]string{}, in.RequiredGates...), Status: "created", Supersedes: in.Supersedes, CreatedBy: in.CreatedBy, CreatedAt: time.Now().UTC()}
	hash, err := model.HashTask(task)
	if err != nil {
		return model.Task{}, OperationResult{}, err
	}
	task.SHA256 = hash
	if err := model.ValidateTask(task); err != nil {
		return model.Task{}, OperationResult{}, err
	}
	state := model.TaskState{SchemaVersion: model.SchemaVersion, TaskID: task.ID, TaskSHA256: task.SHA256, Status: "created", UpdatedAt: time.Now().UTC()}
	tx, err := s.Hub.Transact(ctx, in.ExpectedHubRevision, "gateway: create task "+task.ID, func(w string) ([]string, error) {
		path := s.taskPath(task.ProjectID, task.ID)
		statePath := s.taskStatePath(task.ProjectID, task.ID)
		if err := hub.WriteJSON(w, path, task); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(w, statePath, state); err != nil {
			return nil, err
		}
		return []string{path, statePath}, nil
	})
	if err != nil {
		return model.Task{}, OperationResult{}, err
	}
	return task, OperationResult{Hub: tx, ProjectID: task.ProjectID, TaskID: task.ID, Status: "created"}, nil
}
func (s *Service) taskState(ctx context.Context, task model.Task) (model.TaskState, error) {
	var state model.TaskState
	err := s.Hub.ReadJSON(ctx, s.taskStatePath(task.ProjectID, task.ID), &state)
	if err != nil {
		return model.TaskState{SchemaVersion: model.SchemaVersion, TaskID: task.ID, TaskSHA256: task.SHA256, Status: "created", UpdatedAt: task.CreatedAt}, nil
	}
	if err := model.ValidateTaskState(state, task); err != nil {
		return model.TaskState{}, err
	}
	return state, nil
}
func (s *Service) updateTaskState(ctx context.Context, task model.Task, state model.TaskState, expected, subject string) (hub.TransactionResult, error) {
	if err := model.ValidateTaskState(state, task); err != nil {
		return hub.TransactionResult{}, err
	}
	return s.Hub.Transact(ctx, expected, subject, func(w string) ([]string, error) {
		path := s.taskStatePath(task.ProjectID, task.ID)
		if err := hub.WriteJSON(w, path, state); err != nil {
			return nil, err
		}
		return []string{path}, nil
	})
}

func (s *Service) TaskList(ctx context.Context, project string) ([]model.Task, error) {
	paths, err := s.Hub.List(ctx, s.projectPrefix(project)+"/tasks", ".json")
	if err != nil {
		return nil, err
	}
	items := []model.Task{}
	for _, path := range paths {
		var v model.Task
		if err := s.Hub.ReadJSON(ctx, path, &v); err != nil {
			return nil, err
		}
		if state, err := s.taskState(ctx, v); err == nil {
			v.Status = state.Status
		}
		items = append(items, v)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.After(items[j].CreatedAt) })
	return items, nil
}
func (s *Service) findTask(ctx context.Context, id string) (model.Task, error) {
	projects, err := s.ProjectList(ctx)
	if err != nil {
		return model.Task{}, err
	}
	for _, p := range projects {
		var t model.Task
		if err := s.Hub.ReadJSON(ctx, s.taskPath(p.ID, id), &t); err == nil {
			if state, e := s.taskState(ctx, t); e == nil {
				t.Status = state.Status
			}
			return t, nil
		}
	}
	return model.Task{}, fmt.Errorf("task not found: %s", id)
}
func (s *Service) TaskReadRecord(ctx context.Context, id string) (model.Task, error) {
	return s.findTask(ctx, id)
}
func (s *Service) TaskSupersede(ctx context.Context, oldID string, in TaskCreateInput) (model.Task, OperationResult, error) {
	old, err := s.findTask(ctx, oldID)
	if err != nil {
		return model.Task{}, OperationResult{}, err
	}
	if in.ProjectID == "" {
		in.ProjectID = old.ProjectID
	}
	if in.ProjectID != old.ProjectID {
		return model.Task{}, OperationResult{}, fmt.Errorf("superseding task must remain in project")
	}
	id, err := model.NewID()
	if err != nil {
		return model.Task{}, OperationResult{}, err
	}
	now := time.Now().UTC()
	newTask := model.Task{SchemaVersion: model.SchemaVersion, ID: id, ProjectID: in.ProjectID, Title: in.Title, Objective: in.Objective, Branch: in.Branch, BaseRevision: strings.ToLower(in.BaseRevision), AcceptanceCriteria: append([]string{}, in.AcceptanceCriteria...), Constraints: append([]string{}, in.Constraints...), RequiredGates: append([]string{}, in.RequiredGates...), Status: "created", Supersedes: old.ID, CreatedBy: in.CreatedBy, CreatedAt: now}
	hash, err := model.HashTask(newTask)
	if err != nil {
		return model.Task{}, OperationResult{}, err
	}
	newTask.SHA256 = hash
	if err := model.ValidateTask(newTask); err != nil {
		return model.Task{}, OperationResult{}, err
	}
	original := old
	original.Status = "created"
	oldState, err := s.taskState(ctx, original)
	if err != nil {
		return model.Task{}, OperationResult{}, err
	}
	if oldState.Status == "completed" || oldState.Status == "cancelled" || oldState.Status == "superseded" {
		return model.Task{}, OperationResult{}, fmt.Errorf("cannot supersede terminal task")
	}
	oldState.Status = "superseded"
	oldState.SupersededBy = newTask.ID
	oldState.UpdatedAt = now
	newState := model.TaskState{SchemaVersion: model.SchemaVersion, TaskID: newTask.ID, TaskSHA256: newTask.SHA256, Status: "created", UpdatedAt: now}
	tx, err := s.Hub.Transact(ctx, in.ExpectedHubRevision, "gateway: supersede task "+old.ID, func(w string) ([]string, error) {
		paths := []string{s.taskPath(newTask.ProjectID, newTask.ID), s.taskStatePath(newTask.ProjectID, newTask.ID), s.taskStatePath(old.ProjectID, old.ID)}
		vals := []any{newTask, newState, oldState}
		for i, p := range paths {
			if err := hub.WriteJSON(w, p, vals[i]); err != nil {
				return nil, err
			}
		}
		return paths, nil
	})
	if err != nil {
		return model.Task{}, OperationResult{}, err
	}
	return newTask, OperationResult{Hub: tx, ProjectID: newTask.ProjectID, TaskID: newTask.ID, Status: "created"}, nil
}

func (s *Service) TaskCancel(ctx context.Context, id, expected string) (OperationResult, error) {
	task, err := s.findTask(ctx, id)
	if err != nil {
		return OperationResult{}, err
	}
	runs, err := s.RunList(ctx, task.ProjectID)
	if err != nil {
		return OperationResult{}, err
	}
	for _, run := range runs {
		if run.TaskID == task.ID && activeStatus(run.Status) {
			return OperationResult{}, fmt.Errorf("task has active run %s; cancel the run instead", run.ID)
		}
	}
	original := task
	original.Status = "created"
	state, err := s.taskState(ctx, original)
	if err != nil {
		return OperationResult{}, err
	}
	if state.Status == "cancelled" || state.Status == "completed" || state.Status == "superseded" {
		return OperationResult{}, fmt.Errorf("task is terminal: %s", state.Status)
	}
	state.Status = "cancelled"
	state.UpdatedAt = time.Now().UTC()
	tx, err := s.updateTaskState(ctx, original, state, expected, "gateway: cancel task "+id)
	if err != nil {
		return OperationResult{}, err
	}
	return OperationResult{Hub: tx, ProjectID: task.ProjectID, TaskID: id, Status: "cancelled"}, nil
}

func (s *Service) RunList(ctx context.Context, project string) ([]model.Run, error) {
	paths, err := s.Hub.List(ctx, s.projectPrefix(project)+"/runs", "/run.json")
	if err != nil {
		return nil, err
	}
	items := []model.Run{}
	for _, path := range paths {
		var v model.Run
		if err := s.Hub.ReadJSON(ctx, path, &v); err != nil {
			return nil, err
		}
		items = append(items, v)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.After(items[j].CreatedAt) })
	return items, nil
}
func (s *Service) findRun(ctx context.Context, id string) (model.Run, error) {
	projects, err := s.ProjectList(ctx)
	if err != nil {
		return model.Run{}, err
	}
	for _, p := range projects {
		var r model.Run
		if err := s.Hub.ReadJSON(ctx, s.runPath(p.ID, id), &r); err == nil {
			return r, nil
		}
	}
	return model.Run{}, fmt.Errorf("run not found: %s", id)
}
func (s *Service) RunRead(ctx context.Context, id string) (model.Run, error) {
	return s.findRun(ctx, id)
}
func activeStatus(s string) bool {
	switch s {
	case "created", "dispatching", "dispatched", "awaiting_result", "cancel_requested":
		return true
	}
	return false
}
func (s *Service) checkSessionAvailable(ctx context.Context, project, session string) error {
	runs, err := s.RunList(ctx, project)
	if err != nil {
		return err
	}
	for _, r := range runs {
		if r.SessionKey == session && activeStatus(r.Status) {
			return fmt.Errorf("session %s already has active run %s", session, r.ID)
		}
	}
	return nil
}
func (s *Service) localRunDir(id string) string { return filepath.Join(s.Config.StateDir, "runs", id) }
func (s *Service) writeLocalRun(run model.Run, task model.Task) error {
	dir := s.localRunDir(run.ID)
	if err := fsutil.EnsureDir(dir, 0o700); err != nil {
		return err
	}
	if err := fsutil.WriteJSONAtomic(filepath.Join(dir, "run.json"), run, 0o600); err != nil {
		return err
	}
	return fsutil.WriteJSONAtomic(filepath.Join(dir, "task.json"), task, 0o600)
}

func (s *Service) TaskDispatch(ctx context.Context, in DispatchInput) (model.Run, OperationResult, error) {
	task, err := s.findTask(ctx, in.TaskID)
	if err != nil {
		return model.Run{}, OperationResult{}, err
	}
	if task.Status != "created" && task.Status != "ready" {
		return model.Run{}, OperationResult{}, fmt.Errorf("task is not dispatchable: %s", task.Status)
	}
	plan, err := s.PlanRead(ctx, task.ProjectID)
	if err != nil {
		return model.Run{}, OperationResult{}, err
	}
	if plan.ActiveTaskID != task.ID {
		return model.Run{}, OperationResult{}, fmt.Errorf("global plan does not identify task as active")
	}
	local, err := s.projectConfig(task.ProjectID)
	if err != nil {
		return model.Run{}, OperationResult{}, err
	}
	if err := s.checkSessionAvailable(ctx, task.ProjectID, local.AirelaySessionKey); err != nil {
		return model.Run{}, OperationResult{}, err
	}
	projectLock, err := lockfile.Acquire(filepath.Join(s.Config.StateDir, "locks"), "project-"+task.ProjectID)
	if err != nil {
		return model.Run{}, OperationResult{}, err
	}
	defer projectLock.Release()
	wt, err := s.Git.WorktreeStatus(ctx, local)
	if err != nil {
		return model.Run{}, OperationResult{}, err
	}
	if !wt.Clean {
		return model.Run{}, OperationResult{}, fmt.Errorf("project worktree is dirty")
	}
	resolved, err := s.Git.Resolve(ctx, local.Root, task.BaseRevision)
	if err != nil || resolved != task.BaseRevision {
		return model.Run{}, OperationResult{}, fmt.Errorf("task base unavailable or mismatched")
	}
	id, err := model.NewID()
	if err != nil {
		return model.Run{}, OperationResult{}, err
	}
	now := time.Now().UTC()
	run := model.Run{SchemaVersion: model.SchemaVersion, ID: id, TaskID: task.ID, TaskSHA256: task.SHA256, ProjectID: task.ProjectID, GatewayID: s.Config.GatewayID, SessionKey: local.AirelaySessionKey, Branch: task.Branch, BaseRevision: task.BaseRevision, Status: "created", ResultPath: filepath.Join(s.localRunDir(id), "agent-result.json"), EvidencePath: filepath.Join(s.localRunDir(id), "evidence.json"), CreatedAt: now}
	if err := model.ValidateRun(run); err != nil {
		return model.Run{}, OperationResult{}, err
	}
	if err := s.writeLocalRun(run, task); err != nil {
		return model.Run{}, OperationResult{}, err
	}
	plan.Revision++
	plan.ActiveRunID = run.ID
	plan.UpdatedBy = s.Config.GatewayID
	plan.UpdatedAt = now
	taskState := model.TaskState{SchemaVersion: model.SchemaVersion, TaskID: task.ID, TaskSHA256: task.SHA256, Status: "dispatched", UpdatedAt: now}
	tx, err := s.Hub.Transact(ctx, in.ExpectedHubRevision, "gateway: create run "+run.ID, func(w string) ([]string, error) {
		paths := []string{s.runPath(run.ProjectID, run.ID), s.taskStatePath(task.ProjectID, task.ID), s.planPath(task.ProjectID)}
		vals := []any{run, taskState, plan}
		for i, path := range paths {
			if err := hub.WriteJSON(w, path, vals[i]); err != nil {
				return nil, err
			}
		}
		return paths, nil
	})
	if err != nil {
		return model.Run{}, OperationResult{}, err
	}
	run.HubRevision = tx.After
	if err := s.Git.PrepareBranch(ctx, local, task.Branch, task.BaseRevision); err != nil {
		_, _ = s.failRun(ctx, run, task, "failed", "repository preparation failed: "+err.Error(), tx.After)
		return run, OperationResult{}, err
	}
	message := "Read task and execute it. Run: gpt-tunnel task read " + task.ID
	run.Status = "dispatching"
	run.DispatchMessage = message
	dispatch, err := s.Airelay.Prompt(ctx, run.SessionKey, message)
	code := dispatch.ExitCode
	run.DispatchExitCode = &code
	run.DispatchStdout = dispatch.Stdout
	run.DispatchStderr = dispatch.Stderr
	dispatchedAt := dispatch.FinishedAt
	run.DispatchedAt = &dispatchedAt
	if err != nil {
		tx2, e := s.failRun(ctx, run, task, "failed", "Airelay dispatch failed: "+err.Error(), tx.After)
		if e != nil {
			return run, OperationResult{}, fmt.Errorf("dispatch failed (%v), recording failed (%v)", err, e)
		}
		run.Status = "failed"
		return run, OperationResult{Hub: tx2, ProjectID: run.ProjectID, TaskID: run.TaskID, RunID: run.ID, Status: run.Status}, err
	}
	run.Status = "awaiting_result"
	tx2, err := s.updateRun(ctx, run, tx.After, "gateway: dispatch run "+run.ID)
	if err != nil {
		return run, OperationResult{}, err
	}
	_ = s.writeLocalRun(run, task)
	return run, OperationResult{Hub: tx2, ProjectID: run.ProjectID, TaskID: run.TaskID, RunID: run.ID, Status: run.Status}, nil
}
func (s *Service) updateRun(ctx context.Context, run model.Run, expected, subject string) (hub.TransactionResult, error) {
	return s.Hub.Transact(ctx, expected, subject, func(w string) ([]string, error) {
		path := s.runPath(run.ProjectID, run.ID)
		if err := hub.WriteJSON(w, path, run); err != nil {
			return nil, err
		}
		return []string{path}, nil
	})
}
func taskStateStatusForResult(status string) string {
	switch status {
	case "succeeded":
		return "completed"
	case "cancelled":
		return "cancelled"
	default:
		return "ready"
	}
}
func (s *Service) failRun(ctx context.Context, run model.Run, task model.Task, status, summary, expected string) (hub.TransactionResult, error) {
	now := time.Now().UTC()
	run.Status = status
	run.FinishedAt = &now
	local, _ := s.projectConfig(task.ProjectID)
	head := task.BaseRevision
	branch := task.Branch
	clean := false
	if local.Root != "" {
		if h, b, c, e := s.Git.CurrentHead(ctx, local); e == nil {
			head, branch, clean = h, b, c
		}
	}
	result := model.AgentResult{SchemaVersion: model.SchemaVersion, TaskID: task.ID, TaskSHA256: task.SHA256, RunID: run.ID, Status: status, Summary: summary, FinishedAt: now}
	evidence := model.Evidence{SchemaVersion: model.SchemaVersion, TaskID: task.ID, RunID: run.ID, ProjectHead: head, Branch: branch, WorktreeClean: clean, Notes: []string{summary}, RecordedAt: now}
	report := model.Report{SchemaVersion: model.SchemaVersion, TaskID: task.ID, RunID: run.ID, ProjectID: task.ProjectID, Status: status, Summary: summary, FinishedAt: now}
	state := model.TaskState{SchemaVersion: model.SchemaVersion, TaskID: task.ID, TaskSHA256: task.SHA256, Status: taskStateStatusForResult(status), UpdatedAt: now}
	plan, err := s.PlanRead(ctx, task.ProjectID)
	if err != nil {
		return hub.TransactionResult{}, err
	}
	plan.Revision++
	plan.ActiveRunID = ""
	if status == "succeeded" || status == "cancelled" {
		plan.ActiveTaskID = ""
	}
	plan.UpdatedBy = s.Config.GatewayID
	plan.UpdatedAt = now
	return s.Hub.Transact(ctx, expected, "gateway: finalize failed run "+run.ID, func(w string) ([]string, error) {
		paths := []string{s.runPath(run.ProjectID, run.ID), s.resultPath(run.ProjectID, run.ID), s.evidencePath(run.ProjectID, run.ID), s.reportPath(run.ProjectID, run.ID), s.taskStatePath(task.ProjectID, task.ID), s.planPath(task.ProjectID)}
		vals := []any{run, result, evidence, report, state, plan}
		for i, p := range paths {
			if err := hub.WriteJSON(w, p, vals[i]); err != nil {
				return nil, err
			}
		}
		return paths, nil
	})
}

func (s *Service) TaskRead(ctx context.Context, id string) (TaskPacket, error) {
	task, err := s.findTask(ctx, id)
	if err != nil {
		return TaskPacket{}, err
	}
	runs, err := s.RunList(ctx, task.ProjectID)
	if err != nil {
		return TaskPacket{}, err
	}
	matches := []model.Run{}
	for _, r := range runs {
		if r.TaskID == task.ID && activeStatus(r.Status) {
			matches = append(matches, r)
		}
	}
	if len(matches) != 1 {
		return TaskPacket{}, fmt.Errorf("expected exactly one active run for task, found %d", len(matches))
	}
	run := matches[0]
	if run.GatewayID != s.Config.GatewayID {
		return TaskPacket{}, fmt.Errorf("run assigned to gateway %s", run.GatewayID)
	}
	project, err := s.ProjectRead(ctx, task.ProjectID)
	if err != nil {
		return TaskPacket{}, err
	}
	plan, err := s.PlanRead(ctx, task.ProjectID)
	if err != nil {
		return TaskPacket{}, err
	}
	local, err := s.projectConfig(task.ProjectID)
	if err != nil {
		return TaskPacket{}, err
	}
	text := renderPacket(task, run, project, plan, local.Root)
	return TaskPacket{Task: task, Run: run, Project: project, Plan: plan, RepositoryRoot: local.Root, ResultPath: run.ResultPath, EvidencePath: run.EvidencePath, FinalizeCommand: "gpt-tunnel run finalize " + run.ID, Text: text}, nil
}
func renderPacket(task model.Task, run model.Run, project model.Project, plan model.Plan, root string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# GPT Tunnel Agent Execution Packet\n\nTask: %s\nRun: %s\nProject: %s\nRepository: %s\nBranch: %s\nBase: %s\n\n## Objective\n\n%s\n\n## Acceptance criteria\n", task.ID, run.ID, project.ID, root, task.Branch, task.BaseRevision, task.Objective)
	for _, v := range task.AcceptanceCriteria {
		fmt.Fprintf(&b, "- %s\n", v)
	}
	b.WriteString("\n## Constraints\n")
	for _, v := range task.Constraints {
		fmt.Fprintf(&b, "- %s\n", v)
	}
	b.WriteString("\n## Required gates\n")
	for _, v := range task.RequiredGates {
		fmt.Fprintf(&b, "- %s\n", v)
	}
	fmt.Fprintf(&b, "\n## Global plan context\n\n%s\n\n%s\n\n## Completion contract\n\nWrite agent result atomically to:\n  %s\n\nWrite evidence atomically to:\n  %s\n\nFinalize with:\n  gpt-tunnel run finalize %s\n\nThe task is not complete until finalization prints TASK_FINALIZED. Do not finish only in chat or Airelay.\n", plan.Summary, plan.Body, run.ResultPath, run.EvidencePath, run.ID)
	return b.String()
}

func (s *Service) RunFinalize(ctx context.Context, in FinalizeInput) (model.Report, OperationResult, error) {
	run, err := s.findRun(ctx, in.RunID)
	if err != nil {
		return model.Report{}, OperationResult{}, err
	}
	if !activeStatus(run.Status) {
		return model.Report{}, OperationResult{}, fmt.Errorf("run is not active: %s", run.Status)
	}
	task, err := s.findTask(ctx, run.TaskID)
	if err != nil {
		return model.Report{}, OperationResult{}, err
	}
	if in.ResultFile == "" {
		in.ResultFile = run.ResultPath
	}
	if in.EvidenceFile == "" {
		in.EvidenceFile = run.EvidencePath
	}
	var result model.AgentResult
	if err := fsutil.ReadJSONBounded(in.ResultFile, s.Config.MaxReadBytes, &result); err != nil {
		return model.Report{}, OperationResult{}, err
	}
	var evidence model.Evidence
	if err := fsutil.ReadJSONBounded(in.EvidenceFile, s.Config.MaxReadBytes, &evidence); err != nil {
		return model.Report{}, OperationResult{}, err
	}
	if err := model.ValidateAgentResult(result, task, run); err != nil {
		return model.Report{}, OperationResult{}, err
	}
	if err := model.ValidateEvidence(evidence, task, run); err != nil {
		return model.Report{}, OperationResult{}, err
	}
	local, err := s.projectConfig(run.ProjectID)
	if err != nil {
		return model.Report{}, OperationResult{}, err
	}
	head, branch, clean, err := s.Git.CurrentHead(ctx, local)
	if err != nil {
		return model.Report{}, OperationResult{}, err
	}
	if branch != run.Branch || head != evidence.ProjectHead || clean != evidence.WorktreeClean {
		return model.Report{}, OperationResult{}, fmt.Errorf("repository evidence does not match current state")
	}
	if result.Status == "succeeded" && !clean {
		return model.Report{}, OperationResult{}, fmt.Errorf("successful run must leave clean worktree")
	}
	if len(result.Commits) > 0 && result.Commits[len(result.Commits)-1] != head {
		return model.Report{}, OperationResult{}, fmt.Errorf("final project HEAD does not match last result commit")
	}
	ancestor, err := s.Git.IsAncestor(ctx, local.Root, run.BaseRevision, head)
	if err != nil {
		return model.Report{}, OperationResult{}, err
	}
	if !ancestor {
		return model.Report{}, OperationResult{}, fmt.Errorf("final project HEAD is not descended from task base")
	}
	now := time.Now().UTC()
	run.Status = result.Status
	run.FinishedAt = &now
	report := model.Report{SchemaVersion: model.SchemaVersion, TaskID: task.ID, RunID: run.ID, ProjectID: run.ProjectID, Status: result.Status, Summary: result.Summary, Commits: append([]string{}, result.Commits...), ChangedFiles: model.CanonicalStrings(result.ChangedFiles), Commands: result.Commands, Deviations: result.Deviations, RemainingRisks: result.RemainingRisks, FinishedAt: result.FinishedAt}
	expected := in.ExpectedHubRevision
	if expected == "" {
		expected, err = s.hubRevision(ctx)
		if err != nil {
			return model.Report{}, OperationResult{}, err
		}
	}
	plan, err := s.PlanRead(ctx, task.ProjectID)
	if err != nil {
		return model.Report{}, OperationResult{}, err
	}
	plan.Revision++
	plan.ActiveRunID = ""
	if result.Status == "succeeded" || result.Status == "cancelled" {
		plan.ActiveTaskID = ""
	}
	plan.UpdatedBy = s.Config.GatewayID
	plan.UpdatedAt = now
	tx, err := s.Hub.Transact(ctx, expected, "gateway: finalize run "+run.ID, func(w string) ([]string, error) {
		report.HubCommit = ""
		state := model.TaskState{SchemaVersion: model.SchemaVersion, TaskID: task.ID, TaskSHA256: task.SHA256, Status: taskStateStatusForResult(result.Status), UpdatedAt: now}
		paths := []string{s.runPath(run.ProjectID, run.ID), s.resultPath(run.ProjectID, run.ID), s.evidencePath(run.ProjectID, run.ID), s.reportPath(run.ProjectID, run.ID), s.taskStatePath(task.ProjectID, task.ID), s.planPath(task.ProjectID)}
		vals := []any{run, result, evidence, report, state, plan}
		for i, p := range paths {
			if err := hub.WriteJSON(w, p, vals[i]); err != nil {
				return nil, err
			}
		}
		return paths, nil
	})
	if err != nil {
		return model.Report{}, OperationResult{}, err
	}
	report.HubCommit = tx.After
	return report, OperationResult{Hub: tx, ProjectID: run.ProjectID, TaskID: run.TaskID, RunID: run.ID, Status: "TASK_FINALIZED"}, nil
}
func (s *Service) RunReport(ctx context.Context, id string) (model.Report, error) {
	run, err := s.findRun(ctx, id)
	if err != nil {
		return model.Report{}, err
	}
	var report model.Report
	err = s.Hub.ReadJSON(ctx, s.reportPath(run.ProjectID, id), &report)
	return report, err
}
func (s *Service) RunEvidence(ctx context.Context, id string) (model.Evidence, error) {
	run, err := s.findRun(ctx, id)
	if err != nil {
		return model.Evidence{}, err
	}
	var evidence model.Evidence
	err = s.Hub.ReadJSON(ctx, s.evidencePath(run.ProjectID, id), &evidence)
	return evidence, err
}
func (s *Service) RunCancel(ctx context.Context, id, expected string) (OperationResult, error) {
	run, err := s.findRun(ctx, id)
	if err != nil {
		return OperationResult{}, err
	}
	if !activeStatus(run.Status) {
		return OperationResult{}, fmt.Errorf("run is terminal")
	}
	run.Status = "cancel_requested"
	message := "Cancel task execution. Run: gpt-tunnel run read " + run.ID
	dispatch, dispatchErr := s.Airelay.Prompt(ctx, run.SessionKey, message)
	code := dispatch.ExitCode
	run.DispatchExitCode = &code
	run.DispatchStdout = dispatch.Stdout
	run.DispatchStderr = dispatch.Stderr
	tx, err := s.updateRun(ctx, run, expected, "gateway: request cancellation "+run.ID)
	if err != nil {
		return OperationResult{}, err
	}
	if dispatchErr != nil {
		return OperationResult{Hub: tx, ProjectID: run.ProjectID, TaskID: run.TaskID, RunID: run.ID, Status: run.Status}, dispatchErr
	}
	return OperationResult{Hub: tx, ProjectID: run.ProjectID, TaskID: run.TaskID, RunID: run.ID, Status: run.Status}, nil
}

func (s *Service) ReadResultRaw(ctx context.Context, id string) (map[string]any, error) {
	run, err := s.findRun(ctx, id)
	if err != nil {
		return nil, err
	}
	data, err := s.Hub.ReadFile(ctx, s.resultPath(run.ProjectID, id))
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := decodeStrict(data, &out); err != nil {
		return nil, err
	}
	return out, nil
}

type SweepItem struct {
	RunID  string `json:"run_id"`
	Action string `json:"action"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}
type SweepResult struct {
	Checked int         `json:"checked"`
	Items   []SweepItem `json:"items"`
}

func (s *Service) RunSweep(ctx context.Context) (SweepResult, error) {
	out := SweepResult{Items: []SweepItem{}}
	projects, err := s.ProjectList(ctx)
	if err != nil {
		return out, err
	}
	now := time.Now().UTC()
	for _, project := range projects {
		runs, err := s.RunList(ctx, project.ID)
		if err != nil {
			return out, err
		}
		for _, run := range runs {
			if !activeStatus(run.Status) {
				continue
			}
			out.Checked++
			start := run.CreatedAt
			if run.DispatchedAt != nil {
				start = *run.DispatchedAt
			}
			if run.LastRepromptAt != nil {
				start = *run.LastRepromptAt
			}
			if now.Sub(start) < time.Duration(s.Config.RunTimeoutSeconds)*time.Second {
				continue
			}
			task, e := s.findTask(ctx, run.TaskID)
			if e != nil {
				out.Items = append(out.Items, SweepItem{RunID: run.ID, Action: "error", Status: run.Status, Error: e.Error()})
				continue
			}
			expected, e := s.hubRevision(ctx)
			if e != nil {
				return out, e
			}
			if run.Status == "cancel_requested" {
				tx, e := s.failRun(ctx, run, task, "cancelled", "cooperative cancellation timed out", expected)
				_ = tx
				item := SweepItem{RunID: run.ID, Action: "finalize_cancelled", Status: "cancelled"}
				if e != nil {
					item.Error = e.Error()
				}
				out.Items = append(out.Items, item)
				continue
			}
			if run.RepromptCount < 1 {
				run.RepromptCount++
				t := now
				run.LastRepromptAt = &t
				_, e := s.updateRun(ctx, run, expected, "gateway: record reprompt "+run.ID)
				if e == nil {
					message := "Continue task. Run: gpt-tunnel task read " + task.ID
					_, e = s.Airelay.Prompt(ctx, run.SessionKey, message)
				}
				item := SweepItem{RunID: run.ID, Action: "reprompt", Status: run.Status}
				if e != nil {
					item.Error = e.Error()
				}
				out.Items = append(out.Items, item)
				continue
			}
			tx, e := s.failRun(ctx, run, task, "timed_out", "agent result was not finalized before timeout", expected)
			_ = tx
			item := SweepItem{RunID: run.ID, Action: "finalize_timeout", Status: "timed_out"}
			if e != nil {
				item.Error = e.Error()
			}
			out.Items = append(out.Items, item)
		}
	}
	return out, nil
}

func IsNotFound(err error) bool {
	return errors.Is(err, os.ErrNotExist) || strings.Contains(err.Error(), "does not exist") || strings.Contains(err.Error(), "not found")
}
