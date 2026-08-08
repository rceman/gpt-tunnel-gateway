package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
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
type ProjectIdentifiersAdoptInput struct {
	ProjectID   string `json:"project_id"`
	ProjectCode string `json:"project_code"`
	WriteOptions
}
type ProjectWorkflowPolicyInput struct {
	Policy model.ProjectWorkflowPolicy `json:"policy"`
	WriteOptions
}
type PlanUpdateInput struct {
	ProjectID        string    `json:"project_id"`
	Title            *string   `json:"title,omitempty"`
	Summary          *string   `json:"summary,omitempty"`
	CurrentObjective *string   `json:"current_objective,omitempty"`
	Queue            *[]string `json:"queue,omitempty"`
	ActiveTaskID     *string   `json:"active_task_id,omitempty"`
	ActiveRunID      *string   `json:"active_run_id,omitempty"`
	UpdatedBy        string    `json:"updated_by"`
	WriteOptions
}
type PlanSectionCreateInput struct {
	ProjectID        string `json:"project_id"`
	SectionID        string `json:"section_id"`
	Title            string `json:"title"`
	ShortDescription string `json:"short_description"`
	Description      string `json:"description"`
	UpdatedBy        string `json:"updated_by"`
	WriteOptions
}
type PlanSectionUpdateInput struct {
	ProjectID               string  `json:"project_id"`
	SectionID               string  `json:"section_id"`
	Title                   *string `json:"title,omitempty"`
	ShortDescription        *string `json:"short_description,omitempty"`
	Description             *string `json:"description,omitempty"`
	UpdatedBy               string  `json:"updated_by"`
	ExpectedSectionRevision int     `json:"expected_section_revision"`
	WriteOptions
}
type PlanSectionDeleteInput struct {
	ProjectID               string `json:"project_id"`
	SectionID               string `json:"section_id"`
	UpdatedBy               string `json:"updated_by"`
	ExpectedSectionRevision int    `json:"expected_section_revision"`
	WriteOptions
}
type ADRCreateInput struct {
	ADR model.ADR `json:"adr"`
	WriteOptions
}
type TaskCreateInput struct {
	ProjectID          string   `json:"project_id"`
	Slug               string   `json:"slug"`
	Title              string   `json:"title"`
	Objective          string   `json:"objective"`
	AcceptanceCriteria []string `json:"acceptance_criteria"`
	Constraints        []string `json:"constraints"`
	RequiredGates      []string `json:"required_gates,omitempty"`
	OperationClass     string   `json:"operation_class"`
	CreatedBy          string   `json:"created_by"`
	Supersedes         string   `json:"supersedes,omitempty"`
	WriteOptions
}
type DispatchInput struct {
	TaskID string `json:"task_id"`
	WriteOptions
}
type TaskMarkMergeReadyInput struct {
	TaskID string `json:"task_id"`
	WriteOptions
}
type TaskDeferInput struct {
	TaskID string `json:"task_id"`
	Reason string `json:"reason"`
	WriteOptions
}
type TaskMarkMergedInput struct {
	TaskID          string `json:"task_id"`
	IntegrationHead string `json:"integration_head"`
	WriteOptions
}
type FinalizeInput struct {
	RunID          string `json:"run_id"`
	CompletionFile string `json:"completion_file,omitempty"`
	WriteOptions
}

type CompletionWriteInput struct {
	RunID          string `json:"run_id"`
	CompletionFile string `json:"completion_file"`
}

type CompletionWriteResult struct {
	Status    string `json:"status"`
	Path      string `json:"path"`
	ProjectID string `json:"project_id"`
	TaskID    string `json:"task_id"`
	RunID     string `json:"run_id"`
}

type OperationResult struct {
	Hub       hub.TransactionResult `json:"hub"`
	ProjectID string                `json:"project_id,omitempty"`
	TaskID    string                `json:"task_id,omitempty"`
	RunID     string                `json:"run_id,omitempty"`
	Status    string                `json:"status"`
}

type ProjectStatus struct {
	Project        model.Project               `json:"project"`
	Local          config.ProjectConfig        `json:"local"`
	Worktree       gitx.WorktreeStatus         `json:"worktree"`
	Plan           model.PlanStatus            `json:"plan"`
	HubRevision    string                      `json:"hub_revision"`
	Progress       ProjectProgress             `json:"progress"`
	WorkflowPolicy ProjectWorkflowPolicyStatus `json:"workflow_policy"`
}

type ProjectWorkflowPolicyStatus struct {
	State                string                 `json:"state"`
	Revision             int                    `json:"revision"`
	WorkflowStage        string                 `json:"workflow_stage"`
	IntegrationBranch    string                 `json:"integration_branch"`
	AgentWaitForCI       bool                   `json:"agent_wait_for_ci"`
	CI                   model.WorkflowPolicyCI `json:"ci"`
	ActiveOperationClass string                 `json:"active_operation_class"`
	ActiveCIMode         string                 `json:"active_ci_mode"`
	CIBlocking           bool                   `json:"ci_blocking"`
	Conflicts            []string               `json:"conflicts"`
	CorrectiveAction     string                 `json:"corrective_action"`
}

type TaskRecord struct {
	Task            model.Task                   `json:"task"`
	State           model.TaskState              `json:"state"`
	CurrentRevision *model.TaskRevision          `json:"current_revision,omitempty"`
	RunSummaries    []model.RunReviewSummary     `json:"run_summaries"`
	WorkflowPolicy  *model.ProjectWorkflowPolicy `json:"workflow_policy,omitempty"`
}

type TaskPacket struct {
	Task            model.Task                  `json:"task"`
	CurrentRevision *model.TaskRevision         `json:"current_revision,omitempty"`
	Run             model.Run                   `json:"run"`
	RunSummaries    []model.RunReviewSummary    `json:"run_summaries"`
	Project         model.Project               `json:"project"`
	Plan            model.Plan                  `json:"plan"`
	WorkflowPolicy  model.ProjectWorkflowPolicy `json:"workflow_policy"`
	RepositoryRoot  string                      `json:"repository_root"`
	// CompletionPath is an internal diagnostic value only. The Agent packet
	// never instructs callers to use it; RunWriteCompletion derives the only
	// legal destination from StateDir and the canonical Run ID.
	CompletionPath  string `json:"-"`
	FinalizeCommand string `json:"finalize_command"`
	Text            string `json:"text"`
}

func (s *Service) projectPrefix(id string) string {
	if model.ValidateProjectIdentifier(id) != nil {
		return "../invalid-project-id"
	}
	return filepath.ToSlash(filepath.Join(hub.ProtocolRoot, "projects", id))
}
func (s *Service) projectPath(id string) string { return s.projectPrefix(id) + "/project.json" }
func (s *Service) planPath(id string) string    { return s.projectPrefix(id) + "/plan/current.json" }
func (s *Service) projectIdentifiersPath(id string) string {
	if model.ValidateProjectIdentifier(id) != nil {
		return "../invalid-project-identifiers"
	}
	return s.projectPrefix(id) + "/identifiers.json"
}
func (s *Service) planSectionPath(project, id string) string {
	if model.ValidateObjectIdentifier(id) != nil {
		return "../invalid-plan-section-id"
	}
	return s.projectPrefix(project) + "/plan/sections/" + id + ".json"
}
func (s *Service) adrPath(project, id string) string {
	if model.ValidateADRIdentifier(id) != nil && model.ValidateCanonicalADRIdentifier(id) != nil {
		return "../invalid-adr-id"
	}
	return s.projectPrefix(project) + "/adrs/" + id + ".json"
}
func (s *Service) taskPath(project, id string) string {
	if model.ValidateObjectIdentifier(id) != nil {
		return "../invalid-task-id"
	}
	return s.projectPrefix(project) + "/tasks/" + id + ".json"
}
func (s *Service) taskStatePath(project, id string) string {
	if model.ValidateObjectIdentifier(id) != nil {
		return "../invalid-task-id"
	}
	return s.projectPrefix(project) + "/tasks/" + id + ".state.json"
}
func (s *Service) taskRunCounterPath(project, id string) string {
	if model.ValidateProjectIdentifier(project) != nil || model.ValidateCanonicalTaskID(id) != nil {
		return "../invalid-task-run-counter"
	}
	return s.projectPrefix(project) + "/tasks/" + id + ".run-counter.json"
}
func (s *Service) runPrefix(project, id string) string {
	if model.ValidateObjectIdentifier(id) != nil {
		return "../invalid-run-id"
	}
	return s.projectPrefix(project) + "/runs/" + id
}
func (s *Service) runPath(project, id string) string { return s.runPrefix(project, id) + "/run.json" }
func (s *Service) reportPath(project, id string) string {
	return s.runPrefix(project, id) + "/report.json"
}

func decodeStrict(data []byte, out any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return fmt.Errorf("trailing JSON content")
	}
	return nil
}
func readWorktreeJSON(worktree, path string, out any) error {
	data, err := os.ReadFile(filepath.Join(worktree, filepath.FromSlash(path)))
	if err != nil {
		return err
	}
	return decodeStrict(data, out)
}
func ensureSessionAvailableInWorktree(worktree, session string, maxReadBytes int64) error {
	root := filepath.Join(worktree, filepath.FromSlash(hub.ProtocolRoot), "projects")
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			if os.IsNotExist(walkErr) {
				return nil
			}
			return walkErr
		}
		if entry.IsDir() || entry.Name() != "run.json" {
			return nil
		}
		data, err := fsutil.ReadFileBounded(path, maxReadBytes)
		if err != nil {
			return err
		}
		run, _, err := model.DecodeRunRecord(data)
		if err != nil {
			return fmt.Errorf("decode active run %s: %w", path, err)
		}
		if run.SessionKey == session && operationalActiveRun(run) {
			return fmt.Errorf("active operational run %s already owns the project session", run.ID)
		}
		return nil
	})
}
func (s *Service) projectConfig(id string) (config.ProjectConfig, error) {
	return s.EffectiveProjectConfig(id)
}
func (s *Service) hubRevision(ctx context.Context) (string, error) { return s.Hub.RemoteRevision(ctx) }

func (s *Service) ProjectList(ctx context.Context) ([]model.Project, error) {
	paths, err := s.Hub.List(ctx, hub.ProtocolRoot+"/projects", "/project.json")
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

// ValidateConfiguredProjectRecords prevents a fresh deployment from reporting
// configured projects while the canonical hub has no durable project records.
func (s *Service) ValidateConfiguredProjectRecords(ctx context.Context) error {
	ids, _, err := s.effectiveProjectIDs()
	if err != nil {
		return fmt.Errorf("validate configured projects: %w", err)
	}
	items, err := s.ProjectList(ctx)
	if err != nil {
		return fmt.Errorf("validate durable project records: %w", err)
	}
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		seen[item.ID] = true
	}
	missing := []string{}
	for _, id := range ids {
		if !seen[id] {
			missing = append(missing, id)
			continue
		}
		var plan model.Plan
		if err := s.Hub.ReadJSON(ctx, s.planPath(id), &plan); err != nil {
			return fmt.Errorf("durable hub plan missing or invalid for project %q: %w", id, err)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("durable hub project records missing: %s", strings.Join(missing, ", "))
	}
	return nil
}
func (s *Service) ProjectRead(ctx context.Context, id string) (model.Project, error) {
	var p model.Project
	err := s.Hub.ReadJSON(ctx, s.projectPath(id), &p)
	return p, err
}
func (s *Service) ProjectIdentifiersRead(ctx context.Context, projectID string) (model.ProjectIdentifiers, error) {
	if err := model.ValidateProjectIdentifier(projectID); err != nil {
		return model.ProjectIdentifiers{}, err
	}
	var identifiers model.ProjectIdentifiers
	if err := s.Hub.ReadJSON(ctx, s.projectIdentifiersPath(projectID), &identifiers); err != nil {
		return model.ProjectIdentifiers{}, err
	}
	if err := model.ValidateProjectIdentifiers(identifiers); err != nil {
		return model.ProjectIdentifiers{}, err
	}
	if identifiers.ProjectID != projectID {
		return model.ProjectIdentifiers{}, fmt.Errorf("project identifiers project_id mismatch")
	}
	return identifiers, nil
}
func (s *Service) ProjectIdentifiersAdopt(ctx context.Context, in ProjectIdentifiersAdoptInput) (model.ProjectIdentifiers, OperationResult, error) {
	if err := model.ValidateProjectIdentifier(in.ProjectID); err != nil {
		return model.ProjectIdentifiers{}, OperationResult{}, err
	}
	if err := model.ValidateProjectCode(in.ProjectCode); err != nil {
		return model.ProjectIdentifiers{}, OperationResult{}, err
	}
	if _, err := s.ProjectRead(ctx, in.ProjectID); err != nil {
		return model.ProjectIdentifiers{}, OperationResult{}, err
	}
	identifiers := model.ProjectIdentifiers{SchemaVersion: model.SchemaVersion, ProjectID: in.ProjectID, ProjectCode: in.ProjectCode, NextTaskNumber: 1, NextADRNumber: 1}
	if err := model.ValidateProjectIdentifiers(identifiers); err != nil {
		return model.ProjectIdentifiers{}, OperationResult{}, err
	}
	tx, err := s.Hub.Transact(ctx, in.ExpectedHubRevision, "gateway: adopt project identifiers "+in.ProjectID, func(worktree string) ([]string, error) {
		var project model.Project
		if err := readWorktreeJSON(worktree, s.projectPath(in.ProjectID), &project); err != nil {
			return nil, fmt.Errorf("project %q is not durable: %w", in.ProjectID, err)
		}
		if err := model.ValidateProject(project); err != nil {
			return nil, fmt.Errorf("project %q is invalid: %w", in.ProjectID, err)
		}
		if project.ID != in.ProjectID {
			return nil, fmt.Errorf("project %q has mismatched durable ID", in.ProjectID)
		}
		identifiersPath := s.projectIdentifiersPath(in.ProjectID)
		if _, err := os.Lstat(filepath.Join(worktree, filepath.FromSlash(identifiersPath))); err == nil {
			return nil, fmt.Errorf("project identifiers already exist for %q", in.ProjectID)
		} else if !os.IsNotExist(err) {
			return nil, err
		}
		projectsRoot := filepath.Join(worktree, filepath.FromSlash(hub.ProtocolRoot), "projects")
		entries, err := os.ReadDir(projectsRoot)
		if err != nil {
			return nil, fmt.Errorf("list durable projects: %w", err)
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			projectID := entry.Name()
			if err := model.ValidateProjectIdentifier(projectID); err != nil {
				return nil, fmt.Errorf("invalid durable project directory %q: %w", projectID, err)
			}
			path := s.projectIdentifiersPath(projectID)
			if _, err := os.Lstat(filepath.Join(worktree, filepath.FromSlash(path))); err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return nil, err
			}
			var existing model.ProjectIdentifiers
			if err := readWorktreeJSON(worktree, path, &existing); err != nil {
				return nil, fmt.Errorf("read project identifiers for %q: %w", projectID, err)
			}
			if err := model.ValidateProjectIdentifiers(existing); err != nil {
				return nil, fmt.Errorf("invalid project identifiers for %q: %w", projectID, err)
			}
			if existing.ProjectID != projectID {
				return nil, fmt.Errorf("project identifiers for %q have mismatched project_id", projectID)
			}
			if existing.ProjectCode == in.ProjectCode {
				return nil, fmt.Errorf("project code %q is already adopted by %q", in.ProjectCode, projectID)
			}
		}
		if err := hub.WriteJSON(worktree, identifiersPath, identifiers); err != nil {
			return nil, err
		}
		return []string{identifiersPath}, nil
	})
	if err != nil {
		return model.ProjectIdentifiers{}, OperationResult{}, err
	}
	return identifiers, OperationResult{Hub: tx, ProjectID: in.ProjectID, Status: "adopted"}, nil
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
	plan := model.Plan{
		SchemaVersion:    model.PlanSchemaVersion,
		ProjectID:        p.ID,
		Revision:         1,
		Title:            "Registered active project",
		Summary:          "Registered active project with no current authorized work",
		CurrentObjective: "The " + p.ID + " repository is registered with the gateway and is available for future durable tasks. No task or run is currently active.\n\nNext action: Await an explicitly authorized durable task before implementation, release, runtime mutation or repository changes.",
		Queue:            []string{},
		Sections:         []model.PlanSectionIndex{},
		UpdatedBy:        in.Project.ID,
		UpdatedAt:        now,
	}
	if err := model.ValidatePlan(plan); err != nil {
		return OperationResult{}, err
	}
	tx, err := s.Hub.Transact(ctx, in.ExpectedHubRevision, "gateway: register project "+p.ID, func(w string) ([]string, error) {
		projectPath := s.projectPath(p.ID)
		if _, err := os.Stat(filepath.Join(w, filepath.FromSlash(projectPath))); err == nil {
			return nil, fmt.Errorf("project already exists")
		}
		planPath := s.planPath(p.ID)
		if _, err := os.Stat(filepath.Join(w, filepath.FromSlash(planPath))); err == nil {
			return nil, fmt.Errorf("project plan already exists")
		}
		if err := hub.WriteJSON(w, projectPath, p); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(w, planPath, plan); err != nil {
			return nil, err
		}
		return []string{projectPath, planPath}, nil
	})
	if err != nil {
		return OperationResult{}, err
	}
	return OperationResult{Hub: tx, ProjectID: p.ID, Status: "registered"}, nil
}
func (s *Service) ProjectStatus(ctx context.Context, id string) (ProjectStatus, error) {
	local, err := s.projectConfig(id)
	if err != nil {
		return ProjectStatus{}, err
	}
	componentCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	var (
		p                 = model.Project{SchemaVersion: model.SchemaVersion, ID: id, DefaultBranch: local.DefaultBranch, Status: "unknown"}
		projectErr        error
		wt                gitx.WorktreeStatus
		wtErr             error
		plan              model.Plan
		planErr           error
		workflowPolicy    model.ProjectWorkflowPolicy
		workflowPolicyErr error
		tasks             []TaskRecord
		tasksErr          error
		runs              []model.Run
		runsErr           error
		hubRevision       string
		hubRevisionErr    error
		agentStatus       airelay.SessionStatus
		agentStatusErr    error
		agentTail         airelay.Result
		agentTailErr      error
	)
	var wg sync.WaitGroup
	wg.Add(9)
	go func() {
		defer wg.Done()
		candidate, err := s.ProjectRead(componentCtx, id)
		if err != nil {
			projectErr = err
			return
		}
		p = candidate
	}()
	go func() {
		defer wg.Done()
		wt, wtErr = s.Git.WorktreeStatus(componentCtx, local)
	}()
	go func() {
		defer wg.Done()
		plan, planErr = s.PlanRead(componentCtx, id)
	}()
	go func() {
		defer wg.Done()
		tasks, tasksErr = s.taskStatusList(componentCtx, id)
	}()
	go func() {
		defer wg.Done()
		runs, runsErr = s.RunList(componentCtx, id)
	}()
	go func() {
		defer wg.Done()
		hubRevision, hubRevisionErr = s.hubRevision(componentCtx)
	}()
	go func() {
		defer wg.Done()
		agentStatus, agentStatusErr = s.Airelay.Status(componentCtx, local.AirelaySessionKey)
	}()
	go func() {
		defer wg.Done()
		agentTail, agentTailErr = s.Airelay.Tail(componentCtx, local.AirelaySessionKey, progressTailLines)
	}()
	go func() {
		defer wg.Done()
		workflowPolicy, workflowPolicyErr = s.ProjectWorkflowPolicyRead(componentCtx, id)
	}()
	wg.Wait()
	progress := s.projectProgressFromInputs(plan, planErr, tasks, tasksErr, runs, runsErr, agentStatus, agentStatusErr, agentTail, agentTailErr)
	appendComponentError(&progress.ComponentErrors, "project", projectErr)
	appendComponentError(&progress.ComponentErrors, "worktree", wtErr)
	appendComponentError(&progress.ComponentErrors, "hub_revision", hubRevisionErr)
	appendComponentError(&progress.ComponentErrors, "workflow_policy", workflowPolicyErr)
	internalPaths := []string{s.Config.StateDir, local.Root, local.Mirror, local.AirelaySessionKey}
	for _, run := range runs {
		internalPaths = append(internalPaths, run.CompletionPath)
	}
	for _, internal := range internalPaths {
		if internal != "" {
			progress.Tail = strings.ReplaceAll(progress.Tail, internal, "[gateway-internal-value]")
		}
	}
	sort.Strings(progress.ComponentErrors)
	return ProjectStatus{Project: p, Local: local, Worktree: wt, Plan: plan.StatusView(), HubRevision: hubRevision, Progress: progress, WorkflowPolicy: workflowPolicyStatus(workflowPolicy, workflowPolicyErr, plan, tasks)}, nil
}

