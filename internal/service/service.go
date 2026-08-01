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
	RunID          string `json:"run_id"`
	CompletionFile string `json:"completion_file,omitempty"`
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
	Plan        model.PlanStatus     `json:"plan"`
	HubRevision string               `json:"hub_revision"`
}

type TaskRecord struct {
	Task  model.Task      `json:"task"`
	State model.TaskState `json:"state"`
}

type TaskPacket struct {
	Task            model.Task    `json:"task"`
	Run             model.Run     `json:"run"`
	Project         model.Project `json:"project"`
	Plan            model.Plan    `json:"plan"`
	RepositoryRoot  string        `json:"repository_root"`
	CompletionPath  string        `json:"completion_path"`
	FinalizeCommand string        `json:"finalize_command"`
	Text            string        `json:"text"`
}

func (s *Service) projectPrefix(id string) string {
	if model.ValidateProjectIdentifier(id) != nil {
		return "../invalid-project-id"
	}
	return filepath.ToSlash(filepath.Join(hub.ProtocolRoot, "projects", id))
}
func (s *Service) projectPath(id string) string { return s.projectPrefix(id) + "/project.json" }
func (s *Service) planPath(id string) string    { return s.projectPrefix(id) + "/plan/current.json" }
func (s *Service) planSectionPath(project, id string) string {
	if model.ValidateObjectIdentifier(id) != nil {
		return "../invalid-plan-section-id"
	}
	return s.projectPrefix(project) + "/plan/sections/" + id + ".json"
}
func (s *Service) adrPath(project, id string) string {
	if model.ValidateADRIdentifier(id) != nil {
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
		if run.SessionKey == session && activeStatus(run.Status) {
			return fmt.Errorf("session %s already has active run %s", session, run.ID)
		}
		return nil
	})
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
	items, err := s.ProjectList(ctx)
	if err != nil {
		return fmt.Errorf("validate durable project records: %w", err)
	}
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		seen[item.ID] = true
	}
	missing := []string{}
	for id := range s.Config.Projects {
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
	plan, err := s.PlanRead(ctx, id)
	if err != nil {
		return ProjectStatus{}, err
	}
	rev, err := s.hubRevision(ctx)
	if err != nil {
		return ProjectStatus{}, err
	}
	return ProjectStatus{Project: p, Local: local, Worktree: wt, Plan: plan.StatusView(), HubRevision: rev}, nil
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
	items := []TaskRecord{}
	for _, path := range paths {
		if strings.HasSuffix(path, ".state.json") {
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
	return TaskRecord{Task: task, State: state}, nil
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
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.After(items[j].CreatedAt) })
	return items, nil
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
	if err := ensureOperationalRun(run); err != nil {
		return "", err
	}
	if err := s.ensureRunOwned(run); err != nil {
		return "", err
	}
	if !activeStatus(run.Status) {
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
			if r.SessionKey == session && activeStatus(r.Status) {
				return fmt.Errorf("session %s already has active run %s", session, r.ID)
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
	task, err := s.findTask(ctx, in.TaskID)
	if err != nil {
		return model.Run{}, OperationResult{}, err
	}
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
	sessionLock, err := lockfile.Acquire(filepath.Join(s.Config.StateDir, "locks"), "session-"+local.AirelaySessionKey)
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
	resolved, err := s.Git.Resolve(ctx, local.Root, task.BaseRevision)
	if err != nil || resolved != task.BaseRevision {
		return model.Run{}, OperationResult{}, fmt.Errorf("task base unavailable or mismatched")
	}
	id, err := model.NewID()
	if err != nil {
		return model.Run{}, OperationResult{}, err
	}
	now := time.Now().UTC()
	run := model.Run{SchemaVersion: model.SchemaVersion, ID: id, TaskID: task.ID, TaskSHA256: task.SHA256, ProjectID: task.ProjectID, GatewayID: s.Config.GatewayID, SessionKey: local.AirelaySessionKey, Branch: task.Branch, BaseRevision: task.BaseRevision, Status: "created", CompletionPath: filepath.Join(s.localRunDir(id), "completion.json"), CreatedAt: now}
	if err := model.ValidateRun(run); err != nil {
		return model.Run{}, OperationResult{}, err
	}
	if err := s.writeLocalRun(run, task); err != nil {
		return model.Run{}, OperationResult{}, err
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
		if err := ensureSessionAvailableInWorktree(w, run.SessionKey, s.Config.MaxReadBytes); err != nil {
			return nil, err
		}
		currentPlan.Revision++
		currentPlan.ActiveRunID = run.ID
		currentPlan.UpdatedBy = s.Config.GatewayID
		currentPlan.UpdatedAt = now
		currentState.Status = "dispatched"
		currentState.UpdatedAt = now
		paths := []string{s.runPath(run.ProjectID, run.ID), s.taskStatePath(task.ProjectID, task.ID), s.planPath(task.ProjectID)}
		vals := []any{run, currentState, currentPlan}
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
	if err := ensureOperationalRun(run); err != nil {
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
			return model.RepositoryProof{}, nil, fmt.Errorf("final project HEAD is not descended from task base")
		}
	} else {
		if published {
			proof, err = s.deriveMirrorRepositoryProof(ctx, run, project, publishedHead)
			if err != nil {
				return model.RepositoryProof{}, nil, err
			}
			if !proof.BaseAncestor {
				return model.RepositoryProof{}, nil, fmt.Errorf("published task branch is not descended from task base")
			}
		} else {
			addUniqueRisk(&risks, "published task branch was absent; canonical proof uses the immutable task base")
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
	if err := ensureOperationalRun(run); err != nil {
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
	if run.Historical && activeStatus(run.Status) {
		return TaskPacket{}, fmt.Errorf("workflow-v1 active run is history-only")
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
	text := renderPacket(task, run, project, plan, local.Root)
	return TaskPacket{Task: task, Run: run, Project: project, Plan: plan, RepositoryRoot: local.Root, CompletionPath: run.CompletionPath, FinalizeCommand: "gpt-tunnel run finalize " + run.ID, Text: text}, nil
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
	fmt.Fprintf(&b, "\n## Global plan context\n\n%s\n\nCurrent objective: %s\n\n## Completion contract\n\nBefore writing completion.json, commit the implementation, run every required gate, and push the task branch. Then write one strict completion JSON atomically to:\n  %s\n\nFinalize with:\n  gpt-tunnel run finalize %s\n\nThe task is not complete until finalization prints TASK_FINALIZED. Do not finish only in chat or Airelay.\n", plan.Summary, plan.CurrentObjective, run.CompletionPath, run.ID)
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

func (s *Service) RunFinalize(ctx context.Context, in FinalizeInput) (model.Report, OperationResult, error) {
	run, err := s.findRun(ctx, in.RunID)
	if err != nil {
		return model.Report{}, OperationResult{}, err
	}
	if run.Historical {
		return model.Report{}, OperationResult{}, fmt.Errorf("workflow-v1 run is history-only; canonical finalization is unavailable")
	}
	if err := s.ensureRunOwned(run); err != nil {
		return model.Report{}, OperationResult{}, err
	}
	if !activeStatus(run.Status) {
		return model.Report{}, OperationResult{}, fmt.Errorf("run is not active: %s", run.Status)
	}
	task, err := s.findTask(ctx, run.TaskID)
	if err != nil {
		return model.Report{}, OperationResult{}, err
	}
	completionPath, err := gatewayCompletionPath(run, in.CompletionFile)
	if err != nil {
		return model.Report{}, OperationResult{}, err
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
	if completion.RunID != strings.ToLower(run.ID) || completion.TaskSHA256 != run.TaskSHA256 {
		return model.Report{}, OperationResult{}, fmt.Errorf("completion identity does not match active run")
	}
	recomputed, err := model.HashTask(task)
	if err != nil || recomputed != task.SHA256 || run.TaskSHA256 != recomputed {
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
	report := canonicalReport(model.Report{SchemaVersion: model.SchemaVersion, TaskID: task.ID, RunID: run.ID, ProjectID: run.ProjectID, Status: completion.Status, Summary: completion.Summary, GateResults: completion.GateResults, AcceptanceCoverage: completion.AcceptanceCoverage, Deviations: completion.Deviations, RemainingRisks: remainingRisks, Repository: proof, FinishedAt: now})
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
	wantState := "ready"
	if report.Status == "succeeded" {
		wantState = "completed"
	}
	if state.Status != wantState {
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
	run, err := s.findRun(ctx, id)
	if err != nil {
		return OperationResult{}, err
	}
	if err := ensureOperationalRun(run); err != nil {
		return OperationResult{}, err
	}
	if err := s.ensureRunOwned(run); err != nil {
		return OperationResult{}, err
	}
	sessionLock, err := lockfile.Acquire(filepath.Join(s.Config.StateDir, "locks"), "session-"+run.SessionKey)
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
	if err := ensureOperationalRun(run); err != nil {
		return OperationResult{}, err
	}
	if !activeStatus(run.Status) {
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
			if run.GatewayID != s.Config.GatewayID || !activeStatus(run.Status) {
				continue
			}
			if run.Historical {
				out.Checked++
				out.Items = append(out.Items, SweepItem{RunID: run.ID, Action: "error", Status: run.Status, Error: "workflow-v1 run is history-only"})
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
