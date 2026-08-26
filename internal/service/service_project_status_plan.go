package service

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/airelay"
	"github.com/rceman/gpt-tunnel-gateway/internal/gitx"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s *Service) ProjectStatus(ctx context.Context, id string) (ProjectStatus, error) {
	local, err := s.projectConfig(id)
	if err != nil {
		return ProjectStatus{}, err
	}
	if enabled, enabledErr := s.trainV2Enabled(ctx, id); enabledErr == nil && enabled {
		return s.projectStatusTrainV2(ctx, id, local)
	} else if enabledErr != nil && s.Durability != nil {
		return ProjectStatus{}, enabledErr
	}
	componentCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	agentSession := local.AirelaySessionKey
	if resolved, resolveErr := s.resolveAgentSession(componentCtx, id); resolveErr == nil {
		agentSession = resolved
	}
	var (
		p                    = model.Project{SchemaVersion: model.SchemaVersion, ID: id, DefaultBranch: local.DefaultBranch, Status: "unknown"}
		projectErr           error
		wt                   gitx.WorktreeStatus
		wtErr                error
		workflowPolicy       model.ProjectWorkflowPolicy
		workflowPolicyErr    error
		tasks                []TaskRecord
		tasksErr             error
		hubRevision          string
		hubRevisionErr       error
		agentStatus          airelay.SessionStatus
		agentStatusErr       error
		agentTail            airelay.Result
		agentTailErr         error
		projectConfiguration ProjectConfigurationStatus
	)
	var wg sync.WaitGroup
	wg.Add(8)
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
		tasks, tasksErr = s.taskStatusList(componentCtx, id)
	}()
	go func() {
		defer wg.Done()
		hubRevision, hubRevisionErr = s.hubRevision(componentCtx)
	}()
	go func() {
		defer wg.Done()
		agentStatus, agentStatusErr = s.Airelay.Status(componentCtx, agentSession)
	}()
	go func() {
		defer wg.Done()
		agentTail, agentTailErr = s.Airelay.Tail(componentCtx, agentSession, progressTailLines)
	}()
	go func() {
		defer wg.Done()
		workflowPolicy, workflowPolicyErr = s.ProjectWorkflowPolicyRead(componentCtx, id)
	}()
	go func() {
		defer wg.Done()
		projectConfiguration = s.projectConfigurationStatus(componentCtx, id)
	}()
	wg.Wait()
	progress := projectProgressFromInputs(tasks, tasksErr, agentStatus, agentStatusErr, agentTail, agentTailErr)
	appendComponentError(&progress.ComponentErrors, "project", projectErr)
	appendComponentError(&progress.ComponentErrors, "worktree", wtErr)
	appendComponentError(&progress.ComponentErrors, "hub_revision", hubRevisionErr)
	appendComponentError(&progress.ComponentErrors, "workflow_policy", workflowPolicyErr)
	internalPaths := []string{s.Config.StateDir, local.Root, local.Mirror, local.AirelaySessionKey}
	for _, internal := range internalPaths {
		if internal != "" {
			progress.Tail = strings.ReplaceAll(progress.Tail, internal, "[gateway-internal-value]")
		}
	}
	sort.Strings(progress.ComponentErrors)
	return ProjectStatus{
		Project:              p,
		Local:                local,
		Worktree:             wt,
		Plan:                 retiredPlanStatus(id),
		HubRevision:          hubRevision,
		Progress:             progress,
		WorkflowPolicy:       workflowPolicyStatus(workflowPolicy, workflowPolicyErr, tasks),
		ProjectConfiguration: projectConfiguration,
	}, nil
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
	if err := rejectPlanMutationAfterTrainV2(ctx, s, in.ProjectID); err != nil {
		return OperationResult{}, err
	}
	if _, err := s.ProjectRead(ctx, in.ProjectID); err != nil {
		return OperationResult{}, err
	}
	if in.ActiveTaskID != nil && *in.ActiveTaskID != "" {
		if err := requireCanonicalTaskID(*in.ActiveTaskID); err != nil {
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
	return OperationResult{
		Hub:       tx,
		ProjectID: in.ProjectID,
		Status:    "updated",
	}, nil
}

func sectionIndex(plan model.Plan, id string) (int, model.PlanSectionIndex, error) {
	for i, section := range plan.Sections {
		if section.ID == id {
			return i, section, nil
		}
	}
	return -1, model.PlanSectionIndex{}, notFoundf("plan section %s", id)
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