func (s *Service) PlanRead(ctx context.Context, project string) (model.Plan, error) {
	var p model.Plan
	if err := s.Hub.ReadJSON(ctx, s.planPath(project), &p); err != nil {
		return model.Plan{}, err
	}
	if err := model.ValidatePlan(p); err != nil {
		return model.Plan{}, err
	}
	return p, nil
}

func (s *Service) PlanUpdate(ctx context.Context, in PlanUpdateInput) (OperationResult, error) {
	if _, err := s.ProjectRead(ctx, in.ProjectID); err != nil {
		return OperationResult{}, err
	}
	if in.ActiveTaskID != nil && *in.ActiveTaskID != "" {
		if err := requireCanonicalTaskID(*in.ActiveTaskID); err != nil {
			return OperationResult{}, err
		}
	}
	if in.ActiveRunID != nil && *in.ActiveRunID != "" {
		if err := requireCanonicalRunID(*in.ActiveRunID); err != nil {
			return OperationResult{}, err
		}
	}
	old, err := s.PlanRead(ctx, in.ProjectID)
	if err != nil && !IsNotFound(err) {
		return OperationResult{}, err
	}
	creating := err != nil
	if creating && (in.Title == nil || in.Summary == nil) {
		return OperationResult{}, fmt.Errorf("new plan requires title and summary")
	}
	if creating {
		old = model.Plan{SchemaVersion: model.PlanSchemaVersion, ProjectID: in.ProjectID, Revision: 0, Queue: []string{}, Sections: []model.PlanSectionIndex{}}
	}
	plan := old
	plan.SchemaVersion = model.PlanSchemaVersion
	plan.ProjectID = in.ProjectID
	plan.Revision++
	if in.Title != nil {
		plan.Title = *in.Title
	}
	if in.Summary != nil {
		plan.Summary = *in.Summary
	}
	if in.CurrentObjective != nil {
		plan.CurrentObjective = *in.CurrentObjective
	}
	if in.Queue != nil {
		plan.Queue = append([]string{}, (*in.Queue)...)
	}
	if in.ActiveTaskID != nil {
		plan.ActiveTaskID = *in.ActiveTaskID
	}
	if in.ActiveRunID != nil {
		plan.ActiveRunID = *in.ActiveRunID
	}
	plan.UpdatedBy = in.UpdatedBy
	plan.UpdatedAt = time.Now().UTC()
	if plan.Queue == nil {
		plan.Queue = []string{}
	}
	if plan.Sections == nil {
		plan.Sections = []model.PlanSectionIndex{}
	}
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

func sectionIndex(plan model.Plan, id string) (int, model.PlanSectionIndex, error) {
	for i, section := range plan.Sections {
		if section.ID == id {
			return i, section, nil
		}
	}
	return -1, model.PlanSectionIndex{}, fmt.Errorf("plan section not found: %s", id)
}

func (s *Service) PlanSectionRead(ctx context.Context, project, id string) (model.PlanSection, error) {
	plan, err := s.PlanRead(ctx, project)
	if err != nil {
		return model.PlanSection{}, err
	}
	if _, _, err := sectionIndex(plan, id); err != nil {
		return model.PlanSection{}, err
	}
	var section model.PlanSection
	if err := s.Hub.ReadJSON(ctx, s.planSectionPath(project, id), &section); err != nil {
		return model.PlanSection{}, err
	}
	if err := model.ValidatePlanSection(section); err != nil {
		return model.PlanSection{}, err
	}
	return section, nil
}

func (s *Service) sectionWriteExpectedRevision(ctx context.Context, supplied string) (string, error) {
	if supplied == "" {
		return "", nil
	}
	current, err := s.hubRevision(ctx)
	if err != nil {
		return "", err
	}
	if supplied == current {
		return supplied, nil
	}
	// A stale global revision is intentionally discarded. The transaction
	// below reads the latest manifest and protects only the target section.
	return "", nil
}

func (s *Service) transactSectionWrite(ctx context.Context, expected, subject string, mutate hub.Mutator) (hub.TransactionResult, error) {
	for attempt := 0; attempt < 3; attempt++ {
		tx, err := s.Hub.Transact(ctx, expected, subject, mutate)
		if err == nil {
			return tx, nil
		}
		if !strings.Contains(err.Error(), "HUB_REVISION_CONFLICT") {
			return hub.TransactionResult{}, err
		}
		expected = ""
	}
	return hub.TransactionResult{}, fmt.Errorf("section transaction retry limit exceeded")
}

func (s *Service) PlanSectionCreate(ctx context.Context, in PlanSectionCreateInput) (OperationResult, error) {
	plan, err := s.PlanRead(ctx, in.ProjectID)
	if err != nil {
		return OperationResult{}, err
	}
	if _, _, err := sectionIndex(plan, in.SectionID); err == nil {
		return OperationResult{}, fmt.Errorf("plan section already exists: %s", in.SectionID)
	}
	now := time.Now().UTC()
	section := model.PlanSection{SchemaVersion: model.PlanSchemaVersion, ProjectID: in.ProjectID, ID: in.SectionID, Revision: 1, Title: in.Title, ShortDescription: in.ShortDescription, Description: in.Description, UpdatedBy: in.UpdatedBy, UpdatedAt: now}
	if err := model.ValidatePlanSection(section); err != nil {
		return OperationResult{}, err
	}
	plan.Revision++
	plan.Sections = append(append([]model.PlanSectionIndex{}, plan.Sections...), model.PlanSectionIndex{ID: section.ID, Title: section.Title, ShortDescription: section.ShortDescription, Revision: section.Revision})
	plan.UpdatedBy, plan.UpdatedAt = in.UpdatedBy, now
	if in.ExpectedHubRevision == "" {
		in.ExpectedHubRevision, err = s.hubRevision(ctx)
		if err != nil {
			return OperationResult{}, err
		}
	}
	tx, err := s.Hub.Transact(ctx, in.ExpectedHubRevision, "gateway: create plan section "+in.SectionID, func(w string) ([]string, error) {
		var current model.Plan
		if err := readWorktreeJSON(w, s.planPath(in.ProjectID), &current); err != nil {
			return nil, err
		}
		if _, _, err := sectionIndex(current, in.SectionID); err == nil {
			return nil, fmt.Errorf("plan section already exists: %s", in.SectionID)
		}
		current.Revision++
		current.Sections = append(current.Sections, model.PlanSectionIndex{ID: section.ID, Title: section.Title, ShortDescription: section.ShortDescription, Revision: section.Revision})
		current.UpdatedBy, current.UpdatedAt = in.UpdatedBy, now
		if err := model.ValidatePlan(current); err != nil {
			return nil, err
		}
		sectionPath := s.planSectionPath(in.ProjectID, in.SectionID)
		if err := hub.WriteJSON(w, sectionPath, section); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(w, s.planPath(in.ProjectID), current); err != nil {
			return nil, err
		}
		return []string{sectionPath, s.planPath(in.ProjectID)}, nil
	})
	if err != nil {
		return OperationResult{}, err
	}
	return OperationResult{Hub: tx, ProjectID: in.ProjectID, Status: "created"}, nil
}

func (s *Service) PlanSectionUpdate(ctx context.Context, in PlanSectionUpdateInput) (OperationResult, error) {
	if in.ExpectedSectionRevision < 1 {
		return OperationResult{}, fmt.Errorf("expected section revision is required")
	}
	if _, err := s.PlanSectionRead(ctx, in.ProjectID, in.SectionID); err != nil {
		return OperationResult{}, err
	}
	expectedHubRevision, err := s.sectionWriteExpectedRevision(ctx, in.ExpectedHubRevision)
	if err != nil {
		return OperationResult{}, err
	}
	now := time.Now().UTC()
	tx, err := s.transactSectionWrite(ctx, expectedHubRevision, "gateway: update plan section "+in.SectionID, func(w string) ([]string, error) {
		var currentPlan model.Plan
		if err := readWorktreeJSON(w, s.planPath(in.ProjectID), &currentPlan); err != nil {
			return nil, err
		}
		index, indexEntry, err := sectionIndex(currentPlan, in.SectionID)
		if err != nil {
			return nil, err
		}
		var section model.PlanSection
		sectionPath := s.planSectionPath(in.ProjectID, in.SectionID)
		if err := readWorktreeJSON(w, sectionPath, &section); err != nil {
			return nil, err
		}
		if section.Revision != in.ExpectedSectionRevision || indexEntry.Revision != in.ExpectedSectionRevision {
			return nil, fmt.Errorf("SECTION_REVISION_CONFLICT expected=%d actual=%d", in.ExpectedSectionRevision, section.Revision)
		}
		if in.Title != nil {
			section.Title = *in.Title
		}
		if in.ShortDescription != nil {
			section.ShortDescription = *in.ShortDescription
		}
		if in.Description != nil {
			section.Description = *in.Description
		}
		section.Revision++
		section.UpdatedBy, section.UpdatedAt = in.UpdatedBy, now
		if err := model.ValidatePlanSection(section); err != nil {
			return nil, err
		}
		currentPlan.Revision++
		currentPlan.Sections[index] = model.PlanSectionIndex{ID: section.ID, Title: section.Title, ShortDescription: section.ShortDescription, Revision: section.Revision}
		currentPlan.UpdatedBy, currentPlan.UpdatedAt = in.UpdatedBy, now
		if err := model.ValidatePlan(currentPlan); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(w, sectionPath, section); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(w, s.planPath(in.ProjectID), currentPlan); err != nil {
			return nil, err
		}
		return []string{sectionPath, s.planPath(in.ProjectID)}, nil
	})
	if err != nil {
		return OperationResult{}, err
	}
	return OperationResult{Hub: tx, ProjectID: in.ProjectID, Status: "updated"}, nil
}

func (s *Service) PlanSectionDelete(ctx context.Context, in PlanSectionDeleteInput) (OperationResult, error) {
	if in.ExpectedSectionRevision < 1 {
		return OperationResult{}, fmt.Errorf("expected section revision is required")
	}
	if _, err := s.PlanSectionRead(ctx, in.ProjectID, in.SectionID); err != nil {
		return OperationResult{}, err
	}
	expectedHubRevision, err := s.sectionWriteExpectedRevision(ctx, in.ExpectedHubRevision)
	if err != nil {
		return OperationResult{}, err
	}
	now := time.Now().UTC()
	tx, err := s.transactSectionWrite(ctx, expectedHubRevision, "gateway: delete plan section "+in.SectionID, func(w string) ([]string, error) {
		var currentPlan model.Plan
		if err := readWorktreeJSON(w, s.planPath(in.ProjectID), &currentPlan); err != nil {
			return nil, err
		}
		index, section, err := sectionIndex(currentPlan, in.SectionID)
		if err != nil {
			return nil, err
		}
		var currentSection model.PlanSection
		sectionPath := s.planSectionPath(in.ProjectID, in.SectionID)
		if err := readWorktreeJSON(w, sectionPath, &currentSection); err != nil {
			return nil, err
		}
		if currentSection.Revision != in.ExpectedSectionRevision || section.Revision != in.ExpectedSectionRevision {
			return nil, fmt.Errorf("SECTION_REVISION_CONFLICT expected=%d actual=%d", in.ExpectedSectionRevision, currentSection.Revision)
		}
		if err := os.Remove(filepath.Join(w, filepath.FromSlash(sectionPath))); err != nil {
			return nil, err
		}
		currentPlan.Sections = append(currentPlan.Sections[:index], currentPlan.Sections[index+1:]...)
		currentPlan.Revision++
		currentPlan.UpdatedBy, currentPlan.UpdatedAt = in.UpdatedBy, now
		if err := model.ValidatePlan(currentPlan); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(w, s.planPath(in.ProjectID), currentPlan); err != nil {
			return nil, err
		}
		return []string{sectionPath, s.planPath(in.ProjectID)}, nil
	})
	if err != nil {
		return OperationResult{}, err
	}
	return OperationResult{Hub: tx, ProjectID: in.ProjectID, Status: "deleted"}, nil
}

func (s *Service) PlanRender(ctx context.Context, project string) (model.PlanRender, error) {
	plan, err := s.PlanRead(ctx, project)
	if err != nil {
		return model.PlanRender{}, err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n%s\n\n", plan.Title, plan.Summary)
	if plan.CurrentObjective != "" {
		fmt.Fprintf(&b, "Current objective: %s\n\n", plan.CurrentObjective)
	}
	for _, index := range plan.Sections {
		section, err := s.PlanSectionRead(ctx, project, index.ID)
		if err != nil {
			return model.PlanRender{}, err
		}
		fmt.Fprintf(&b, "## %s\n\n%s\n\n%s\n\n", section.Title, section.ShortDescription, section.Description)
	}
	text := b.String()
	if s.Config.MaxReadBytes > 0 && int64(len(text)) > s.Config.MaxReadBytes {
		return model.PlanRender{}, fmt.Errorf("plan render exceeds configured output limit")
	}
	return model.PlanRender{SchemaVersion: model.PlanSchemaVersion, ProjectID: plan.ProjectID, Revision: plan.Revision, Title: plan.Title, Summary: plan.Summary, CurrentObjective: plan.CurrentObjective, Text: text}, nil
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
	if err := model.ValidateADRIdentifier(id); err != nil {
		return model.ADR{}, err
	}
	var v model.ADR
	err := s.Hub.ReadJSON(ctx, s.adrPath(project, id), &v)
	return v, err
}
func allocatorConflict(err error) bool {
	if err == nil {
		return false
	}
	message := err.Error()
	return strings.Contains(message, "project identifiers changed before") ||
		strings.Contains(message, "already exists") ||
		strings.Contains(message, "HUB_REVISION_CONFLICT")
}

// allocatorRetryLimit bounds optimistic allocator retries for every canonical
// ID family, including operator journal events and corrections.
const allocatorRetryLimit = 20

func (s *Service) ADRCreate(ctx context.Context, in ADRCreateInput) (OperationResult, error) {
	for attempt := 0; ; attempt++ {
		result, err := s.adrCreateOnce(ctx, in)
		if in.ExpectedHubRevision != "" || err == nil || !allocatorConflict(err) || attempt+1 >= allocatorRetryLimit {
			return result, err
		}
	}
}

func (s *Service) adrCreateOnce(ctx context.Context, in ADRCreateInput) (OperationResult, error) {
	v := in.ADR
	v.SchemaVersion = model.SchemaVersion
	if v.ID != "" {
		return OperationResult{}, fmt.Errorf("ADR id is allocated by the gateway")
	}
	v.CreatedAt = time.Now().UTC()
	if v.Status == "" {
		v.Status = "accepted"
	}
	if _, err := s.ProjectRead(ctx, v.ProjectID); err != nil {
		return OperationResult{}, err
	}
	identifiers, err := s.ProjectIdentifiersRead(ctx, v.ProjectID)
	if err != nil {
		return OperationResult{}, fmt.Errorf("read project identifiers: %w", err)
	}
	v.ID, err = model.FormatADRID(identifiers.ProjectCode, identifiers.NextADRNumber)
	if err != nil {
		return OperationResult{}, err
	}
	if identifiers.NextADRNumber == model.MaxSafeInteger {
		if _, readErr := s.Hub.ReadFile(ctx, s.adrPath(v.ProjectID, v.ID)); readErr == nil {
			return OperationResult{}, fmt.Errorf("ADR allocator exhausted for project %q", v.ProjectID)
		} else if !IsNotFound(readErr) {
			return OperationResult{}, readErr
		}
	}
	if err := model.ValidateADR(v); err != nil {
		return OperationResult{}, err
	}
	nextADR := identifiers.NextADRNumber
	if nextADR < model.MaxSafeInteger {
		nextADR++
	}
	updatedIdentifiers := identifiers
	updatedIdentifiers.NextADRNumber = nextADR
	tx, err := s.Hub.Transact(ctx, in.ExpectedHubRevision, "gateway: create ADR "+v.ID, func(w string) ([]string, error) {
		var current model.ProjectIdentifiers
		if err := readWorktreeJSON(w, s.projectIdentifiersPath(v.ProjectID), &current); err != nil {
			return nil, err
		}
		if current.ProjectCode != identifiers.ProjectCode || current.NextADRNumber != identifiers.NextADRNumber {
			return nil, fmt.Errorf("project identifiers changed before ADR allocation")
		}
		path := s.adrPath(v.ProjectID, v.ID)
		if _, err := os.Lstat(filepath.Join(w, filepath.FromSlash(path))); err == nil {
			return nil, fmt.Errorf("ADR already exists")
		} else if !os.IsNotExist(err) {
			return nil, err
		}
		if err := hub.WriteJSON(w, path, v); err != nil {
			return nil, err
		}
		identifiersPath := s.projectIdentifiersPath(v.ProjectID)
		if err := hub.WriteJSON(w, identifiersPath, updatedIdentifiers); err != nil {
			return nil, err
		}
		return []string{path, identifiersPath}, nil
	})
	if err != nil {
		return OperationResult{}, err
	}
	return OperationResult{Hub: tx, ProjectID: v.ProjectID, Status: "created"}, nil
}

func (s *Service) TaskCreate(ctx context.Context, in TaskCreateInput) (model.Task, OperationResult, error) {
	for attempt := 0; ; attempt++ {
		task, result, err := s.taskCreateOnce(ctx, in)
		if in.ExpectedHubRevision != "" || err == nil || !allocatorConflict(err) || attempt+1 >= allocatorRetryLimit {
			return task, result, err
		}
	}
}

func (s *Service) taskCreateOnce(ctx context.Context, in TaskCreateInput) (model.Task, OperationResult, error) {
	if in.Slug == "" {
		return model.Task{}, OperationResult{}, fmt.Errorf("slug is required")
	}
	_, effectivePolicy, err := s.deriveTaskWorkflowPolicy(ctx, in.ProjectID, in.OperationClass, in.RequiredGates)
	if err != nil {
		return model.Task{}, OperationResult{}, err
	}
	identifiers, err := s.ProjectIdentifiersRead(ctx, in.ProjectID)
	if err != nil {
		return model.Task{}, OperationResult{}, fmt.Errorf("read project identifiers: %w", err)
	}
	var id, branch string
	if err := model.ValidateTaskSlug(in.Slug); err != nil {
		return model.Task{}, OperationResult{}, err
	}
	id, err = model.FormatTaskID(identifiers.ProjectCode, identifiers.NextTaskNumber)
	if err != nil {
		return model.Task{}, OperationResult{}, err
	}
	if identifiers.NextTaskNumber == model.MaxSafeInteger {
		if _, readErr := s.Hub.ReadFile(ctx, s.taskPath(in.ProjectID, id)); readErr == nil {
			return model.Task{}, OperationResult{}, fmt.Errorf("task allocator exhausted for project %q", in.ProjectID)
		} else if !IsNotFound(readErr) {
			return model.Task{}, OperationResult{}, readErr
		}
	}
	branch = "task/" + id + "-" + in.Slug
	task := model.Task{SchemaVersion: model.SchemaVersion, ID: id, ProjectID: in.ProjectID, Title: in.Title, Objective: in.Objective, Branch: branch, AcceptanceCriteria: append([]string{}, in.AcceptanceCriteria...), Constraints: append([]string{}, in.Constraints...), RequiredGates: append([]string{}, in.RequiredGates...), WorkflowPolicyRevision: effectivePolicy.WorkflowPolicyRevision, OperationClass: effectivePolicy.OperationClass, EffectiveCIField: effectivePolicy.EffectiveCIField, EffectiveCIMode: effectivePolicy.EffectiveCIMode, WaitForCI: effectivePolicy.WaitForCI, CIBlocking: effectivePolicy.CIBlocking, AgentMayWait: effectivePolicy.AgentMayWait, Status: "created", Supersedes: in.Supersedes, CreatedBy: in.CreatedBy, CreatedAt: time.Now().UTC()}
	hash, err := model.HashTask(task)
	if err != nil {
		return model.Task{}, OperationResult{}, err
	}
	task.SHA256 = hash
	if err := model.ValidateTask(task); err != nil {
		return model.Task{}, OperationResult{}, err
	}
	state := model.TaskState{SchemaVersion: model.SchemaVersion, TaskID: task.ID, TaskSHA256: task.SHA256, Status: "created", UpdatedAt: time.Now().UTC()}
	updatedIdentifiers := identifiers
	if updatedIdentifiers.NextTaskNumber < model.MaxSafeInteger {
		updatedIdentifiers.NextTaskNumber++
	}
	tx, err := s.Hub.Transact(ctx, in.ExpectedHubRevision, "gateway: create task "+task.ID, func(w string) ([]string, error) {
		path := s.taskPath(task.ProjectID, task.ID)
		statePath := s.taskStatePath(task.ProjectID, task.ID)
		if _, err := os.Lstat(filepath.Join(w, filepath.FromSlash(path))); err == nil {
			return nil, fmt.Errorf("task already exists")
		} else if !os.IsNotExist(err) {
			return nil, err
		}
		if _, err := os.Lstat(filepath.Join(w, filepath.FromSlash(statePath))); err == nil {
			return nil, fmt.Errorf("task state already exists")
		} else if !os.IsNotExist(err) {
			return nil, err
		}
		paths := []string{path, statePath}
		var current model.ProjectIdentifiers
		if err := readWorktreeJSON(w, s.projectIdentifiersPath(task.ProjectID), &current); err != nil {
			return nil, err
		}
		if current.ProjectCode != identifiers.ProjectCode || current.NextTaskNumber != identifiers.NextTaskNumber {
			return nil, fmt.Errorf("project identifiers changed before task allocation")
		}
		counterPath := s.taskRunCounterPath(task.ProjectID, task.ID)
		if _, err := os.Lstat(filepath.Join(w, filepath.FromSlash(counterPath))); err == nil {
			return nil, fmt.Errorf("task run counter already exists")
		} else if !os.IsNotExist(err) {
			return nil, err
		}
		paths = append(paths, counterPath, s.projectIdentifiersPath(task.ProjectID))
		if err := hub.WriteJSON(w, path, task); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(w, statePath, state); err != nil {
			return nil, err
		}
		counter := model.TaskRunCounter{SchemaVersion: model.SchemaVersion, ProjectID: task.ProjectID, TaskID: task.ID, NextRunNumber: 1}
		if err := model.ValidateTaskRunCounter(counter); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(w, s.taskRunCounterPath(task.ProjectID, task.ID), counter); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(w, s.projectIdentifiersPath(task.ProjectID), updatedIdentifiers); err != nil {
			return nil, err
		}
		return paths, nil
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

func (s *Service) TaskList(ctx context.Context, project string) ([]TaskRecord, error) {
	paths, err := s.Hub.List(ctx, s.projectPrefix(project)+"/tasks", ".json")
	if err != nil {
		return nil, err
	}
	runs, err := s.RunList(ctx, project)
	if err != nil {
		return nil, err
	}
	items := []TaskRecord{}
	for _, path := range paths {
		if strings.HasSuffix(path, ".state.json") || strings.HasSuffix(path, ".run-counter.json") || strings.Contains(path, "/revisions/") {
			continue
		}
		var task model.Task
		if err := s.Hub.ReadJSON(ctx, path, &task); err != nil {
			return nil, err
		}
		state, err := s.taskState(ctx, task)
		if err != nil {
			return nil, err
		}
		var currentRevision *model.TaskRevision
		if model.ValidateCanonicalTaskID(task.ID) == nil {
			if revision, revisionErr := s.currentTaskRevision(ctx, task); revisionErr != nil {
				return nil, revisionErr
			} else {
				currentRevision = &revision
			}
		}
		summaries, err := s.taskReviewSummaries(ctx, task, runs)
		if err != nil {
			return nil, err
		}
		items = append(items, TaskRecord{Task: task, State: state, CurrentRevision: currentRevision, RunSummaries: summaries})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Task.CreatedAt.After(items[j].Task.CreatedAt) })
	return items, nil
}

// taskStatusList reads only the task and mutable state fields needed by the
// project status and workflow-policy projections.  Full TaskList enrichment
// also loads revisions, run history and review summaries; that work belongs
// to the explicit task-list/read surfaces, not the bounded status path.
func (s *Service) taskStatusList(ctx context.Context, project string) ([]TaskRecord, error) {
	paths, err := s.Hub.List(ctx, s.projectPrefix(project)+"/tasks", ".json")
	if err != nil {
		return nil, err
	}
	items := []TaskRecord{}
	for _, path := range paths {
		if strings.HasSuffix(path, ".state.json") || strings.HasSuffix(path, ".run-counter.json") || strings.Contains(path, "/revisions/") {
			continue
		}
		var task model.Task
		if err := s.Hub.ReadJSON(ctx, path, &task); err != nil {
			return nil, err
		}
		if err := model.ValidateTask(task); err != nil {
			return nil, err
		}
		if task.ProjectID != project {
			return nil, fmt.Errorf("task project_id mismatch: %s", task.ID)
		}
		state, err := s.taskState(ctx, task)
		if err != nil {
			return nil, err
		}
		items = append(items, TaskRecord{Task: task, State: state})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Task.CreatedAt.After(items[j].Task.CreatedAt) })
	return items, nil
}

func (s *Service) findTask(ctx context.Context, id string) (model.Task, error) {
	projects, err := s.ProjectList(ctx)
	if err != nil {
		return model.Task{}, err
	}
	for _, p := range projects {
		var t model.Task
		err := s.Hub.ReadJSON(ctx, s.taskPath(p.ID, id), &t)
		if err == nil {
			return t, nil
		}
		if !IsNotFound(err) {
			return model.Task{}, err
		}
	}
	return model.Task{}, fmt.Errorf("task not found: %s", id)
}
func (s *Service) TaskReadRecord(ctx context.Context, id string) (TaskRecord, error) {
	task, err := s.findTask(ctx, id)
	if err != nil {
		return TaskRecord{}, err
	}
	state, err := s.taskState(ctx, task)
	if err != nil {
		return TaskRecord{}, err
	}
	var currentRevision *model.TaskRevision
	if model.ValidateCanonicalTaskID(task.ID) == nil {
		revision, revisionErr := s.currentTaskRevision(ctx, task)
		if revisionErr != nil {
			return TaskRecord{}, revisionErr
		}
		currentRevision = &revision
	}
	runs, err := s.RunList(ctx, task.ProjectID)
	if err != nil {
		return TaskRecord{}, err
	}
	summaries, err := s.taskReviewSummaries(ctx, task, runs)
	if err != nil {
		return TaskRecord{}, err
	}
	var policy *model.ProjectWorkflowPolicy
	if current, policyErr := s.ProjectWorkflowPolicyRead(ctx, task.ProjectID); policyErr == nil {
		policy = &current
	} else if !IsNotFound(policyErr) {
		return TaskRecord{}, policyErr
	}
	return TaskRecord{Task: task, State: state, CurrentRevision: currentRevision, RunSummaries: summaries, WorkflowPolicy: policy}, nil
}
func (s *Service) TaskSupersede(ctx context.Context, oldID string, in TaskCreateInput) (model.Task, OperationResult, error) {
	for attempt := 0; ; attempt++ {
		task, result, err := s.taskSupersedeOnce(ctx, oldID, in)
		if in.ExpectedHubRevision != "" || err == nil || !allocatorConflict(err) || attempt+1 >= allocatorRetryLimit {
			return task, result, err
		}
	}
}

func (s *Service) taskSupersedeOnce(ctx context.Context, oldID string, in TaskCreateInput) (model.Task, OperationResult, error) {
	if err := requireCanonicalTaskID(oldID); err != nil {
		return model.Task{}, OperationResult{}, err
	}
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
	if in.Slug == "" {
		return model.Task{}, OperationResult{}, fmt.Errorf("slug is required")
	}
	if err := model.ValidateTaskSlug(in.Slug); err != nil {
		return model.Task{}, OperationResult{}, err
	}
	identifiers, err := s.ProjectIdentifiersRead(ctx, old.ProjectID)
	if err != nil {
		return model.Task{}, OperationResult{}, err
	}
	_, effectivePolicy, err := s.deriveTaskWorkflowPolicy(ctx, old.ProjectID, in.OperationClass, in.RequiredGates)
	if err != nil {
		return model.Task{}, OperationResult{}, err
	}
	id, err := model.FormatTaskID(identifiers.ProjectCode, identifiers.NextTaskNumber)
	if err != nil {
		return model.Task{}, OperationResult{}, err
	}
	if identifiers.NextTaskNumber == model.MaxSafeInteger {
		if _, readErr := s.Hub.ReadFile(ctx, s.taskPath(old.ProjectID, id)); readErr == nil {
			return model.Task{}, OperationResult{}, fmt.Errorf("task allocator exhausted for project %q", old.ProjectID)
		} else if !IsNotFound(readErr) {
			return model.Task{}, OperationResult{}, readErr
		}
	}
	branch := "task/" + id + "-" + in.Slug
	now := time.Now().UTC()
	newTask := model.Task{SchemaVersion: model.SchemaVersion, ID: id, ProjectID: in.ProjectID, Title: in.Title, Objective: in.Objective, Branch: branch, AcceptanceCriteria: append([]string{}, in.AcceptanceCriteria...), Constraints: append([]string{}, in.Constraints...), RequiredGates: append([]string{}, in.RequiredGates...), WorkflowPolicyRevision: effectivePolicy.WorkflowPolicyRevision, OperationClass: effectivePolicy.OperationClass, EffectiveCIField: effectivePolicy.EffectiveCIField, EffectiveCIMode: effectivePolicy.EffectiveCIMode, WaitForCI: effectivePolicy.WaitForCI, CIBlocking: effectivePolicy.CIBlocking, AgentMayWait: effectivePolicy.AgentMayWait, Status: "created", Supersedes: old.ID, CreatedBy: in.CreatedBy, CreatedAt: now}
	hash, err := model.HashTask(newTask)
	if err != nil {
		return model.Task{}, OperationResult{}, err
	}
	newTask.SHA256 = hash
	if err := model.ValidateTask(newTask); err != nil {
		return model.Task{}, OperationResult{}, err
	}
	original := old
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
	updatedIdentifiers := identifiers
	if updatedIdentifiers.NextTaskNumber < model.MaxSafeInteger {
		updatedIdentifiers.NextTaskNumber++
	}
	tx, err := s.Hub.Transact(ctx, in.ExpectedHubRevision, "gateway: supersede task "+old.ID, func(w string) ([]string, error) {
		targetPaths := []string{s.taskPath(newTask.ProjectID, newTask.ID), s.taskStatePath(newTask.ProjectID, newTask.ID), s.taskRunCounterPath(newTask.ProjectID, newTask.ID)}
		for _, path := range targetPaths {
			if _, err := os.Lstat(filepath.Join(w, filepath.FromSlash(path))); err == nil {
				return nil, fmt.Errorf("task supersede target already exists: %s", path)
			} else if !os.IsNotExist(err) {
				return nil, err
			}
		}
		paths := []string{s.taskPath(newTask.ProjectID, newTask.ID), s.taskStatePath(newTask.ProjectID, newTask.ID), s.taskStatePath(old.ProjectID, old.ID)}
		var current model.ProjectIdentifiers
		if err := readWorktreeJSON(w, s.projectIdentifiersPath(old.ProjectID), &current); err != nil {
			return nil, err
		}
		if current.ProjectCode != identifiers.ProjectCode || current.NextTaskNumber != identifiers.NextTaskNumber {
			return nil, fmt.Errorf("project identifiers changed before task allocation")
		}
		paths = append(paths, s.taskRunCounterPath(newTask.ProjectID, newTask.ID), s.projectIdentifiersPath(old.ProjectID))
		vals := []any{newTask, newState, oldState}
		for i, p := range paths {
			if i >= len(vals) {
				break
			}
			if err := hub.WriteJSON(w, p, vals[i]); err != nil {
				return nil, err
			}
		}
		counter := model.TaskRunCounter{SchemaVersion: model.SchemaVersion, ProjectID: newTask.ProjectID, TaskID: newTask.ID, NextRunNumber: 1}
		if err := model.ValidateTaskRunCounter(counter); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(w, s.taskRunCounterPath(newTask.ProjectID, newTask.ID), counter); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(w, s.projectIdentifiersPath(old.ProjectID), updatedIdentifiers); err != nil {
			return nil, err
		}
		return paths, nil
	})
	if err != nil {
		return model.Task{}, OperationResult{}, err
	}
	return newTask, OperationResult{Hub: tx, ProjectID: newTask.ProjectID, TaskID: newTask.ID, Status: "created"}, nil
}

func (s *Service) TaskCancel(ctx context.Context, id, expected string) (OperationResult, error) {
	if err := requireCanonicalTaskID(id); err != nil {
		return OperationResult{}, err
	}
	task, err := s.findTask(ctx, id)
	if err != nil {
		return OperationResult{}, err
	}
	runs, err := s.RunList(ctx, task.ProjectID)
	if err != nil {
		return OperationResult{}, err
	}
	for _, run := range runs {
		if run.TaskID == task.ID && operationalActiveRun(run) {
			return OperationResult{}, fmt.Errorf("task has active run %s; cancel the run instead", run.ID)
		}
	}
	original := task
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

func (s *Service) TaskMarkMergeReady(ctx context.Context, in TaskMarkMergeReadyInput) (OperationResult, error) {
	if err := requireCanonicalTaskID(in.TaskID); err != nil {
		return OperationResult{}, err
	}
	task, err := s.findTask(ctx, in.TaskID)
	if err != nil {
		return OperationResult{}, err
	}
	state, err := s.taskState(ctx, task)
	if err != nil {
		return OperationResult{}, err
	}
	if state.Status != "completed" {
		return OperationResult{}, fmt.Errorf("task must be completed before merge_ready: %s", state.Status)
	}
	runs, err := s.RunList(ctx, task.ProjectID)
	if err != nil {
		return OperationResult{}, err
	}
	revision := 0
	var revisionSHA string
	if model.ValidateCanonicalTaskID(task.ID) == nil {
		current, revisionErr := s.currentTaskRevision(ctx, task)
		if revisionErr != nil {
			return OperationResult{}, revisionErr
		}
		revision = current.TaskRevision
		revisionSHA = current.RevisionSHA256
	}
	latest, ok := latestApplicableRunForRevision(runs, task.ID, revision, revisionSHA)
	if !ok {
		return OperationResult{}, fmt.Errorf("no canonical successful report for task %s", task.ID)
	}
	if latest.Status != "succeeded" {
		return OperationResult{}, fmt.Errorf("latest applicable run %s is not succeeded: %s", latest.ID, latest.Status)
	}
	report, err := s.RunReport(ctx, latest.ID)
	if err != nil {
		return OperationResult{}, fmt.Errorf("latest successful report is invalid: %w", err)
	}
	if err := model.ValidateCommitSHA(report.Repository.Head); err != nil {
		return OperationResult{}, fmt.Errorf("successful report repository head: %w", err)
	}
	delivery, err := s.readFinalReviewReport(ctx, task, latest)
	if err != nil {
		return OperationResult{}, fmt.Errorf("latest run %s requires a finalized Delivery review: %w", latest.ID, err)
	}
	if delivery.RunID != latest.ID || delivery.TaskSHA256 != task.SHA256 || delivery.Branch != latest.Branch || delivery.BaseRevision != latest.BaseRevision || delivery.Outcome != model.ReviewOutcomeAccepted {
		return OperationResult{}, fmt.Errorf("Delivery review outcome %q does not permit merge-ready", delivery.Outcome)
	}
	if delivery.ReviewedHead != report.Repository.Head {
		return OperationResult{}, fmt.Errorf("Delivery review head does not match successful Agent report")
	}
	tx, err := s.transitionTaskStateWithWorktree(ctx, task, in.ExpectedHubRevision, "gateway: mark task merge-ready "+task.ID, func(worktree string, current model.TaskState) (model.TaskState, error) {
		if current.Status != "completed" {
			return model.TaskState{}, fmt.Errorf("task changed before merge_ready: %s", current.Status)
		}
		currentLatest, found, err := s.latestApplicableRunInWorktree(worktree, task.ProjectID, task.ID)
		if err != nil {
			return model.TaskState{}, err
		}
		if !found || currentLatest.ID != latest.ID || currentLatest.Status != "succeeded" || currentLatest.TaskSHA256 != task.SHA256 || currentLatest.Branch != latest.Branch || currentLatest.BaseRevision != latest.BaseRevision {
			return model.TaskState{}, fmt.Errorf("latest applicable run changed before merge_ready")
		}
		var currentAgent model.Report
		if err := readWorktreeJSON(worktree, s.reportPath(task.ProjectID, currentLatest.ID), &currentAgent); err != nil {
			return model.TaskState{}, fmt.Errorf("Agent report changed before merge_ready: %w", err)
		}
		if err := model.ValidateReport(currentAgent, task, currentLatest, s.Config.MaxListItems); err != nil || currentAgent.Status != "succeeded" || !sameAgentAuthority(currentAgent, report) {
			return model.TaskState{}, fmt.Errorf("Agent report changed before merge_ready")
		}
		deliveryData, err := os.ReadFile(filepath.Join(worktree, filepath.FromSlash(s.reviewReportPath(task.ProjectID, currentLatest.ID))))
		if err != nil {
			return model.TaskState{}, fmt.Errorf("Delivery review changed before merge_ready: %w", err)
		}
		currentDelivery, err := model.ParseRunReviewReport(deliveryData)
		if err != nil {
			return model.TaskState{}, fmt.Errorf("Delivery review changed before merge_ready: %w", err)
		}
		if err := model.ValidateRunReviewReport(currentDelivery); err != nil || currentDelivery.TaskID != task.ID || currentDelivery.RunID != currentLatest.ID || currentDelivery.ProjectID != task.ProjectID || currentDelivery.TaskSHA256 != task.SHA256 || currentDelivery.Branch != currentLatest.Branch || currentDelivery.BaseRevision != currentLatest.BaseRevision || currentDelivery.Outcome != model.ReviewOutcomeAccepted || currentDelivery.ReviewedHead != currentAgent.Repository.Head {
			return model.TaskState{}, fmt.Errorf("Delivery review no longer permits merge-ready")
		}
		current.Status = "merge_ready"
		current.ReviewedHead = currentAgent.Repository.Head
		return current, nil
	})
	if err != nil {
		return OperationResult{}, err
	}
	return OperationResult{Hub: tx, ProjectID: task.ProjectID, TaskID: task.ID, Status: "merge_ready"}, nil
}

func (s *Service) TaskDefer(ctx context.Context, in TaskDeferInput) (OperationResult, error) {
	if err := requireCanonicalTaskID(in.TaskID); err != nil {
		return OperationResult{}, err
	}
	task, err := s.findTask(ctx, in.TaskID)
	if err != nil {
		return OperationResult{}, err
	}
	state, err := s.taskState(ctx, task)
	if err != nil {
		return OperationResult{}, err
	}
	if state.Status != "completed" && state.Status != "merge_ready" {
		return OperationResult{}, fmt.Errorf("task cannot be deferred from state %s", state.Status)
	}
	reason := strings.TrimSpace(in.Reason)
	if strings.ContainsRune(reason, '\x00') {
		return OperationResult{}, fmt.Errorf("deferred reason must not contain NUL")
	}
	if reason == "" {
		return OperationResult{}, fmt.Errorf("deferred reason must be non-empty")
	}
	if len([]byte(reason)) > model.MaxDeferredReasonBytes {
		return OperationResult{}, fmt.Errorf("deferred reason exceeds %d bytes", model.MaxDeferredReasonBytes)
	}
	reviewedHead := state.ReviewedHead
	if state.Status == "completed" {
		report, reportErr := s.latestSuccessfulReport(ctx, task)
		if reportErr != nil {
			return OperationResult{}, reportErr
		}
		reviewedHead = report.Repository.Head
	}
	if err := model.ValidateCommitSHA(reviewedHead); err != nil {
		return OperationResult{}, fmt.Errorf("reviewed head: %w", err)
	}
	tx, err := s.transitionTaskState(ctx, task, in.ExpectedHubRevision, "gateway: defer task "+task.ID, func(current model.TaskState) (model.TaskState, error) {
		switch current.Status {
		case "completed":
			if current.ReviewedHead != "" {
				return model.TaskState{}, fmt.Errorf("task acquired a reviewed head concurrently")
			}
			current.ReviewedHead = reviewedHead
		case "merge_ready":
			if current.ReviewedHead != reviewedHead {
				return model.TaskState{}, fmt.Errorf("reviewed head changed concurrently")
			}
		default:
			return model.TaskState{}, fmt.Errorf("task changed before defer: %s", current.Status)
		}
		current.Status = "deferred"
		current.DeferredReason = reason
		return current, nil
	})
	if err != nil {
		return OperationResult{}, err
	}
	return OperationResult{Hub: tx, ProjectID: task.ProjectID, TaskID: task.ID, Status: "deferred"}, nil
}

func (s *Service) TaskMarkMerged(ctx context.Context, in TaskMarkMergedInput) (OperationResult, error) {
	if err := model.ValidateCommitSHA(in.IntegrationHead); err != nil {
		return OperationResult{}, fmt.Errorf("integration_head: %w", err)
	}
	if err := requireCanonicalTaskID(in.TaskID); err != nil {
		return OperationResult{}, err
	}
	task, err := s.findTask(ctx, in.TaskID)
	if err != nil {
		return OperationResult{}, err
	}
	state, err := s.taskState(ctx, task)
	if err != nil {
		return OperationResult{}, err
	}
	if state.Status != "merge_ready" {
		return OperationResult{}, fmt.Errorf("task must be merge_ready before merged: %s", state.Status)
	}
	if err := model.ValidateCommitSHA(state.ReviewedHead); err != nil {
		return OperationResult{}, fmt.Errorf("reviewed head: %w", err)
	}
	project, err := s.projectConfig(task.ProjectID)
	if err != nil {
		return OperationResult{}, err
	}
	policy, err := s.ProjectWorkflowPolicyRead(ctx, task.ProjectID)
	if err != nil {
		return OperationResult{}, fmt.Errorf("read project workflow policy: %w", err)
	}
	if err := s.Git.Refresh(ctx, project); err != nil {
		return OperationResult{}, err
	}
	taskBranchHead, taskBranchExists, err := s.mirrorRemoteBranchHead(ctx, project, task.Branch)
	if err != nil {
		return OperationResult{}, err
	}
	if !taskBranchExists || taskBranchHead != state.ReviewedHead {
		return OperationResult{}, fmt.Errorf("remote task branch %q does not point at reviewed head", task.Branch)
	}
	integrationHead, integrationExists, err := s.Git.MirrorBranchHead(ctx, project, policy.IntegrationBranch)
	if err != nil {
		return OperationResult{}, err
	}
	if !integrationExists || integrationHead != in.IntegrationHead {
		return OperationResult{}, fmt.Errorf("remote %s does not point at integration head", policy.IntegrationBranch)
	}
	ancestor, err := s.Git.MirrorAncestor(ctx, project, state.ReviewedHead, in.IntegrationHead)
	if err != nil {
		return OperationResult{}, err
	}
	if !ancestor {
		return OperationResult{}, fmt.Errorf("reviewed head is not an ancestor of integration head")
	}
	tx, err := s.transitionTaskState(ctx, task, in.ExpectedHubRevision, "gateway: record merged task "+task.ID, func(current model.TaskState) (model.TaskState, error) {
		if current.Status != "merge_ready" {
			return model.TaskState{}, fmt.Errorf("task changed before merged: %s", current.Status)
		}
		if current.ReviewedHead != state.ReviewedHead {
			return model.TaskState{}, fmt.Errorf("reviewed head changed concurrently")
		}
		current.Status = "merged"
		current.DeferredReason = ""
		current.IntegrationBranch = policy.IntegrationBranch
		current.IntegrationHead = in.IntegrationHead
		return current, nil
	})
	if err != nil {
		return OperationResult{}, err
	}
	return OperationResult{Hub: tx, ProjectID: task.ProjectID, TaskID: task.ID, Status: "merged"}, nil
}

func (s *Service) latestSuccessfulReport(ctx context.Context, task model.Task) (model.Report, error) {
	runs, err := s.RunList(ctx, task.ProjectID)
	if err != nil {
		return model.Report{}, err
	}
	for _, run := range runs {
		if run.TaskID != task.ID || run.Historical || run.Status != "succeeded" {
			continue
		}
		report, reportErr := s.RunReport(ctx, run.ID)
		if reportErr != nil {
			return model.Report{}, fmt.Errorf("latest successful report is invalid: %w", reportErr)
		}
		if report.Status != "succeeded" {
			return model.Report{}, fmt.Errorf("latest successful run has non-success report")
		}
		return report, nil
	}
	return model.Report{}, fmt.Errorf("no canonical successful report for task %s", task.ID)
}

func (s *Service) transitionTaskState(ctx context.Context, task model.Task, expected, subject string, mutate func(model.TaskState) (model.TaskState, error)) (hub.TransactionResult, error) {
	return s.transitionTaskStateWithWorktree(ctx, task, expected, subject, func(_ string, current model.TaskState) (model.TaskState, error) {
		return mutate(current)
	})
}

func (s *Service) transitionTaskStateWithWorktree(ctx context.Context, task model.Task, expected, subject string, mutate func(string, model.TaskState) (model.TaskState, error)) (hub.TransactionResult, error) {
	if expected == "" {
		var err error
		expected, err = s.hubRevision(ctx)
		if err != nil {
			return hub.TransactionResult{}, err
		}
	}
	return s.Hub.Transact(ctx, expected, subject, func(worktree string) ([]string, error) {
		var currentTask model.Task
		if err := readWorktreeJSON(worktree, s.taskPath(task.ProjectID, task.ID), &currentTask); err != nil {
			return nil, err
		}
		if err := model.ValidateTask(currentTask); err != nil {
			return nil, err
		}
		if currentTask.ID != task.ID || currentTask.SHA256 != task.SHA256 {
			return nil, fmt.Errorf("task changed concurrently")
		}
		var current model.TaskState
		if err := readWorktreeJSON(worktree, s.taskStatePath(task.ProjectID, task.ID), &current); err != nil {
			return nil, err
		}
		if err := model.ValidateTaskState(current, currentTask); err != nil {
			return nil, err
		}
		next, err := mutate(worktree, current)
		if err != nil {
			return nil, err
		}
		next.UpdatedAt = time.Now().UTC()
		if err := model.ValidateTaskState(next, currentTask); err != nil {
			return nil, err
		}
		path := s.taskStatePath(task.ProjectID, task.ID)
		if err := hub.WriteJSON(worktree, path, next); err != nil {
			return nil, err
		}
		return []string{path}, nil
	})
}

func (s *Service) mirrorRemoteBranchHead(ctx context.Context, project config.ProjectConfig, branch string) (string, bool, error) {
	return s.Git.MirrorBranchHead(ctx, project, branch)
}

func (s *Service) RunList(ctx context.Context, project string) ([]model.Run, error) {
	paths, err := s.Hub.List(ctx, s.projectPrefix(project)+"/runs", "/run.json")
	if err != nil {
		return nil, err
	}
	items := []model.Run{}
	for _, path := range paths {
		data, err := s.Hub.ReadFile(ctx, path)
		if err != nil {
			return nil, err
		}
		v, _, err := model.DecodeRunRecord(data)
		if err != nil {
			return nil, err
		}
		items = append(items, v)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].ID > items[j].ID
		}
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})
	return items, nil
}

func latestApplicableRunForRevision(runs []model.Run, taskID string, revision int, revisionSHA string) (model.Run, bool) {
	for _, run := range runs {
		if run.TaskID != taskID || run.Historical {
			continue
		}
		runRevision := run.TaskRevision
		if runRevision == 0 {
			runRevision = 1
		}
		if runRevision != revision {
			continue
		}
		if revision > 1 && run.TaskRevisionSHA256 != revisionSHA {
			continue
		}
		if revision == 1 && run.TaskRevision != 0 && run.TaskRevisionSHA256 != revisionSHA {
			continue
		}
		if run.TaskID == taskID && !run.Historical {
			return run, true
		}
	}
	return model.Run{}, false
}

func (s *Service) latestApplicableRunInWorktree(worktree, projectID, taskID string) (model.Run, bool, error) {
	root := filepath.Join(worktree, filepath.FromSlash(s.projectPrefix(projectID)+"/runs"))
	var latest model.Run
	found := false
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			if os.IsNotExist(walkErr) {
				return nil
			}
			return walkErr
		}
		if entry.IsDir() || entry.Name() != "run.json" {
			return nil
		}
		data, err := fsutil.ReadFileBounded(path, s.Config.MaxReadBytes)
		if err != nil {
			return err
		}
		run, _, err := model.DecodeRunRecord(data)
		if err != nil {
			return fmt.Errorf("decode run %s: %w", path, err)
		}
		if run.TaskID != taskID || run.Historical {
			return nil
		}
		if !found || run.CreatedAt.After(latest.CreatedAt) || (run.CreatedAt.Equal(latest.CreatedAt) && run.ID > latest.ID) {
			latest = run
			found = true
		}
		return nil
	})
	return latest, found, err
}
func (s *Service) findRun(ctx context.Context, id string) (model.Run, error) {
	projects, err := s.ProjectList(ctx)
	if err != nil {
		return model.Run{}, err
	}
	for _, p := range projects {
		data, err := s.Hub.ReadFile(ctx, s.runPath(p.ID, id))
		if err == nil {
			r, _, decodeErr := model.DecodeRunRecord(data)
			if decodeErr != nil {
				return model.Run{}, decodeErr
			}
			return r, nil
		}
		if !IsNotFound(err) {
			return model.Run{}, err
		}
	}
	return model.Run{}, fmt.Errorf("run not found: %s", id)
}
func (s *Service) RunRead(ctx context.Context, id string) (model.Run, error) {
	return s.findRun(ctx, id)
}

func (s *Service) RunAgentTail(ctx context.Context, id string, lines int) (string, error) {
	run, err := s.findRun(ctx, id)
	if err != nil {
		return "", err
	}
	if err := requireCanonicalRun(run); err != nil {
		return "", err
	}
	if err := s.ensureRunOwned(run); err != nil {
		return "", err
	}
	if !operationalActiveRun(run) {
		return "", fmt.Errorf("run is not active")
	}
	if lines == 0 {
		lines = 4
	}
	if lines < 1 || lines > 200 {
		return "", fmt.Errorf("invalid tail line count: must be between 1 and 200")
	}
	result, err := s.Airelay.Tail(ctx, run.SessionKey, lines)
	if err != nil {
		return "", err
	}
	return result.Stdout, nil
}
func ensureOperationalRun(run model.Run) error {
	if run.Historical {
		return fmt.Errorf("workflow-v1 run is history-only")
	}
	return nil
}

func requireCanonicalTaskID(id string) error {
	if err := model.ValidateCanonicalTaskID(id); err != nil {
		return fmt.Errorf("task %q is read-only: canonical task ID required", id)
	}
	return nil
}

func requireCanonicalRunID(id string) error {
	if model.ValidateCanonicalRunID(id) != nil && model.ValidateTaskRevisionRunID(id) != nil {
		return fmt.Errorf("run %q is read-only: canonical run ID required", id)
	}
	return nil
}

func requireCanonicalRun(run model.Run) error {
	if run.TaskRevision != 0 {
		if err := model.ValidateTaskRevisionRunID(run.ID); err != nil {
			return fmt.Errorf("run %q is read-only: revision-aware run ID required", run.ID)
		}
	} else if err := requireCanonicalRunID(run.ID); err != nil {
		return err
	}
	return ensureOperationalRun(run)
}
func (s *Service) ensureRunOwned(run model.Run) error {
	if run.GatewayID != s.Config.GatewayID {
		return fmt.Errorf("run %s is assigned to gateway %s, current gateway is %s", run.ID, run.GatewayID, s.Config.GatewayID)
	}
	return nil
}
func activeStatus(s string) bool {
	switch s {
	case "created", "dispatching", "dispatched", "awaiting_result", "cancel_requested":
		return true
	}
	return false
}

// operationalActiveRun distinguishes current workflow-v2 activity from
// immutable workflow-v1 history. Historical records retain their original
// status for auditability and must never own a session or block lifecycle
// mutations.
func validateTaskRunCounterIdentity(counter model.TaskRunCounter, task model.Task) error {
	if counter.ProjectID != task.ProjectID || counter.TaskID != task.ID {
		return fmt.Errorf("task run counter identity mismatch: project_id=%q task_id=%q task_project_id=%q task_id=%q", counter.ProjectID, counter.TaskID, task.ProjectID, task.ID)
	}
	return nil
}

func operationalActiveRun(run model.Run) bool {
	return !run.Historical && activeStatus(run.Status)
}

func (s *Service) checkSessionAvailable(ctx context.Context, session string) error {
	projects, err := s.ProjectList(ctx)
	if err != nil {
		return err
	}
	for _, project := range projects {
		runs, err := s.RunList(ctx, project.ID)
		if err != nil {
			return err
		}
		for _, r := range runs {
			if r.SessionKey == session && operationalActiveRun(r) {
				return fmt.Errorf("active operational run %s already owns the project session", r.ID)
			}
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
	for attempt := 0; ; attempt++ {
		run, result, err := s.taskDispatchOnce(ctx, in)
		if in.ExpectedHubRevision != "" || err == nil || !allocatorConflict(err) || attempt+1 >= allocatorRetryLimit {
			return run, result, err
		}
	}
}

// dispatchExecutionBase resolves the immutable execution authority for a new
// run.  Existing implementation tasks may have been created before the
// canonical integration branch advanced; their published task base remains
// immutable, while the run is pinned to the refreshed branch head.  Other
// operation classes retain their prepared lineage exactly.
func (s *Service) dispatchExecutionBase(ctx context.Context, task model.Task, revision model.TaskRevision, local config.ProjectConfig) (string, error) {
	if revision.OperationClass != "" && revision.OperationClass != "implementation" && revision.BaseRevision != "" {
		resolved, err := s.Git.Resolve(ctx, local.Root, revision.BaseRevision)
		if err != nil || resolved != revision.BaseRevision {
			return "", fmt.Errorf("task base unavailable or mismatched")
		}
		return revision.BaseRevision, nil
	}
	project, err := s.ProjectRead(ctx, task.ProjectID)
	if err != nil {
		return "", err
	}
	branch := project.DefaultBranch
	if policy, policyErr := s.ProjectWorkflowPolicyRead(ctx, task.ProjectID); policyErr == nil && policy.IntegrationBranch != "" {
		branch = policy.IntegrationBranch
	} else if policyErr != nil && !IsNotFound(policyErr) {
		return "", policyErr
	}
	if err := s.Git.Refresh(ctx, local); err != nil {
		return "", fmt.Errorf("refresh canonical execution branch: %w", err)
	}
	head, exists, err := s.Git.MirrorBranchHead(ctx, local, branch)
	if err != nil {
		return "", fmt.Errorf("resolve canonical execution branch: %w", err)
	}
	if !exists || head == "" {
		return "", fmt.Errorf("canonical execution branch %q is unavailable", branch)
	}
	if err := model.ValidateCommitSHA(head); err != nil {
		return "", fmt.Errorf("canonical execution branch head: %w", err)
	}
	return head, nil
}

func (s *Service) taskDispatchOnce(ctx context.Context, in DispatchInput) (model.Run, OperationResult, error) {
	if err := requireCanonicalTaskID(in.TaskID); err != nil {
		return model.Run{}, OperationResult{}, err
	}
	task, err := s.findTask(ctx, in.TaskID)
	if err != nil {
		return model.Run{}, OperationResult{}, err
	}
	revision, err := s.currentTaskRevision(ctx, task)
	if err != nil {
		return model.Run{}, OperationResult{}, err
	}
	revisionAware := revision.TaskRevision > 1
	state, err := s.taskState(ctx, task)
	if err != nil {
		return model.Run{}, OperationResult{}, err
	}
	if state.Status != "created" && state.Status != "ready" {
		return model.Run{}, OperationResult{}, fmt.Errorf("task is not dispatchable: %s", state.Status)
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
	sessionLock, err := s.acquireSessionSendLock(local.AirelaySessionKey)
	if err != nil {
		return model.Run{}, OperationResult{}, err
	}
	defer sessionLock.Release()
	projectLock, err := lockfile.Acquire(filepath.Join(s.Config.StateDir, "locks"), "project-"+task.ProjectID)
	if err != nil {
		return model.Run{}, OperationResult{}, err
	}
	defer projectLock.Release()
	if err := s.checkSessionAvailable(ctx, local.AirelaySessionKey); err != nil {
		return model.Run{}, OperationResult{}, err
	}
	executionBase, err := s.dispatchExecutionBase(ctx, task, revision, local)
	if err != nil {
		return model.Run{}, OperationResult{}, err
	}
	if in.ExpectedHubRevision == "" {
		in.ExpectedHubRevision, err = s.hubRevision(ctx)
		if err != nil {
			return model.Run{}, OperationResult{}, err
		}
	}
	wt, err := s.Git.WorktreeStatus(ctx, local)
	if err != nil {
		return model.Run{}, OperationResult{}, err
	}
	if !wt.Clean {
		return model.Run{}, OperationResult{}, fmt.Errorf("project worktree is dirty")
	}
	resolved, err := s.Git.Resolve(ctx, local.Root, executionBase)
	if err != nil || resolved != executionBase {
		return model.Run{}, OperationResult{}, fmt.Errorf("execution base unavailable or mismatched")
	}
	var counter model.TaskRunCounter
	if err := s.Hub.ReadJSON(ctx, s.taskRunCounterPath(task.ProjectID, task.ID), &counter); err != nil {
		return model.Run{}, OperationResult{}, fmt.Errorf("read task run counter: %w", err)
	}
	if err := model.ValidateTaskRunCounter(counter); err != nil {
		return model.Run{}, OperationResult{}, err
	}
	if err := validateTaskRunCounterIdentity(counter, task); err != nil {
		return model.Run{}, OperationResult{}, err
	}
	id, err := model.FormatRunID(task.ID, counter.NextRunNumber)
	if revisionAware {
		revisionID, revisionErr := model.FormatTaskRevisionID(task.ID, revision.TaskRevision)
		if revisionErr != nil {
			return model.Run{}, OperationResult{}, revisionErr
		}
		id, err = model.FormatTaskRevisionRunID(revisionID, counter.NextRunNumber)
	}
	if err != nil {
		return model.Run{}, OperationResult{}, err
	}
	if counter.NextRunNumber == model.MaxSafeInteger {
		if _, readErr := s.Hub.ReadFile(ctx, s.runPath(task.ProjectID, id)); readErr == nil {
			return model.Run{}, OperationResult{}, fmt.Errorf("run allocator exhausted for task %q", task.ID)
		} else if !IsNotFound(readErr) {
			return model.Run{}, OperationResult{}, readErr
		}
	}
	completionPath, err := canonicalCompletionDestination(s.Config.StateDir, id)
	if err != nil {
		return model.Run{}, OperationResult{}, err
	}
	now := time.Now().UTC()
	run := model.Run{SchemaVersion: model.SchemaVersion, ID: id, TaskID: task.ID, TaskSHA256: task.SHA256, ProjectID: task.ProjectID, GatewayID: s.Config.GatewayID, SessionKey: local.AirelaySessionKey, Branch: revision.Branch, BaseRevision: executionBase, Status: "created", CompletionPath: completionPath, CreatedAt: now}
	if revisionAware {
		run.TaskRevision, run.TaskRevisionSHA256, run.TaskRunNumber = revision.TaskRevision, revision.RevisionSHA256, counter.NextRunNumber
	}
	if err := model.ValidateRun(run); err != nil {
		return model.Run{}, OperationResult{}, err
	}
	if (revisionAware && model.ValidateTaskRevisionRunID(run.ID) != nil) || (!revisionAware && model.ValidateCanonicalRunID(run.ID) != nil) {
		return model.Run{}, OperationResult{}, fmt.Errorf("invalid run identity")
	}
	tx, err := s.Hub.Transact(ctx, in.ExpectedHubRevision, "gateway: create run "+run.ID, func(w string) ([]string, error) {
		var currentPlan model.Plan
		if err := readWorktreeJSON(w, s.planPath(task.ProjectID), &currentPlan); err != nil {
			return nil, err
		}
		var currentState model.TaskState
		if err := readWorktreeJSON(w, s.taskStatePath(task.ProjectID, task.ID), &currentState); err != nil {
			return nil, err
		}
		if err := model.ValidateTaskState(currentState, task); err != nil {
			return nil, err
		}
		if currentState.Status != "created" && currentState.Status != "ready" {
			return nil, fmt.Errorf("task state changed before dispatch: %s", currentState.Status)
		}
		if currentPlan.ActiveTaskID != task.ID || currentPlan.ActiveRunID != "" {
			return nil, fmt.Errorf("plan changed before dispatch")
		}
		paths := []string{s.runPath(run.ProjectID, run.ID), s.taskStatePath(task.ProjectID, task.ID), s.planPath(task.ProjectID)}
		var currentCounter model.TaskRunCounter
		if err := readWorktreeJSON(w, s.taskRunCounterPath(task.ProjectID, task.ID), &currentCounter); err != nil {
			return nil, err
		}
		if err := model.ValidateTaskRunCounter(currentCounter); err != nil {
			return nil, err
		}
		if err := validateTaskRunCounterIdentity(currentCounter, task); err != nil {
			return nil, err
		}
		if currentCounter.NextRunNumber != counter.NextRunNumber {
			return nil, fmt.Errorf("task run counter changed before dispatch")
		}
		if currentCounter.NextRunNumber < model.MaxSafeInteger {
			currentCounter.NextRunNumber++
		}
		if err := model.ValidateTaskRunCounter(currentCounter); err != nil {
			return nil, err
		}
		paths = append(paths, s.taskRunCounterPath(task.ProjectID, task.ID))
		counter = currentCounter
		if err := ensureSessionAvailableInWorktree(w, run.SessionKey, s.Config.MaxReadBytes); err != nil {
			return nil, err
		}
		currentPlan.Revision++
		currentPlan.ActiveRunID = run.ID
		currentPlan.UpdatedBy = s.Config.GatewayID
		currentPlan.UpdatedAt = now
		currentState.Status = "dispatched"
		currentState.UpdatedAt = now
		vals := []any{run, currentState, currentPlan}
		basePaths := []string{s.runPath(run.ProjectID, run.ID), s.taskStatePath(task.ProjectID, task.ID), s.planPath(task.ProjectID)}
		for i, path := range basePaths {
			if err := hub.WriteJSON(w, path, vals[i]); err != nil {
				return nil, err
			}
		}
		if err := hub.WriteJSON(w, s.taskRunCounterPath(task.ProjectID, task.ID), counter); err != nil {
			return nil, err
		}
		return paths, nil
	})
	if err != nil {
		return model.Run{}, OperationResult{}, err
	}
	run.HubRevision = tx.After
	if err := s.writeLocalRun(run, task); err != nil {
		return model.Run{}, OperationResult{}, err
	}
	if err := s.Git.PrepareBranch(ctx, local, revision.Branch, executionBase); err != nil {
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
	if err := requireCanonicalRun(run); err != nil {
		return hub.TransactionResult{}, err
	}
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
	default:
		return "ready"
	}
}
func canonicalReport(report model.Report) model.Report {
	report.GateResults = append([]model.CompletionGateResult{}, report.GateResults...)
	report.AcceptanceCoverage = append([]string{}, report.AcceptanceCoverage...)
	report.Deviations = append([]string{}, report.Deviations...)
	report.RemainingRisks = append([]string{}, report.RemainingRisks...)
	report.Repository.Commits = append([]string{}, report.Repository.Commits...)
	report.Repository.ChangedFiles = append([]string{}, report.Repository.ChangedFiles...)
	if report.AgentFeedback != nil {
		feedback := *report.AgentFeedback
		feedback.Friction = append([]string{}, feedback.Friction...)
		feedback.Improvements = append([]string{}, feedback.Improvements...)
		feedback.ToolCandidates = append([]model.AgentFeedbackToolCandidate{}, feedback.ToolCandidates...)
		report.AgentFeedback = &feedback
	}
	if report.GateResults == nil {
		report.GateResults = []model.CompletionGateResult{}
	}
	if report.AcceptanceCoverage == nil {
		report.AcceptanceCoverage = []string{}
	}
	return report
}

func addUniqueRisk(risks *[]string, risk string) {
	for _, existing := range *risks {
		if existing == risk {
			return
		}
	}
	*risks = append(*risks, risk)
}

func (s *Service) deriveMirrorRepositoryProof(ctx context.Context, run model.Run, project config.ProjectConfig, head string) (model.RepositoryProof, error) {
	resolved, exists, err := s.Git.ResolveMirrorRefStatus(ctx, project, head)
	if err != nil || !exists || resolved != head {
		return model.RepositoryProof{}, fmt.Errorf("durable report HEAD does not resolve exactly")
	}
	ancestor, err := s.Git.MirrorAncestor(ctx, project, run.BaseRevision, head)
	if err != nil {
		return model.RepositoryProof{}, err
	}
	files, err := s.Git.MirrorChangedFiles(ctx, project, run.BaseRevision, head)
	if err != nil {
		return model.RepositoryProof{}, err
	}
	commits, err := s.Git.MirrorLog(ctx, project, run.BaseRevision, head, s.Config.MaxListItems)
	if err != nil {
		return model.RepositoryProof{}, err
	}
	ids := make([]string, 0, len(commits))
	for _, commit := range commits {
		ids = append(ids, commit.SHA)
	}
	return model.RepositoryProof{Branch: run.Branch, Head: head, BaseAncestor: ancestor, Commits: ids, ChangedFiles: files, DiffScope: run.BaseRevision + ".." + head}, nil
}

func (s *Service) durableRepositoryProof(ctx context.Context, run model.Run, project config.ProjectConfig, localHead, localBranch string, localClean, requirePublished bool) (model.RepositoryProof, []string, error) {
	if err := s.Git.Refresh(ctx, project); err != nil {
		return model.RepositoryProof{}, nil, err
	}
	publishedHead, published, err := s.Git.MirrorBranchHead(ctx, project, run.Branch)
	if err != nil {
		return model.RepositoryProof{}, nil, err
	}
	risks := []string{}
	var proof model.RepositoryProof
	if requirePublished {
		if !published || publishedHead != localHead {
			return model.RepositoryProof{}, nil, fmt.Errorf("task branch must be pushed before finalization")
		}
		proof, err = s.deriveMirrorRepositoryProof(ctx, run, project, localHead)
		if err != nil {
			return model.RepositoryProof{}, nil, err
		}
		if !proof.BaseAncestor {
			return model.RepositoryProof{}, nil, fmt.Errorf("final project HEAD is not descended from run execution base")
		}
	} else {
		if published {
			proof, err = s.deriveMirrorRepositoryProof(ctx, run, project, publishedHead)
			if err != nil {
				return model.RepositoryProof{}, nil, err
			}
			if !proof.BaseAncestor {
				return model.RepositoryProof{}, nil, fmt.Errorf("published task branch is not descended from run execution base")
			}
		} else {
			addUniqueRisk(&risks, "published task branch was absent; canonical proof uses the run execution base")
			proof, err = s.deriveMirrorRepositoryProof(ctx, run, project, run.BaseRevision)
			if err != nil {
				return model.RepositoryProof{}, nil, err
			}
		}
	}
	proof.WorktreeClean = localClean && localBranch == run.Branch && localHead == proof.Head
	if localBranch != "" && localBranch != run.Branch {
		addUniqueRisk(&risks, "local worktree was not on the task branch; canonical proof excludes that local state")
	}
	if !localClean {
		addUniqueRisk(&risks, "local worktree was dirty; uncommitted changes were excluded from canonical proof")
	}
	if localHead != "" && localHead != proof.Head {
		addUniqueRisk(&risks, "local unpublished commits were excluded from canonical proof")
	}
	return proof, risks, nil
}

func (s *Service) failRun(ctx context.Context, run model.Run, task model.Task, status, summary, expected string) (hub.TransactionResult, error) {
	if err := requireCanonicalRun(run); err != nil {
		return hub.TransactionResult{}, err
	}
	now := time.Now().UTC()
	run.Status = status
	run.FinishedAt = &now
	local, err := s.projectConfig(task.ProjectID)
	if err != nil {
		return hub.TransactionResult{}, err
	}
	head := run.BaseRevision
	branch := run.Branch
	clean := false
	if local.Root != "" {
		if h, b, c, e := s.Git.CurrentHead(ctx, local); e == nil {
			head, branch, clean = h, b, c
		}
	}
	proof, risks, err := s.durableRepositoryProof(ctx, run, local, head, branch, clean, false)
	if err != nil {
		return hub.TransactionResult{}, err
	}
	report := canonicalReport(model.Report{SchemaVersion: model.SchemaVersion, TaskID: task.ID, RunID: run.ID, ProjectID: task.ProjectID, Status: status, Summary: summary, RemainingRisks: risks, Repository: proof, FinishedAt: now})
	state := model.TaskState{SchemaVersion: model.SchemaVersion, TaskID: task.ID, TaskSHA256: task.SHA256, Status: taskStateStatusForResult(status), UpdatedAt: now}
	plan, err := s.PlanRead(ctx, task.ProjectID)
	if err != nil {
		return hub.TransactionResult{}, err
	}
	plan.Revision++
	plan.ActiveRunID = ""
	plan.ActiveTaskID = ""
	plan.UpdatedBy = s.Config.GatewayID
	plan.UpdatedAt = now
	return s.Hub.Transact(ctx, expected, "gateway: finalize failed run "+run.ID, func(w string) ([]string, error) {
		paths := []string{s.runPath(run.ProjectID, run.ID), s.reportPath(run.ProjectID, run.ID), s.taskStatePath(task.ProjectID, task.ID), s.planPath(task.ProjectID)}
		vals := []any{run, report, state, plan}
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
	var currentRevision *model.TaskRevision
	if model.ValidateCanonicalTaskID(task.ID) == nil {
		revision, revisionErr := s.currentTaskRevision(ctx, task)
		if revisionErr != nil {
			return TaskPacket{}, revisionErr
		}
		currentRevision = &revision
	}
	runs, err := s.RunList(ctx, task.ProjectID)
	if err != nil {
		return TaskPacket{}, err
	}
	matches := []model.Run{}
	for _, r := range runs {
		canonicalRun := model.ValidateCanonicalRunID(r.ID) == nil
		if r.TaskRevision != 0 {
			canonicalRun = model.ValidateTaskRevisionRunID(r.ID) == nil
		}
		if r.TaskID == task.ID && operationalActiveRun(r) && canonicalRun {
			matches = append(matches, r)
		}
	}
	if len(matches) != 1 {
		return TaskPacket{}, fmt.Errorf("expected exactly one active run for task, found %d", len(matches))
	}
	run := matches[0]
	if err := requireCanonicalRun(run); err != nil {
		return TaskPacket{}, err
	}
	if err := s.ensureRunOwned(run); err != nil {
		return TaskPacket{}, err
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
	policy, err := s.ProjectWorkflowPolicyRead(ctx, task.ProjectID)
	if err != nil {
		return TaskPacket{}, fmt.Errorf("read task workflow policy: %w", err)
	}
	text := renderPacket(task, run, project, plan, policy, local.Root)
	summaries, err := s.taskReviewSummaries(ctx, task, runs)
	if err != nil {
		return TaskPacket{}, err
	}
	return TaskPacket{Task: task, CurrentRevision: currentRevision, Run: run, RunSummaries: summaries, Project: project, Plan: plan, WorkflowPolicy: policy, RepositoryRoot: local.Root, CompletionPath: run.CompletionPath, FinalizeCommand: "gpt-tunnel run finalize " + run.ID, Text: text}, nil
}
func renderPacket(task model.Task, run model.Run, project model.Project, plan model.Plan, policy model.ProjectWorkflowPolicy, root string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# GPT Tunnel Agent Execution Packet\n\nTask: %s\nRun: %s\nProject: %s\nRepository: %s\nBranch: %s\nBase: %s\n\n## Objective\n\n%s\n\n## Acceptance criteria\n", task.ID, run.ID, project.ID, root, run.Branch, run.BaseRevision, task.Objective)
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
	fmt.Fprintf(&b, "\n## Durable workflow policy\n\nWorkflow stage: %s\nIntegration branch authority: %s\nAgent wait for hosted CI: %t\nCI modes: task=%s, task_merge=%s, release=%s\nEffective operation class: %s\nEffective CI field/mode: %s/%s\nEffective wait_for_ci: %t\nEffective ci_blocking: %t\nAgent may wait: %t\n\nCurrent Gateway implementation, integration and release tasks do not wait for hosted CI unless the durable project policy explicitly requires it.\n\n## Global plan context\n\n%s\n\nCurrent objective: %s\n\n## Context-compaction recovery\n\nIf context is lost or a compaction marker appears, re-read this immutable task packet with `gpt-tunnel task read %s`. Inspect the declared branch, base, current HEAD, worktree, existing commits, and durable run state. Resume from committed and durable evidence; do not rely on conversation memory, redo completed phases, or change task scope. If implementation is already complete, continue through verification, completion evidence, push, and finalization.\n\n## Completion contract\n\nBefore writing completion.json, commit the implementation, run every required gate, and push the task branch. Then prepare one strict completion JSON input and invoke exactly:\n  gpt-tunnel run write-completion %s --completion-file <INPUT>\n\nThe Gateway obtains the canonical Task and Run, validates the receipt, and derives the only legal completion destination. Do not write directly to a filesystem completion path. Then finalize with:\n  gpt-tunnel run finalize %s\n\nTo read a prior Delivery report, use exactly `gpt-tunnel task report-read <TASK-ID> [RUN-ID]`.\n\nThe task is not complete until finalization prints TASK_FINALIZED. Do not finish only in chat or Airelay.\n", policy.WorkflowStage, policy.IntegrationBranch, policy.Agent.WaitForCI, policy.CI.Task, policy.CI.TaskMerge, policy.CI.Release, task.OperationClass, task.EffectiveCIField, task.EffectiveCIMode, task.WaitForCI, task.CIBlocking, task.AgentMayWait, plan.Summary, plan.CurrentObjective, task.ID, run.ID, run.ID)
	return b.String()
}

func normalizedAbsolutePath(path string) (string, error) {
	abs, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	return filepath.Clean(abs), nil
}

func gatewayCompletionPath(run model.Run, requested string) (string, error) {
	if run.CompletionPath == "" {
		return "", fmt.Errorf("run has no gateway-owned completion path")
	}
	expected, err := normalizedAbsolutePath(run.CompletionPath)
	if err != nil {
		return "", fmt.Errorf("invalid gateway completion path")
	}
	if requested == "" {
		requested = run.CompletionPath
	}
	actual, err := normalizedAbsolutePath(requested)
	if err != nil || actual != expected {
		return "", fmt.Errorf("completion file must equal the gateway-owned run completion path")
	}
	info, err := os.Lstat(actual)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", fmt.Errorf("completion file is not a regular gateway-owned file")
	}
	resolved, err := filepath.EvalSymlinks(actual)
	if err != nil {
		return "", err
	}
	resolved, err = normalizedAbsolutePath(resolved)
	if err != nil || resolved != actual {
		return "", fmt.Errorf("completion file must not resolve outside the gateway-owned path")
	}
	return actual, nil
}

func canonicalCompletionDestination(stateDir, runID string) (string, error) {
	if err := requireCanonicalRunID(runID); err != nil {
		return "", err
	}
	stateRoot, err := normalizedAbsolutePath(stateDir)
	if err != nil {
		return "", fmt.Errorf("invalid gateway state directory")
	}
	return filepath.Join(stateRoot, "runs", runID, "completion.json"), nil
}

func gatewayCompletionDestination(stateDir string, run model.Run) (string, error) {
	if run.CompletionPath == "" {
		return "", fmt.Errorf("run has no gateway-owned completion path")
	}
	expected, err := canonicalCompletionDestination(stateDir, run.ID)
	if err != nil {
		return "", err
	}
	destination, err := normalizedAbsolutePath(run.CompletionPath)
	if err != nil {
		return "", fmt.Errorf("invalid gateway completion path")
	}
	if destination != expected {
		return "", fmt.Errorf("gateway completion path must equal the canonical Run-specific path")
	}
	stateRoot, err := normalizedAbsolutePath(stateDir)
	if err != nil {
		return "", fmt.Errorf("invalid gateway state directory")
	}
	relative, err := filepath.Rel(stateRoot, destination)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", fmt.Errorf("gateway completion path escapes state directory")
	}
	parent := filepath.Dir(destination)
	parentInfo, err := os.Lstat(parent)
	if err != nil {
		return "", err
	}
	if !parentInfo.IsDir() {
		return "", fmt.Errorf("gateway completion directory is not a directory")
	}
	resolvedParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return "", fmt.Errorf("gateway completion directory cannot be resolved: %w", err)
	}
	resolvedParent, err = normalizedAbsolutePath(resolvedParent)
	if err != nil || resolvedParent != parent {
		return "", fmt.Errorf("gateway completion directory must not contain symlinks")
	}
	info, err := os.Lstat(destination)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return "", fmt.Errorf("gateway completion path is not a regular file")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return destination, nil
}

type completionDirectory interface {
	Sync() error
	Close() error
}

var completionOpenDirectory = func(path string) (completionDirectory, error) {
	return os.Open(path)
}

func writeCompletionExclusive(path string, data []byte, task model.Task, maxReadBytes int64) (bool, error) {
	readExisting := func() (bool, error) {
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return false, fmt.Errorf("gateway completion path is not a regular file")
		}
		existing, err := fsutil.ReadFileBounded(path, maxReadBytes)
		if err != nil {
			return false, err
		}
		if bytes.Equal(existing, data) {
			return true, nil
		}
		parsed, err := model.ParseCompletion(existing, task)
		if err == nil {
			canonical, canonicalErr := model.CompletionJSON(parsed)
			if canonicalErr == nil && bytes.Equal(append(canonical, '\n'), data) {
				return true, nil
			}
		}
		return false, fmt.Errorf("gateway completion already exists with different content")
	}
	if same, err := readExisting(); err != nil || same {
		return same, err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".completion-*")
	if err != nil {
		return false, err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return false, err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return false, err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return false, err
	}
	if err := temporary.Close(); err != nil {
		return false, err
	}
	if err := os.Link(temporaryPath, path); err == nil {
		directory, openErr := completionOpenDirectory(filepath.Dir(path))
		if openErr != nil {
			return false, fmt.Errorf("open completion directory for sync: %w", openErr)
		}
		if syncErr := directory.Sync(); syncErr != nil {
			_ = directory.Close()
			return false, fmt.Errorf("sync completion directory: %w", syncErr)
		}
		if closeErr := directory.Close(); closeErr != nil {
			return false, fmt.Errorf("close completion directory: %w", closeErr)
		}
		return false, nil
	} else if !errors.Is(err, os.ErrExist) {
		return false, err
	}
	if same, err := readExisting(); err != nil {
		return false, err
	} else if same {
		return true, nil
	}
	return false, fmt.Errorf("gateway completion appeared with different content")
}

func (s *Service) RunWriteCompletion(ctx context.Context, in CompletionWriteInput) (CompletionWriteResult, error) {
	if err := requireCanonicalRunID(in.RunID); err != nil {
		return CompletionWriteResult{}, err
	}
	if in.CompletionFile == "" {
		return CompletionWriteResult{}, fmt.Errorf("completion file is required")
	}
	inputInfo, err := os.Lstat(in.CompletionFile)
	if err != nil {
		return CompletionWriteResult{}, err
	}
	if inputInfo.Mode()&os.ModeSymlink != 0 || !inputInfo.Mode().IsRegular() {
		return CompletionWriteResult{}, fmt.Errorf("completion input must be a regular non-symlink file")
	}
	loadAuthority := func() (model.Run, model.Task, string, error) {
		run, err := s.findRun(ctx, in.RunID)
		if err != nil {
			return model.Run{}, model.Task{}, "", err
		}
		if err := requireCanonicalRun(run); err != nil {
			return model.Run{}, model.Task{}, "", err
		}
		if err := s.ensureRunOwned(run); err != nil {
			return model.Run{}, model.Task{}, "", err
		}
		if !operationalActiveRun(run) {
			return model.Run{}, model.Task{}, "", fmt.Errorf("run is not active: %s", run.Status)
		}
		task, err := s.findTask(ctx, run.TaskID)
		if err != nil {
			return model.Run{}, model.Task{}, "", err
		}
		if err := model.ValidateTask(task); err != nil {
			return model.Run{}, model.Task{}, "", err
		}
		if err := requireCanonicalTaskID(task.ID); err != nil {
			return model.Run{}, model.Task{}, "", err
		}
		if task.ID != run.TaskID || task.ProjectID != run.ProjectID || task.SHA256 != run.TaskSHA256 {
			return model.Run{}, model.Task{}, "", fmt.Errorf("canonical task/run identity mismatch")
		}
		if err := model.ValidateTaskHash(task); err != nil || run.TaskSHA256 != task.SHA256 {
			return model.Run{}, model.Task{}, "", fmt.Errorf("durable task hash mismatch")
		}
		destination, err := gatewayCompletionDestination(s.Config.StateDir, run)
		if err != nil {
			return model.Run{}, model.Task{}, "", err
		}
		return run, task, destination, nil
	}
	run, task, destination, err := loadAuthority()
	if err != nil {
		return CompletionWriteResult{}, err
	}
	projectLock, err := lockfile.Acquire(filepath.Join(s.Config.StateDir, "locks"), "project-"+run.ProjectID)
	if err != nil {
		return CompletionWriteResult{}, err
	}
	defer projectLock.Release()
	run, task, destination, err = loadAuthority()
	if err != nil {
		return CompletionWriteResult{}, err
	}
	data, err := fsutil.ReadFileBounded(in.CompletionFile, s.Config.MaxReadBytes)
	if err != nil {
		return CompletionWriteResult{}, err
	}
	completion, err := model.ParseCompletion(data, task)
	if err != nil {
		return CompletionWriteResult{}, err
	}
	if completion.RunID != run.ID || completion.TaskSHA256 != run.TaskSHA256 || completion.TaskRevision != run.TaskRevision || completion.TaskRevisionSHA256 != run.TaskRevisionSHA256 || completion.TaskRunNumber != run.TaskRunNumber {
		return CompletionWriteResult{}, fmt.Errorf("completion identity does not match canonical run")
	}
	canonical, err := model.CompletionJSON(completion)
	if err != nil {
		return CompletionWriteResult{}, err
	}
	canonical = append(canonical, '\n')
	alreadyPresent, err := writeCompletionExclusive(destination, canonical, task, s.Config.MaxReadBytes)
	if err != nil {
		return CompletionWriteResult{}, err
	}
	status := "WRITTEN"
	if alreadyPresent {
		status = "ALREADY_PRESENT"
	}
	return CompletionWriteResult{Status: status, Path: destination, ProjectID: run.ProjectID, TaskID: task.ID, RunID: run.ID}, nil
}

func (s *Service) RunFinalize(ctx context.Context, in FinalizeInput) (model.Report, OperationResult, error) {
	if err := requireCanonicalRunID(in.RunID); err != nil {
		return model.Report{}, OperationResult{}, err
	}
	run, err := s.findRun(ctx, in.RunID)
	if err != nil {
		return model.Report{}, OperationResult{}, err
	}
	if err := requireCanonicalRun(run); err != nil {
		return model.Report{}, OperationResult{}, err
	}
	if err := s.ensureRunOwned(run); err != nil {
		return model.Report{}, OperationResult{}, err
	}
	if !operationalActiveRun(run) {
		return model.Report{}, OperationResult{}, fmt.Errorf("run is not active: %s", run.Status)
	}
	task, err := s.findTask(ctx, run.TaskID)
	if err != nil {
		return model.Report{}, OperationResult{}, err
	}
	canonicalCompletionPath, err := gatewayCompletionDestination(s.Config.StateDir, run)
	if err != nil {
		return model.Report{}, OperationResult{}, err
	}
	completionPath, err := gatewayCompletionPath(run, in.CompletionFile)
	if err != nil {
		return model.Report{}, OperationResult{}, err
	}
	if completionPath != canonicalCompletionPath {
		return model.Report{}, OperationResult{}, fmt.Errorf("completion file must equal the canonical Run-specific path")
	}
	data, err := fsutil.ReadFileBounded(completionPath, s.Config.MaxReadBytes)
	if err != nil {
		return model.Report{}, OperationResult{}, err
	}
	if s.Config.MaxReadBytes > 0 && int64(len(data)) > s.Config.MaxReadBytes {
		return model.Report{}, OperationResult{}, fmt.Errorf("completion exceeds configured output limit")
	}
	completion, err := model.ParseCompletion(data, task)
	if err != nil {
		return model.Report{}, OperationResult{}, err
	}
	if completion.RunID != run.ID || completion.TaskSHA256 != run.TaskSHA256 || completion.TaskRevision != run.TaskRevision || completion.TaskRevisionSHA256 != run.TaskRevisionSHA256 || completion.TaskRunNumber != run.TaskRunNumber {
		return model.Report{}, OperationResult{}, fmt.Errorf("completion identity does not match active run")
	}
	if err := model.ValidateTaskHash(task); err != nil || run.TaskSHA256 != task.SHA256 {
		return model.Report{}, OperationResult{}, fmt.Errorf("durable task hash mismatch")
	}
	local, err := s.projectConfig(run.ProjectID)
	if err != nil {
		return model.Report{}, OperationResult{}, err
	}
	head, branch, clean, err := s.Git.CurrentHead(ctx, local)
	if err != nil {
		return model.Report{}, OperationResult{}, err
	}
	if branch != run.Branch {
		return model.Report{}, OperationResult{}, fmt.Errorf("repository branch does not match task branch")
	}
	if completion.Status == "succeeded" && !clean {
		return model.Report{}, OperationResult{}, fmt.Errorf("successful run must leave clean worktree")
	}
	proof, risks, err := s.durableRepositoryProof(ctx, run, local, head, branch, clean, true)
	if err != nil {
		return model.Report{}, OperationResult{}, err
	}
	now := time.Now().UTC()
	run.Status = completion.Status
	run.FinishedAt = &now
	remainingRisks := append([]string{}, completion.RemainingRisks...)
	for _, risk := range risks {
		addUniqueRisk(&remainingRisks, risk)
	}
	report := canonicalReport(model.Report{SchemaVersion: model.SchemaVersion, TaskID: task.ID, RunID: run.ID, TaskRevision: run.TaskRevision, TaskRevisionSHA256: run.TaskRevisionSHA256, TaskRunNumber: run.TaskRunNumber, ProjectID: run.ProjectID, Status: completion.Status, Summary: completion.Summary, GateResults: completion.GateResults, AcceptanceCoverage: completion.AcceptanceCoverage, Deviations: completion.Deviations, RemainingRisks: remainingRisks, AgentFeedback: completion.AgentFeedback, Repository: proof, FinishedAt: now})
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
	plan.ActiveTaskID = ""
	plan.UpdatedBy = s.Config.GatewayID
	plan.UpdatedAt = now
	tx, err := s.Hub.Transact(ctx, expected, "gateway: finalize run "+run.ID, func(w string) ([]string, error) {
		state := model.TaskState{SchemaVersion: model.SchemaVersion, TaskID: task.ID, TaskSHA256: task.SHA256, Status: taskStateStatusForResult(completion.Status), UpdatedAt: now}
		paths := []string{s.runPath(run.ProjectID, run.ID), s.reportPath(run.ProjectID, run.ID), s.taskStatePath(task.ProjectID, task.ID), s.planPath(task.ProjectID)}
		vals := []any{run, report, state, plan}
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
	if run.Historical {
		return model.Report{}, fmt.Errorf("workflow-v1 run report is history-only")
	}
	var report model.Report
	path := s.reportPath(run.ProjectID, id)
	if err := s.Hub.ReadJSON(ctx, path, &report); err != nil {
		return model.Report{}, err
	}
	task, err := s.readTaskForRun(ctx, run)
	if err != nil {
		return model.Report{}, err
	}
	if err := model.ValidateReport(report, task, run, s.Config.MaxListItems); err != nil {
		return model.Report{}, err
	}
	local, err := s.projectConfig(run.ProjectID)
	if err != nil {
		return model.Report{}, err
	}
	if err := s.Git.Refresh(ctx, local); err != nil {
		return model.Report{}, err
	}
	if err := s.validateCanonicalReportProof(ctx, report, run, local); err != nil {
		return model.Report{}, err
	}
	if run.Status != report.Status {
		return model.Report{}, fmt.Errorf("report status does not match run")
	}
	state, err := s.taskState(ctx, task)
	if err != nil {
		return model.Report{}, err
	}
	if report.Status == "succeeded" {
		switch state.Status {
		case "completed", "merge_ready", "deferred", "merged":
		default:
			return model.Report{}, fmt.Errorf("report status does not match task state")
		}
	} else if state.Status != "ready" {
		return model.Report{}, fmt.Errorf("report status does not match task state")
	}
	commit, err := s.Hub.LastChange(ctx, path)
	if err != nil {
		return model.Report{}, err
	}
	report.HubCommit = commit
	return canonicalReport(report), nil
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func (s *Service) validateCanonicalReportProof(ctx context.Context, report model.Report, run model.Run, project config.ProjectConfig) error {
	proof, err := s.deriveMirrorRepositoryProof(ctx, run, project, report.Repository.Head)
	if err != nil {
		return err
	}
	if report.Repository.Branch != proof.Branch || report.Repository.BaseAncestor != proof.BaseAncestor || report.Repository.DiffScope != proof.DiffScope {
		return fmt.Errorf("report repository proof does not match Git")
	}
	if !sameStrings(proof.ChangedFiles, report.Repository.ChangedFiles) {
		return fmt.Errorf("report changed files do not match Git")
	}
	if !sameStrings(proof.Commits, report.Repository.Commits) {
		return fmt.Errorf("report commits do not match Git history")
	}
	branchHead, branchExists, err := s.Git.MirrorBranchHead(ctx, project, proof.Branch)
	if err != nil {
		return err
	}
	if branchExists {
		if branchHead != report.Repository.Head {
			return fmt.Errorf("report branch does not point at reported HEAD")
		}
		return nil
	}
	if report.Repository.Head == run.BaseRevision {
		return nil
	}
	defaultHead, defaultExists, err := s.Git.MirrorBranchHead(ctx, project, project.DefaultBranch)
	if err != nil {
		return err
	}
	if !defaultExists {
		return fmt.Errorf("default branch is unavailable for deleted task branch")
	}
	reachable, err := s.Git.MirrorAncestor(ctx, project, report.Repository.Head, defaultHead)
	if err != nil {
		return err
	}
	if !reachable {
		return fmt.Errorf("reported HEAD is not reachable from the default branch")
	}
	return nil
}

func (s *Service) RunCancel(ctx context.Context, id, expected string) (OperationResult, error) {
	if err := requireCanonicalRunID(id); err != nil {
		return OperationResult{}, err
	}
	run, err := s.findRun(ctx, id)
	if err != nil {
		return OperationResult{}, err
	}
	if err := requireCanonicalRun(run); err != nil {
		return OperationResult{}, err
	}
	if err := s.ensureRunOwned(run); err != nil {
		return OperationResult{}, err
	}
	sessionLock, err := s.acquireSessionSendLock(run.SessionKey)
	if err != nil {
		return OperationResult{}, err
	}
	defer sessionLock.Release()
	run, err = s.findRun(ctx, id)
	if err != nil {
		return OperationResult{}, err
	}
	if err := s.ensureRunOwned(run); err != nil {
		return OperationResult{}, err
	}
	if err := requireCanonicalRun(run); err != nil {
		return OperationResult{}, err
	}
	if !operationalActiveRun(run) {
		return OperationResult{}, fmt.Errorf("run is terminal")
	}
	run.Status = "cancel_requested"
	published, err := s.updateRun(ctx, run, expected, "gateway: request cancellation "+run.ID)
	if err != nil {
		return OperationResult{}, err
	}
	message := "Cancel task execution. Run: gpt-tunnel run read " + run.ID
	dispatch, dispatchErr := s.Airelay.Prompt(ctx, run.SessionKey, message)
	code := dispatch.ExitCode
	run.DispatchExitCode = &code
	run.DispatchStdout = dispatch.Stdout
	run.DispatchStderr = dispatch.Stderr
	recorded, recordErr := s.updateRun(ctx, run, published.After, "gateway: record cancellation delivery "+run.ID)
	if recordErr != nil {
		return OperationResult{Hub: published, ProjectID: run.ProjectID, TaskID: run.TaskID, RunID: run.ID, Status: run.Status}, fmt.Errorf("cancellation published but delivery evidence was not recorded: %w", recordErr)
	}
	result := OperationResult{Hub: recorded, ProjectID: run.ProjectID, TaskID: run.TaskID, RunID: run.ID, Status: run.Status}
	if dispatchErr != nil {
		return result, dispatchErr
	}
	return result, nil
}

const cancelDeliveryOutputLimit = 8192

func validateCancelDelivery(run model.Run) error {
	if run.DispatchExitCode == nil || *run.DispatchExitCode != 0 {
		return fmt.Errorf("cancellation delivery was not successful")
	}
	if strings.TrimSpace(run.DispatchStdout) == "" {
		return fmt.Errorf("cancellation delivery produced no stdout")
	}
	if run.DispatchStderr != "" {
		return fmt.Errorf("cancellation delivery produced stderr")
	}
	if len([]byte(run.DispatchStdout)) > cancelDeliveryOutputLimit || len([]byte(run.DispatchStderr)) > cancelDeliveryOutputLimit {
		return fmt.Errorf("cancellation delivery evidence exceeds output limit")
	}
	return nil
}

func readCurrentRun(worktree, path string, maxReadBytes int64) (model.Run, error) {
	data, err := fsutil.ReadFileBounded(filepath.Join(worktree, filepath.FromSlash(path)), maxReadBytes)
	if err != nil {
		return model.Run{}, err
	}
	run, historical, err := model.DecodeRunRecord(data)
	if err != nil {
		return model.Run{}, err
	}
	if historical {
		return model.Run{}, fmt.Errorf("workflow-v1 run is history-only")
	}
	return run, nil
}

func (s *Service) validateCancelNoMutationWorktree(ctx context.Context, task model.Task, executionBase string) error {
	local, err := s.projectConfig(task.ProjectID)
	if err != nil {
		return err
	}
	status, err := s.Git.WorktreeStatus(ctx, local)
	if err != nil {
		return err
	}
	if status.Branch != task.Branch {
		return fmt.Errorf("repository branch does not match task branch")
	}
	if !status.Clean {
		return fmt.Errorf("repository worktree is dirty or conflicted")
	}
	if status.Head != executionBase {
		return fmt.Errorf("repository HEAD does not match run execution base")
	}
	if status.Upstream != "" && (status.Ahead != 0 || status.Behind != 0) {
		return fmt.Errorf("task branch differs from its upstream")
	}
	return nil
}

// RunCancelAcknowledgeNoMutation closes a successfully delivered cancellation
// only when the task branch proves that no source mutation occurred.
func (s *Service) RunCancelAcknowledgeNoMutation(ctx context.Context, id, expected string) (OperationResult, error) {
	if err := requireCanonicalRunID(id); err != nil {
		return OperationResult{}, err
	}
	run, err := s.findRun(ctx, id)
	if err != nil {
		return OperationResult{}, err
	}
	if err := model.ValidateRun(run); err != nil {
		return OperationResult{}, err
	}
	if err := requireCanonicalRun(run); err != nil {
		return OperationResult{}, err
	}
	if err := s.ensureRunOwned(run); err != nil {
		return OperationResult{}, err
	}
	if run.Status != "cancel_requested" {
		return OperationResult{}, fmt.Errorf("run status must be cancel_requested")
	}
	if err := validateCancelDelivery(run); err != nil {
		return OperationResult{}, err
	}

	task, err := s.findTask(ctx, run.TaskID)
	if err != nil {
		return OperationResult{}, err
	}
	if err := model.ValidateTask(task); err != nil {
		return OperationResult{}, err
	}
	if task.ID != run.TaskID || task.ProjectID != run.ProjectID || task.SHA256 != run.TaskSHA256 {
		return OperationResult{}, fmt.Errorf("cancelled run task identity does not match")
	}
	if run.Branch != task.Branch {
		return OperationResult{}, fmt.Errorf("cancelled run repository identity does not match task")
	}
	if hashErr := model.ValidateTaskHash(task); hashErr != nil {
		return OperationResult{}, fmt.Errorf("durable task hash mismatch")
	}
	projectLock, err := lockfile.Acquire(filepath.Join(s.Config.StateDir, "locks"), "project-"+task.ProjectID)
	if err != nil {
		return OperationResult{}, err
	}
	defer projectLock.Release()
	state, err := s.taskState(ctx, task)
	if err != nil {
		return OperationResult{}, err
	}
	if err := model.ValidateTaskState(state, task); err != nil {
		return OperationResult{}, err
	}
	if state.Status != "dispatched" {
		return OperationResult{}, fmt.Errorf("task state must be dispatched")
	}
	plan, err := s.PlanRead(ctx, task.ProjectID)
	if err != nil {
		return OperationResult{}, err
	}
	if err := model.ValidatePlan(plan); err != nil {
		return OperationResult{}, err
	}
	if plan.ActiveTaskID != task.ID || plan.ActiveRunID != run.ID {
		return OperationResult{}, fmt.Errorf("plan does not own cancelled task and run")
	}
	completionExists, err := os.Lstat(run.CompletionPath)
	if err == nil {
		_ = completionExists
		return OperationResult{}, fmt.Errorf("completion file already exists")
	}
	if !errors.Is(err, os.ErrNotExist) {
		return OperationResult{}, err
	}
	if err := s.validateCancelNoMutationWorktree(ctx, task, run.BaseRevision); err != nil {
		return OperationResult{}, err
	}
	if expected == "" {
		expected, err = s.hubRevision(ctx)
		if err != nil {
			return OperationResult{}, err
		}
	}
	deliveryExitCode := *run.DispatchExitCode
	deliveryStdout := run.DispatchStdout
	deliveryStderr := run.DispatchStderr

	now := time.Now().UTC()
	tx, err := s.Hub.Transact(ctx, expected, "gateway: acknowledge cancellation without mutation "+run.ID, func(worktree string) ([]string, error) {
		currentRun, err := readCurrentRun(worktree, s.runPath(run.ProjectID, run.ID), s.Config.MaxReadBytes)
		if err != nil {
			return nil, err
		}
		currentTask := model.Task{}
		if err := readWorktreeJSON(worktree, s.taskPath(task.ProjectID, task.ID), &currentTask); err != nil {
			return nil, err
		}
		currentState := model.TaskState{}
		if err := readWorktreeJSON(worktree, s.taskStatePath(task.ProjectID, task.ID), &currentState); err != nil {
			return nil, err
		}
		currentPlan := model.Plan{}
		if err := readWorktreeJSON(worktree, s.planPath(task.ProjectID), &currentPlan); err != nil {
			return nil, err
		}
		if err := model.ValidateRun(currentRun); err != nil {
			return nil, err
		}
		if currentRun.ID != run.ID || currentRun.TaskID != task.ID || currentRun.TaskSHA256 != task.SHA256 || currentRun.ProjectID != task.ProjectID || currentRun.CompletionPath != run.CompletionPath || currentRun.Branch != task.Branch || currentRun.BaseRevision != run.BaseRevision {
			return nil, fmt.Errorf("run changed before cancellation acknowledgement")
		}
		if err := requireCanonicalRun(currentRun); err != nil {
			return nil, err
		}
		if currentRun.Status != "cancel_requested" {
			return nil, fmt.Errorf("run changed before cancellation acknowledgement")
		}
		if err := validateCancelDelivery(currentRun); err != nil {
			return nil, err
		}
		if currentRun.DispatchExitCode == nil || *currentRun.DispatchExitCode != deliveryExitCode || currentRun.DispatchStdout != deliveryStdout || currentRun.DispatchStderr != deliveryStderr {
			return nil, fmt.Errorf("cancellation delivery evidence changed before acknowledgement")
		}
		if err := model.ValidateTask(currentTask); err != nil {
			return nil, err
		}
		if currentTask.ID != task.ID || currentTask.ProjectID != task.ProjectID || currentTask.SHA256 != task.SHA256 {
			return nil, fmt.Errorf("task changed before cancellation acknowledgement")
		}
		if hashErr := model.ValidateTaskHash(currentTask); hashErr != nil {
			return nil, fmt.Errorf("durable task hash mismatch")
		}
		if err := model.ValidateTaskState(currentState, currentTask); err != nil {
			return nil, err
		}
		if currentState.Status != "dispatched" {
			return nil, fmt.Errorf("task state changed before cancellation acknowledgement")
		}
		if err := model.ValidatePlan(currentPlan); err != nil {
			return nil, err
		}
		if currentPlan.ProjectID != task.ProjectID || currentPlan.ActiveTaskID != task.ID || currentPlan.ActiveRunID != run.ID {
			return nil, fmt.Errorf("plan changed before cancellation acknowledgement")
		}

		currentRun.Status = "failed"
		currentRun.FinishedAt = &now
		currentState.Status = taskStateStatusForResult("failed")
		currentState.UpdatedAt = now
		currentPlan.Revision++
		currentPlan.ActiveTaskID = ""
		currentPlan.ActiveRunID = ""
		currentPlan.UpdatedBy = s.Config.GatewayID
		currentPlan.UpdatedAt = now
		if err := model.ValidateRun(currentRun); err != nil {
			return nil, err
		}
		if err := model.ValidateTaskState(currentState, currentTask); err != nil {
			return nil, err
		}
		if err := model.ValidatePlan(currentPlan); err != nil {
			return nil, err
		}
		paths := []string{s.runPath(run.ProjectID, run.ID), s.taskStatePath(task.ProjectID, task.ID), s.planPath(task.ProjectID)}
		values := []any{currentRun, currentState, currentPlan}
		for i, path := range paths {
			if err := hub.WriteJSON(worktree, path, values[i]); err != nil {
				return nil, err
			}
		}
		return paths, nil
	})
	if err != nil {
		return OperationResult{}, err
	}
	return OperationResult{Hub: tx, ProjectID: run.ProjectID, TaskID: run.TaskID, RunID: run.ID, Status: "cancelled_no_mutation"}, nil
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
			if model.ValidateCanonicalRunID(run.ID) != nil || run.GatewayID != s.Config.GatewayID || !operationalActiveRun(run) {
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
			if run.Status != "cancel_requested" {
				if e := s.observeResumeProgress(ctx, run, now); e != nil {
					out.Items = append(out.Items, SweepItem{RunID: run.ID, Action: "error", Status: run.Status, Error: "liveness observation failed"})
					continue
				}
				if _, resumeErr := s.runResume(ctx, run.ID, true); resumeErr == nil {
					out.Items = append(out.Items, SweepItem{RunID: run.ID, Action: "resume", Status: run.Status})
					continue
				}
			}
			if now.Sub(start) < time.Duration(s.Config.RunTimeoutSeconds)*time.Second {
				continue
			}
			task, e := s.findTask(ctx, run.TaskID)
			if e != nil {
				out.Items = append(out.Items, SweepItem{RunID: run.ID, Action: "error", Status: run.Status, Error: e.Error()})
				continue
			}
			if run.Status == "cancel_requested" {
				expected, e := s.hubRevision(ctx)
				if e != nil {
					return out, e
				}
				tx, e := s.failRun(ctx, run, task, "failed", "cooperative cancellation timed out", expected)
				_ = tx
				item := SweepItem{RunID: run.ID, Action: "finalize_cancelled", Status: "failed"}
				if e != nil {
					item.Error = e.Error()
				}
				out.Items = append(out.Items, item)
				continue
			}
			if run.RepromptCount < 1 {
				_, resumeErr := s.runResume(ctx, run.ID, true)
				item := SweepItem{RunID: run.ID, Action: "resume", Status: run.Status}
				if resumeErr != nil {
					// A stale run without confirmed compaction is not silently
					// reprompted.  It remains visible for an explicit operator/GPT
					// decision instead of receiving a bare continue message.
					item.Action = "review"
					item.Error = "automatic resume not performed"
				}
				out.Items = append(out.Items, item)
				continue
			}
			expected, e := s.hubRevision(ctx)
			if e != nil {
				return out, e
			}
			tx, e := s.failRun(ctx, run, task, "failed", "agent completion was not finalized before timeout", expected)
			_ = tx
			item := SweepItem{RunID: run.ID, Action: "finalize_timeout", Status: "failed"}
			if e != nil {
				item.Error = e.Error()
			}
			out.Items = append(out.Items, item)
		}
	}
	return out, nil
}

func IsNotFound(err error) bool {
	if err == nil {
		return false
	}
	text := err.Error()
	return errors.Is(err, os.ErrNotExist) || strings.Contains(text, "does not exist") || strings.Contains(text, "not found") || strings.Contains(text, "exists on disk, but not in")
}
