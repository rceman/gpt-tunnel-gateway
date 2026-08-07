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

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

type DeliveryHandoffCreateInput struct {
	ProjectID            string             `json:"project_id"`
	TaskID               string             `json:"task_id"`
	RunID                string             `json:"run_id"`
	TaskSHA256           string             `json:"task_sha256"`
	OwnerSummary         model.OwnerSummary `json:"owner_summary"`
	TechnicalEvidence    json.RawMessage    `json:"technical_evidence"`
	PlanRevision         int                `json:"plan_revision"`
	HubRevision          string             `json:"hub_revision"`
	TaskRefs             []model.TaskRef    `json:"task_refs"`
	TrainRefs            []string           `json:"train_refs"`
	PlanSectionRefs      []string           `json:"plan_section_refs"`
	OperatorEventRefs    []string           `json:"operator_event_refs"`
	ExpectedRepoBase     string             `json:"expected_repo_base"`
	ExpectedRepoHead     string             `json:"expected_repo_head"`
	FirstAction          string             `json:"first_action"`
	StopBoundary         string             `json:"stop_boundary"`
	ProhibitedOperations []string           `json:"prohibited_operations"`
	InstructionBody      string             `json:"instruction_body"`
	RoleRefs             []string           `json:"role_refs"`
	DelegationRefs       []string           `json:"delegation_refs"`
	CreatedBy            string             `json:"created_by"`
	SupersedesID         string             `json:"supersedes_handoff_id,omitempty"`
	WriteOptions
}

type DeliveryHandoffAcknowledgeInput struct {
	HandoffID      string `json:"handoff_id"`
	AcknowledgedBy string `json:"acknowledged_by"`
	WriteOptions
}

type DeliveryHandoffNextInput struct {
	HandoffID string `json:"handoff_id"`
	NextBy    string `json:"next_by"`
	WriteOptions
}

type DeliveryHandoffCancelInput struct {
	HandoffID   string `json:"handoff_id"`
	CancelledBy string `json:"cancelled_by"`
	Reason      string `json:"reason"`
	WriteOptions
}

type DeliveryHandoffSupersedeInput struct {
	HandoffID            string             `json:"handoff_id"`
	OwnerSummary         model.OwnerSummary `json:"owner_summary"`
	TechnicalEvidence    json.RawMessage    `json:"technical_evidence"`
	PlanRevision         int                `json:"plan_revision"`
	HubRevision          string             `json:"hub_revision"`
	TaskRefs             []model.TaskRef    `json:"task_refs"`
	TrainRefs            []string           `json:"train_refs"`
	PlanSectionRefs      []string           `json:"plan_section_refs"`
	OperatorEventRefs    []string           `json:"operator_event_refs"`
	ExpectedRepoBase     string             `json:"expected_repo_base"`
	ExpectedRepoHead     string             `json:"expected_repo_head"`
	FirstAction          string             `json:"first_action"`
	StopBoundary         string             `json:"stop_boundary"`
	ProhibitedOperations []string           `json:"prohibited_operations"`
	InstructionBody      string             `json:"instruction_body"`
	RoleRefs             []string           `json:"role_refs"`
	DelegationRefs       []string           `json:"delegation_refs"`
	CreatedBy            string             `json:"created_by"`
	WriteOptions
}

type PlannerReportPublishInput struct {
	HandoffID string              `json:"handoff_id"`
	Report    model.PlannerReport `json:"report"`
	WriteOptions
}

type PlannerReportAcknowledgeInput struct {
	ReportID       string `json:"report_id"`
	AcknowledgedBy string `json:"acknowledged_by"`
	WriteOptions
}

type PlannerReportNextInput struct {
	ReportID   string `json:"report_id"`
	ResolvedBy string `json:"resolved_by"`
	WriteOptions
}

func (s *Service) deliveryHandoffPrefix(project string) string {
	if model.ValidateProjectIdentifier(project) != nil {
		return "../invalid-delivery-handoff"
	}
	return s.projectPrefix(project) + "/delivery-handoffs"
}

func (s *Service) deliveryHandoffPath(project, id string) string {
	if model.ValidateProjectIdentifier(project) != nil || model.ValidateObjectIdentifier(id) != nil {
		return "../invalid-delivery-handoff"
	}
	return s.deliveryHandoffPrefix(project) + "/" + id + ".json"
}

func (s *Service) plannerReportPrefix(project string) string {
	if model.ValidateProjectIdentifier(project) != nil {
		return "../invalid-planner-report"
	}
	return s.projectPrefix(project) + "/planner-reports"
}

func (s *Service) plannerReportPath(project, id string) string {
	if model.ValidateProjectIdentifier(project) != nil || model.ValidateObjectIdentifier(id) != nil {
		return "../invalid-planner-report"
	}
	return s.plannerReportPrefix(project) + "/" + id + ".json"
}

func (s *Service) plannerReportStatePath(project, id string) string {
	if model.ValidateProjectIdentifier(project) != nil || model.ValidateObjectIdentifier(id) != nil {
		return "../invalid-planner-report-state"
	}
	return s.plannerReportPrefix(project) + "/" + id + ".state.json"
}

func newDurableRecordID() (string, error) {
	id, err := model.NewID()
	if err != nil {
		return "", err
	}
	if err := model.ValidateObjectIdentifier(id); err != nil {
		return "", err
	}
	return id, nil
}

func validateHandoffSummaryAndEvidence(summary model.OwnerSummary, evidence json.RawMessage) error {
	if err := model.ValidateOwnerSummary(summary); err != nil {
		return err
	}
	return model.ValidateTechnicalEvidence(evidence)
}

func handoffJournalInput(handoff model.DeliveryHandoff, reportID string, extraIdentities []string, summary, actor string) OperatorRecordInput {
	identities := []string{handoff.ID}
	identities = append(identities, extraIdentities...)
	if reportID != "" {
		identities = append(identities, reportID)
	}
	return OperatorRecordInput{ProjectID: handoff.ProjectID, Kind: model.OperatorOperation, Summary: summary, Content: model.OperatorJournalContent{Facts: []string{summary}}, References: model.OperatorJournalReferences{PlanSections: append([]string(nil), handoff.PlanSectionRefs...), Tasks: []string{handoff.TaskID}, Runs: []string{handoff.RunID}, Identities: identities}, Actor: actor}
}

func (s *Service) validateHandoffReferences(ctx context.Context, projectID, taskID, runID, taskSHA string) (model.Task, model.Run, error) {
	if err := model.ValidateProjectIdentifier(projectID); err != nil {
		return model.Task{}, model.Run{}, err
	}
	task, err := s.findTask(ctx, taskID)
	if err != nil {
		return model.Task{}, model.Run{}, err
	}
	run, err := s.findRun(ctx, runID)
	if err != nil {
		return model.Task{}, model.Run{}, err
	}
	if task.ProjectID != projectID || run.ProjectID != projectID || run.TaskID != task.ID {
		return model.Task{}, model.Run{}, fmt.Errorf("handoff task/run ownership mismatch")
	}
	if err := model.ValidateTask(task); err != nil {
		return model.Task{}, model.Run{}, err
	}
	if err := model.ValidateRun(run); err != nil {
		return model.Task{}, model.Run{}, err
	}
	if taskSHA != "" && taskSHA != task.SHA256 {
		return model.Task{}, model.Run{}, fmt.Errorf("handoff task hash mismatch")
	}
	if run.TaskSHA256 != task.SHA256 {
		return model.Task{}, model.Run{}, fmt.Errorf("handoff run task hash mismatch")
	}
	return task, run, nil
}

func (s *Service) validateTaskRefsAgainstDurable(ctx context.Context, projectID, primaryTaskID, primarySHA string, refs []model.TaskRef) error {
	foundPrimary := false
	for _, ref := range refs {
		task, err := s.findTask(ctx, ref.TaskID)
		if err != nil {
			return fmt.Errorf("task reference %s: %w", ref.TaskID, err)
		}
		if task.ProjectID != projectID || task.SHA256 != ref.TaskSHA256 {
			return fmt.Errorf("task reference %s does not match durable task", ref.TaskID)
		}
		if ref.TaskID == primaryTaskID && ref.TaskSHA256 == primarySHA {
			foundPrimary = true
		}
	}
	if !foundPrimary {
		return fmt.Errorf("task_refs must include the handoff task and exact hash")
	}
	return nil
}

func (s *Service) validateCandidatePlanAuthority(ctx context.Context, projectID string, planRevision int, hubRevision, expectedHubRevision string) error {
	plan, err := s.PlanRead(ctx, projectID)
	if err != nil {
		return fmt.Errorf("read current durable plan: %w", err)
	}
	if plan.Revision != planRevision {
		return fmt.Errorf("handoff plan revision %d is stale; current plan revision is %d", planRevision, plan.Revision)
	}
	if strings.TrimSpace(expectedHubRevision) == "" || hubRevision != expectedHubRevision {
		return fmt.Errorf("handoff hub revision must equal expected hub revision")
	}
	currentHubRevision, err := s.Hub.RemoteRevision(ctx)
	if err != nil {
		return err
	}
	if currentHubRevision != expectedHubRevision {
		return fmt.Errorf("expected hub revision is stale")
	}
	return nil
}

func (s *Service) validateHandoffPlanAndJournalRefs(ctx context.Context, projectID string, planSectionRefs, operatorEventRefs []string) error {
	if len(planSectionRefs) > 0 {
		plan, err := s.PlanRead(ctx, projectID)
		if err != nil {
			return fmt.Errorf("read current plan sections: %w", err)
		}
		known := make(map[string]bool, len(plan.Sections))
		for _, section := range plan.Sections {
			known[section.ID] = true
		}
		for _, ref := range planSectionRefs {
			if !known[ref] {
				return fmt.Errorf("plan section reference %q is missing", ref)
			}
		}
	}
	for _, eventID := range operatorEventRefs {
		var event model.OperatorJournalEvent
		if err := s.Hub.ReadJSON(ctx, s.operatorHistoryEventPath(projectID, eventID), &event); err != nil {
			return fmt.Errorf("operator event reference %q is missing: %w", eventID, err)
		}
		if err := model.ValidateOperatorJournalEvent(event); err != nil || event.ProjectID != projectID || event.ID != eventID {
			return fmt.Errorf("operator event reference %q is invalid", eventID)
		}
	}
	return nil
}

func (s *Service) validateHandoffPlanAuthority(ctx context.Context, handoff model.DeliveryHandoff, expectedHubRevision string) error {
	if strings.TrimSpace(expectedHubRevision) == "" {
		return fmt.Errorf("expected hub revision is required")
	}
	if handoff.HubRevision != expectedHubRevision {
		return fmt.Errorf("handoff hub revision is not the transaction revision")
	}
	return nil
}

func (s *Service) DeliveryHandoffCreate(ctx context.Context, in DeliveryHandoffCreateInput) (model.DeliveryHandoff, OperationResult, error) {
	if err := authority.RequirePlanner(ctx); err != nil {
		return model.DeliveryHandoff{}, OperationResult{}, err
	}
	if err := validateHandoffSummaryAndEvidence(in.OwnerSummary, in.TechnicalEvidence); err != nil {
		return model.DeliveryHandoff{}, OperationResult{}, err
	}
	if strings.TrimSpace(in.CreatedBy) == "" {
		return model.DeliveryHandoff{}, OperationResult{}, fmt.Errorf("created_by is required")
	}
	task, run, err := s.validateHandoffReferences(ctx, in.ProjectID, in.TaskID, in.RunID, in.TaskSHA256)
	if err != nil {
		return model.DeliveryHandoff{}, OperationResult{}, err
	}
	if err := s.validateCandidatePlanAuthority(ctx, in.ProjectID, in.PlanRevision, in.HubRevision, in.ExpectedHubRevision); err != nil {
		return model.DeliveryHandoff{}, OperationResult{}, err
	}
	if err := s.validateTaskRefsAgainstDurable(ctx, in.ProjectID, task.ID, task.SHA256, in.TaskRefs); err != nil {
		return model.DeliveryHandoff{}, OperationResult{}, err
	}
	if in.SupersedesID != "" {
		old, readErr := s.deliveryHandoffReadInProject(ctx, in.ProjectID, in.SupersedesID)
		if readErr != nil {
			return model.DeliveryHandoff{}, OperationResult{}, readErr
		}
		if old.Status == model.DeliveryHandoffCompleted || old.Status == model.DeliveryHandoffCancelled || old.Status == model.DeliveryHandoffSuperseded {
			return model.DeliveryHandoff{}, OperationResult{}, fmt.Errorf("handoff cannot supersede terminal status %q", old.Status)
		}
	}
	id, err := newDurableRecordID()
	if err != nil {
		return model.DeliveryHandoff{}, OperationResult{}, err
	}
	now := time.Now().UTC()
	handoff := model.DeliveryHandoff{SchemaVersion: model.DurableHandoffSchemaVersion, ID: id, ProjectID: in.ProjectID, TaskID: task.ID, RunID: run.ID, TaskSHA256: task.SHA256, Status: model.DeliveryHandoffPending, OwnerSummary: in.OwnerSummary, TechnicalEvidence: append(json.RawMessage(nil), in.TechnicalEvidence...), SupersedesHandoffID: in.SupersedesID, PlanRevision: in.PlanRevision, HubRevision: in.HubRevision, TaskRefs: append([]model.TaskRef(nil), in.TaskRefs...), TrainRefs: append([]string(nil), in.TrainRefs...), PlanSectionRefs: append([]string(nil), in.PlanSectionRefs...), OperatorEventRefs: append([]string(nil), in.OperatorEventRefs...), ExpectedRepoBase: in.ExpectedRepoBase, ExpectedRepoHead: in.ExpectedRepoHead, FirstAction: in.FirstAction, StopBoundary: in.StopBoundary, ProhibitedOperations: append([]string(nil), in.ProhibitedOperations...), InstructionBody: in.InstructionBody, RoleRefs: append([]string(nil), in.RoleRefs...), DelegationRefs: append([]string(nil), in.DelegationRefs...), AuthorRole: "planner", ConsumerRole: "delivery", CreatedBy: in.CreatedBy, CreatedAt: now, UpdatedAt: now}
	handoff.CanonicalDigest, err = model.CanonicalDeliveryHandoffDigest(handoff)
	if err != nil {
		return model.DeliveryHandoff{}, OperationResult{}, err
	}
	if err := model.ValidateDeliveryHandoff(handoff); err != nil {
		return model.DeliveryHandoff{}, OperationResult{}, err
	}
	path := s.deliveryHandoffPath(in.ProjectID, id)
	tx, err := s.Hub.Transact(ctx, in.ExpectedHubRevision, "planner: create delivery handoff "+id, func(worktree string) ([]string, error) {
		changed := make([]string, 0, 3)
		if _, statErr := os.Lstat(filepath.Join(worktree, filepath.FromSlash(path))); statErr == nil {
			return nil, fmt.Errorf("delivery handoff already exists")
		} else if !os.IsNotExist(statErr) {
			return nil, statErr
		}
		if err := hub.WriteJSON(worktree, path, handoff); err != nil {
			return nil, err
		}
		_, journalPaths, err := s.appendOperatorEventInWorktree(worktree, handoffJournalInput(handoff, "", nil, "delivery handoff created", "planner"))
		if err != nil {
			return nil, err
		}
		changed = append(changed, path)
		changed = append(changed, journalPaths...)
		return changed, nil
	})
	if err != nil {
		return model.DeliveryHandoff{}, OperationResult{}, err
	}
	return handoff, OperationResult{Hub: tx, ProjectID: handoff.ProjectID, Status: handoff.Status}, nil
}

func (s *Service) deliveryHandoffReadInProject(ctx context.Context, projectID, id string) (model.DeliveryHandoff, error) {
	var handoff model.DeliveryHandoff
	if err := s.Hub.ReadJSON(ctx, s.deliveryHandoffPath(projectID, id), &handoff); err != nil {
		return model.DeliveryHandoff{}, err
	}
	if err := model.ValidateDeliveryHandoff(handoff); err != nil {
		return model.DeliveryHandoff{}, err
	}
	return handoff, nil
}

func (s *Service) findDeliveryHandoff(ctx context.Context, id string) (model.DeliveryHandoff, error) {
	projects, err := s.ProjectList(ctx)
	if err != nil {
		return model.DeliveryHandoff{}, err
	}
	for _, project := range projects {
		handoff, readErr := s.deliveryHandoffReadInProject(ctx, project.ID, id)
		if readErr == nil {
			return handoff, nil
		}
		if !IsNotFound(readErr) {
			return model.DeliveryHandoff{}, readErr
		}
	}
	return model.DeliveryHandoff{}, fmt.Errorf("delivery handoff not found: %s", id)
}

func (s *Service) DeliveryHandoffRead(ctx context.Context, id string) (model.DeliveryHandoff, error) {
	return s.findDeliveryHandoff(ctx, id)
}

func deliveryHandoffStatusProjection(item model.DeliveryHandoff) model.DeliveryHandoffStatus {
	return model.DeliveryHandoffStatus{SchemaVersion: item.SchemaVersion, ID: item.ID, ProjectID: item.ProjectID, TaskID: item.TaskID, RunID: item.RunID, Status: item.Status, OwnerSummary: item.OwnerSummary, CurrentReportID: item.CurrentReportID, SupersedesHandoffID: item.SupersedesHandoffID, SupersededByHandoffID: item.SupersededByHandoffID, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt}
}

func (s *Service) DeliveryHandoffStatus(ctx context.Context, id string) (model.DeliveryHandoffStatus, error) {
	handoff, err := s.findDeliveryHandoff(ctx, id)
	if err != nil {
		return model.DeliveryHandoffStatus{}, err
	}
	return deliveryHandoffStatusProjection(handoff), nil
}

func (s *Service) DeliveryHandoffList(ctx context.Context, projectID string) ([]model.DeliveryHandoffStatus, error) {
	paths, err := s.Hub.List(ctx, s.deliveryHandoffPrefix(projectID), ".json")
	if err != nil {
		return nil, err
	}
	items := make([]model.DeliveryHandoffStatus, 0, len(paths))
	for _, path := range paths {
		var item model.DeliveryHandoff
		if err := s.Hub.ReadJSON(ctx, path, &item); err != nil {
			return nil, err
		}
		if err := model.ValidateDeliveryHandoff(item); err != nil {
			return nil, err
		}
		items = append(items, deliveryHandoffStatusProjection(item))
	}
	sort.Slice(items, func(i, j int) bool { return items[i].UpdatedAt.After(items[j].UpdatedAt) })
	return items, nil
}

func (s *Service) DeliveryHandoffAcknowledge(ctx context.Context, in DeliveryHandoffAcknowledgeInput) (model.DeliveryHandoff, OperationResult, error) {
	if err := authority.RequireDelivery(ctx); err != nil {
		return model.DeliveryHandoff{}, OperationResult{}, err
	}
	current, err := s.findDeliveryHandoff(ctx, in.HandoffID)
	if err != nil {
		return model.DeliveryHandoff{}, OperationResult{}, err
	}
	if current.Status != model.DeliveryHandoffPending {
		return model.DeliveryHandoff{}, OperationResult{}, fmt.Errorf("handoff cannot be acknowledged from %q", current.Status)
	}
	if strings.TrimSpace(in.AcknowledgedBy) == "" {
		return model.DeliveryHandoff{}, OperationResult{}, fmt.Errorf("acknowledged_by is required")
	}
	path := s.deliveryHandoffPath(current.ProjectID, current.ID)
	now := time.Now().UTC()
	next := current
	next.Status = model.DeliveryHandoffAcknowledged
	next.AcknowledgedBy = in.AcknowledgedBy
	next.AcknowledgedAt = &now
	next.UpdatedAt = now
	tx, err := s.Hub.Transact(ctx, in.ExpectedHubRevision, "delivery: acknowledge handoff "+current.ID, func(worktree string) ([]string, error) {
		var stored model.DeliveryHandoff
		if err := readWorktreeJSON(worktree, path, &stored); err != nil {
			return nil, err
		}
		if err := model.ValidateDeliveryHandoff(stored); err != nil {
			return nil, err
		}
		if stored.Status != model.DeliveryHandoffPending || stored.ID != current.ID {
			return nil, fmt.Errorf("handoff changed before acknowledgement")
		}
		if err := hub.WriteJSON(worktree, path, next); err != nil {
			return nil, err
		}
		_, journalPaths, err := s.appendOperatorEventInWorktree(worktree, handoffJournalInput(next, "", nil, "delivery handoff acknowledged", "delivery"))
		if err != nil {
			return nil, err
		}
		return append([]string{path}, journalPaths...), nil
	})
	if err != nil {
		return model.DeliveryHandoff{}, OperationResult{}, err
	}
	return next, OperationResult{Hub: tx, ProjectID: next.ProjectID, Status: next.Status}, nil
}

// DeliveryHandoffNext claims the exact acknowledged handoff for Delivery and
// advances it into the only state from which a report may be published.
func (s *Service) DeliveryHandoffNext(ctx context.Context, in DeliveryHandoffNextInput) (model.DeliveryHandoff, OperationResult, error) {
	if err := authority.RequireDelivery(ctx); err != nil {
		return model.DeliveryHandoff{}, OperationResult{}, err
	}
	if strings.TrimSpace(in.NextBy) == "" {
		return model.DeliveryHandoff{}, OperationResult{}, fmt.Errorf("next_by is required")
	}
	current, err := s.findDeliveryHandoff(ctx, in.HandoffID)
	if err != nil {
		return model.DeliveryHandoff{}, OperationResult{}, err
	}
	if current.Status != model.DeliveryHandoffAcknowledged {
		return model.DeliveryHandoff{}, OperationResult{}, fmt.Errorf("handoff cannot advance from %q", current.Status)
	}
	path := s.deliveryHandoffPath(current.ProjectID, current.ID)
	now := time.Now().UTC()
	next := current
	next.Status = model.DeliveryHandoffInProgress
	next.StartedBy = in.NextBy
	next.StartedAt = &now
	next.UpdatedAt = now
	tx, err := s.Hub.Transact(ctx, in.ExpectedHubRevision, "delivery: advance handoff "+current.ID, func(worktree string) ([]string, error) {
		var stored model.DeliveryHandoff
		if err := readWorktreeJSON(worktree, path, &stored); err != nil {
			return nil, err
		}
		if err := model.ValidateDeliveryHandoff(stored); err != nil {
			return nil, err
		}
		if stored.Status != model.DeliveryHandoffAcknowledged || stored.UpdatedAt != current.UpdatedAt {
			return nil, fmt.Errorf("handoff changed before advancement")
		}
		if err := hub.WriteJSON(worktree, path, next); err != nil {
			return nil, err
		}
		_, journalPaths, err := s.appendOperatorEventInWorktree(worktree, handoffJournalInput(next, "", nil, "delivery handoff advanced to in_progress", "delivery"))
		if err != nil {
			return nil, err
		}
		return append([]string{path}, journalPaths...), nil
	})
	if err != nil {
		return model.DeliveryHandoff{}, OperationResult{}, err
	}
	return next, OperationResult{Hub: tx, ProjectID: next.ProjectID, Status: next.Status}, nil
}

func (s *Service) DeliveryHandoffCancel(ctx context.Context, in DeliveryHandoffCancelInput) (model.DeliveryHandoff, OperationResult, error) {
	if err := authority.RequirePlanner(ctx); err != nil {
		return model.DeliveryHandoff{}, OperationResult{}, err
	}
	if strings.TrimSpace(in.CancelledBy) == "" || strings.TrimSpace(in.Reason) == "" {
		return model.DeliveryHandoff{}, OperationResult{}, fmt.Errorf("cancelled_by and reason are required")
	}
	current, err := s.findDeliveryHandoff(ctx, in.HandoffID)
	if err != nil {
		return model.DeliveryHandoff{}, OperationResult{}, err
	}
	if current.Status == model.DeliveryHandoffCompleted || current.Status == model.DeliveryHandoffCancelled || current.Status == model.DeliveryHandoffSuperseded {
		return model.DeliveryHandoff{}, OperationResult{}, fmt.Errorf("handoff cannot be cancelled from %q", current.Status)
	}
	path := s.deliveryHandoffPath(current.ProjectID, current.ID)
	now := time.Now().UTC()
	next := current
	next.Status = model.DeliveryHandoffCancelled
	next.CancelledBy = in.CancelledBy
	next.CancelReason = in.Reason
	next.CancelledAt = &now
	next.UpdatedAt = now
	tx, err := s.Hub.Transact(ctx, in.ExpectedHubRevision, "planner: cancel handoff "+current.ID, func(worktree string) ([]string, error) {
		var stored model.DeliveryHandoff
		if err := readWorktreeJSON(worktree, path, &stored); err != nil {
			return nil, err
		}
		if err := model.ValidateDeliveryHandoff(stored); err != nil {
			return nil, err
		}
		if stored.Status != current.Status || stored.UpdatedAt != current.UpdatedAt {
			return nil, fmt.Errorf("handoff changed before cancellation")
		}
		if err := hub.WriteJSON(worktree, path, next); err != nil {
			return nil, err
		}
		_, journalPaths, err := s.appendOperatorEventInWorktree(worktree, handoffJournalInput(next, "", nil, "delivery handoff cancelled", "planner"))
		if err != nil {
			return nil, err
		}
		return append([]string{path}, journalPaths...), nil
	})
	if err != nil {
		return model.DeliveryHandoff{}, OperationResult{}, err
	}
	return next, OperationResult{Hub: tx, ProjectID: next.ProjectID, Status: next.Status}, nil
}

func (s *Service) DeliveryHandoffSupersede(ctx context.Context, in DeliveryHandoffSupersedeInput) (model.DeliveryHandoff, OperationResult, error) {
	if err := authority.RequirePlanner(ctx); err != nil {
		return model.DeliveryHandoff{}, OperationResult{}, err
	}
	if err := validateHandoffSummaryAndEvidence(in.OwnerSummary, in.TechnicalEvidence); err != nil {
		return model.DeliveryHandoff{}, OperationResult{}, err
	}
	if strings.TrimSpace(in.CreatedBy) == "" {
		return model.DeliveryHandoff{}, OperationResult{}, fmt.Errorf("created_by is required")
	}
	old, err := s.findDeliveryHandoff(ctx, in.HandoffID)
	if err != nil {
		return model.DeliveryHandoff{}, OperationResult{}, err
	}
	if err := s.validateCandidatePlanAuthority(ctx, old.ProjectID, in.PlanRevision, in.HubRevision, in.ExpectedHubRevision); err != nil {
		return model.DeliveryHandoff{}, OperationResult{}, err
	}
	if err := s.validateTaskRefsAgainstDurable(ctx, old.ProjectID, old.TaskID, old.TaskSHA256, in.TaskRefs); err != nil {
		return model.DeliveryHandoff{}, OperationResult{}, err
	}
	if err := s.validateHandoffPlanAndJournalRefs(ctx, old.ProjectID, in.PlanSectionRefs, in.OperatorEventRefs); err != nil {
		return model.DeliveryHandoff{}, OperationResult{}, err
	}
	if old.Status == model.DeliveryHandoffCompleted || old.Status == model.DeliveryHandoffCancelled || old.Status == model.DeliveryHandoffSuperseded {
		return model.DeliveryHandoff{}, OperationResult{}, fmt.Errorf("handoff cannot be superseded from %q", old.Status)
	}
	id, err := newDurableRecordID()
	if err != nil {
		return model.DeliveryHandoff{}, OperationResult{}, err
	}
	now := time.Now().UTC()
	next := model.DeliveryHandoff{SchemaVersion: model.DurableHandoffSchemaVersion, ID: id, ProjectID: old.ProjectID, TaskID: old.TaskID, RunID: old.RunID, TaskSHA256: old.TaskSHA256, Status: model.DeliveryHandoffPending, OwnerSummary: in.OwnerSummary, TechnicalEvidence: append(json.RawMessage(nil), in.TechnicalEvidence...), SupersedesHandoffID: old.ID, PlanRevision: in.PlanRevision, HubRevision: in.HubRevision, TaskRefs: append([]model.TaskRef(nil), in.TaskRefs...), TrainRefs: append([]string(nil), in.TrainRefs...), PlanSectionRefs: append([]string(nil), in.PlanSectionRefs...), OperatorEventRefs: append([]string(nil), in.OperatorEventRefs...), ExpectedRepoBase: in.ExpectedRepoBase, ExpectedRepoHead: in.ExpectedRepoHead, FirstAction: in.FirstAction, StopBoundary: in.StopBoundary, ProhibitedOperations: append([]string(nil), in.ProhibitedOperations...), InstructionBody: in.InstructionBody, RoleRefs: append([]string(nil), in.RoleRefs...), DelegationRefs: append([]string(nil), in.DelegationRefs...), AuthorRole: "planner", ConsumerRole: "delivery", CreatedBy: in.CreatedBy, CreatedAt: now, UpdatedAt: now}
	next.CanonicalDigest, err = model.CanonicalDeliveryHandoffDigest(next)
	if err != nil {
		return model.DeliveryHandoff{}, OperationResult{}, err
	}
	if err := model.ValidateDeliveryHandoff(next); err != nil {
		return model.DeliveryHandoff{}, OperationResult{}, err
	}
	oldPath, nextPath := s.deliveryHandoffPath(old.ProjectID, old.ID), s.deliveryHandoffPath(old.ProjectID, id)
	oldNext := old
	oldNext.Status = model.DeliveryHandoffSuperseded
	oldNext.SupersededByHandoffID = id
	oldNext.UpdatedAt = now
	tx, err := s.Hub.Transact(ctx, in.ExpectedHubRevision, "planner: supersede delivery handoff "+old.ID, func(worktree string) ([]string, error) {
		var stored model.DeliveryHandoff
		if err := readWorktreeJSON(worktree, oldPath, &stored); err != nil {
			return nil, err
		}
		if err := model.ValidateDeliveryHandoff(stored); err != nil {
			return nil, err
		}
		if stored.Status != old.Status || stored.UpdatedAt != old.UpdatedAt {
			return nil, fmt.Errorf("handoff changed before supersession")
		}
		if _, statErr := os.Lstat(filepath.Join(worktree, filepath.FromSlash(nextPath))); statErr == nil {
			return nil, fmt.Errorf("replacement handoff already exists")
		} else if !os.IsNotExist(statErr) {
			return nil, statErr
		}
		if err := hub.WriteJSON(worktree, oldPath, oldNext); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, nextPath, next); err != nil {
			return nil, err
		}
		_, journalPaths, err := s.appendOperatorEventInWorktree(worktree, handoffJournalInput(next, "", []string{old.ID}, "delivery handoff superseded", "planner"))
		if err != nil {
			return nil, err
		}
		return append([]string{oldPath, nextPath}, journalPaths...), nil
	})
	if err != nil {
		return model.DeliveryHandoff{}, OperationResult{}, err
	}
	return next, OperationResult{Hub: tx, ProjectID: next.ProjectID, Status: next.Status}, nil
}

func evidenceString(evidence map[string]json.RawMessage, key string) (string, error) {
	var value string
	if err := json.Unmarshal(evidence[key], &value); err != nil || strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("technical_evidence.%s is required", key)
	}
	return value, nil
}

func (s *Service) validateCompletedDeliveryProof(ctx context.Context, handoff model.DeliveryHandoff, evidence json.RawMessage) (model.Task, model.Run, model.Report, model.RunReviewReport, error) {
	task, run, err := s.validateHandoffReferences(ctx, handoff.ProjectID, handoff.TaskID, handoff.RunID, handoff.TaskSHA256)
	if err != nil {
		return model.Task{}, model.Run{}, model.Report{}, model.RunReviewReport{}, err
	}
	if run.Historical || operationalActiveRun(run) || run.Status != "succeeded" {
		return model.Task{}, model.Run{}, model.Report{}, model.RunReviewReport{}, fmt.Errorf("completed handoff requires a terminal successful operational run")
	}
	agent, err := s.RunReport(ctx, run.ID)
	if err != nil {
		return model.Task{}, model.Run{}, model.Report{}, model.RunReviewReport{}, fmt.Errorf("read immutable Agent report: %w", err)
	}
	if agent.Status != "succeeded" || agent.TaskID != task.ID || agent.RunID != run.ID || agent.ProjectID != task.ProjectID || agent.Repository.Branch != run.Branch || agent.Repository.DiffScope != run.BaseRevision+".."+agent.Repository.Head {
		return model.Task{}, model.Run{}, model.Report{}, model.RunReviewReport{}, fmt.Errorf("immutable Agent report does not prove completed work")
	}
	delivery, err := s.readFinalReviewReport(ctx, task, run)
	if err != nil {
		return model.Task{}, model.Run{}, model.Report{}, model.RunReviewReport{}, fmt.Errorf("read immutable Delivery report: %w", err)
	}
	if err := model.ValidateRunReviewReport(delivery); err != nil {
		return model.Task{}, model.Run{}, model.Report{}, model.RunReviewReport{}, fmt.Errorf("immutable Delivery report is invalid: %w", err)
	}
	if delivery.Outcome != model.ReviewOutcomeAccepted || delivery.ReviewedHead != agent.Repository.Head || delivery.TaskSHA256 != task.SHA256 || delivery.Branch != run.Branch || delivery.BaseRevision != run.BaseRevision {
		return model.Task{}, model.Run{}, model.Report{}, model.RunReviewReport{}, fmt.Errorf("immutable Delivery report does not prove accepted reviewed work")
	}
	state, err := s.taskState(ctx, task)
	if err != nil {
		return model.Task{}, model.Run{}, model.Report{}, model.RunReviewReport{}, err
	}
	switch state.Status {
	case "completed", "merge_ready", "merged":
	default:
		return model.Task{}, model.Run{}, model.Report{}, model.RunReviewReport{}, fmt.Errorf("task state %q does not prove completed work", state.Status)
	}
	var proof map[string]json.RawMessage
	if err := json.Unmarshal(evidence, &proof); err != nil {
		return model.Task{}, model.Run{}, model.Report{}, model.RunReviewReport{}, fmt.Errorf("technical evidence proof is invalid")
	}
	if err := model.PlannerReportRequiresTerminalEvidence(model.PlannerReport{ReportType: model.PlannerReportCompleted, TechnicalEvidence: evidence}); err != nil {
		return model.Task{}, model.Run{}, model.Report{}, model.RunReviewReport{}, err
	}
	if value, err := evidenceString(proof, "task_sha256"); err != nil || value != task.SHA256 {
		return model.Task{}, model.Run{}, model.Report{}, model.RunReviewReport{}, fmt.Errorf("technical evidence task hash does not match immutable task")
	}
	if value, err := evidenceString(proof, "run_id"); err != nil || value != run.ID {
		return model.Task{}, model.Run{}, model.Report{}, model.RunReviewReport{}, fmt.Errorf("technical evidence run does not match immutable run")
	}
	if value, err := evidenceString(proof, "delivery_report_id"); err != nil || value != delivery.ID {
		return model.Task{}, model.Run{}, model.Report{}, model.RunReviewReport{}, fmt.Errorf("technical evidence Delivery report does not match immutable report")
	}
	if value, err := evidenceString(proof, "reviewed_head"); err != nil || value != delivery.ReviewedHead {
		return model.Task{}, model.Run{}, model.Report{}, model.RunReviewReport{}, fmt.Errorf("technical evidence reviewed head does not match immutable report")
	}
	return task, run, agent, delivery, nil
}

func validateCompletedDeliveryProofInWorktree(worktree string, s *Service, handoff model.DeliveryHandoff, expectedTask model.Task, expectedRun model.Run, expectedAgent model.Report, expectedDelivery model.RunReviewReport) error {
	var task model.Task
	if err := readWorktreeJSON(worktree, s.taskPath(handoff.ProjectID, handoff.TaskID), &task); err != nil {
		return fmt.Errorf("task changed before completed report: %w", err)
	}
	if err := model.ValidateTask(task); err != nil || task.ID != expectedTask.ID || task.SHA256 != expectedTask.SHA256 || task.ProjectID != expectedTask.ProjectID || task.Branch != expectedTask.Branch || task.BaseRevision != expectedTask.BaseRevision {
		return fmt.Errorf("task changed before completed report")
	}
	if err := model.ValidateTaskHash(task); err != nil {
		return fmt.Errorf("task hash changed before completed report")
	}
	var run model.Run
	if err := readWorktreeJSON(worktree, s.runPath(handoff.ProjectID, handoff.RunID), &run); err != nil {
		return fmt.Errorf("run changed before completed report: %w", err)
	}
	if err := model.ValidateRun(run); err != nil || run.ID != expectedRun.ID || run.TaskID != expectedRun.TaskID || run.ProjectID != expectedRun.ProjectID || run.TaskSHA256 != expectedRun.TaskSHA256 || run.Branch != expectedRun.Branch || run.BaseRevision != expectedRun.BaseRevision || run.Status != "succeeded" || operationalActiveRun(run) {
		return fmt.Errorf("run changed before completed report")
	}
	var agent model.Report
	if err := readWorktreeJSON(worktree, s.reportPath(handoff.ProjectID, handoff.RunID), &agent); err != nil {
		return fmt.Errorf("Agent report changed before completed report: %w", err)
	}
	if err := model.ValidateReport(agent, task, run, s.Config.MaxListItems); err != nil || agent.Status != "succeeded" || !sameAgentAuthority(agent, expectedAgent) {
		return fmt.Errorf("Agent report changed before completed report")
	}
	data, err := os.ReadFile(filepath.Join(worktree, filepath.FromSlash(s.reviewReportPath(handoff.ProjectID, handoff.RunID))))
	if err != nil {
		return fmt.Errorf("Delivery report changed before completed report: %w", err)
	}
	delivery, err := model.ParseRunReviewReport(data)
	if err != nil || model.ValidateRunReviewReport(delivery) != nil {
		return fmt.Errorf("Delivery report changed before completed report")
	}
	if delivery.ID != expectedDelivery.ID || delivery.TaskID != task.ID || delivery.RunID != run.ID || delivery.ProjectID != task.ProjectID || delivery.TaskSHA256 != task.SHA256 || delivery.Branch != run.Branch || delivery.BaseRevision != run.BaseRevision || delivery.Outcome != model.ReviewOutcomeAccepted || delivery.ReviewedHead != agent.Repository.Head {
		return fmt.Errorf("Delivery report proof changed before completed report")
	}
	var state model.TaskState
	if err := readWorktreeJSON(worktree, s.taskStatePath(handoff.ProjectID, handoff.TaskID), &state); err != nil {
		return fmt.Errorf("task state changed before completed report: %w", err)
	}
	if err := model.ValidateTaskState(state, task); err != nil {
		return fmt.Errorf("task state changed before completed report")
	}
	switch state.Status {
	case "completed", "merge_ready", "merged":
	default:
		return fmt.Errorf("task state changed before completed report")
	}
	return nil
}

func (s *Service) PlannerReportPublish(ctx context.Context, in PlannerReportPublishInput) (model.PlannerReport, OperationResult, error) {
	if err := authority.RequireDelivery(ctx); err != nil {
		return model.PlannerReport{}, OperationResult{}, err
	}
	handoff, err := s.findDeliveryHandoff(ctx, in.HandoffID)
	if err != nil {
		return model.PlannerReport{}, OperationResult{}, err
	}
	if err := s.validateTaskRefsAgainstDurable(ctx, handoff.ProjectID, handoff.TaskID, handoff.TaskSHA256, handoff.TaskRefs); err != nil {
		return model.PlannerReport{}, OperationResult{}, err
	}
	if handoff.Status != model.DeliveryHandoffInProgress && handoff.Status != model.DeliveryHandoffBlocked && handoff.Status != model.DeliveryHandoffAwaitingDecision {
		return model.PlannerReport{}, OperationResult{}, fmt.Errorf("handoff cannot receive a report from %q", handoff.Status)
	}
	report := in.Report
	if report.ID == "" {
		report.ID, err = newDurableRecordID()
		if err != nil {
			return model.PlannerReport{}, OperationResult{}, err
		}
	}
	report.SchemaVersion = model.DurableHandoffSchemaVersion
	report.ProjectID = handoff.ProjectID
	report.HandoffID = handoff.ID
	report.TaskID = handoff.TaskID
	report.RunID = handoff.RunID
	report.TaskSHA256 = handoff.TaskSHA256
	if strings.TrimSpace(report.PublishedBy) == "" {
		return model.PlannerReport{}, OperationResult{}, fmt.Errorf("published_by is required")
	}
	if report.PublishedAt.IsZero() {
		report.PublishedAt = time.Now().UTC()
	}
	if err := model.ValidatePlannerReport(report); err != nil {
		return model.PlannerReport{}, OperationResult{}, err
	}
	if err := model.PlannerReportRequiresTerminalEvidence(report); err != nil {
		return model.PlannerReport{}, OperationResult{}, err
	}
	var expectedTask model.Task
	var expectedRun model.Run
	var expectedAgent model.Report
	var expectedDelivery model.RunReviewReport
	if report.ReportType == model.PlannerReportCompleted {
		expectedTask, expectedRun, expectedAgent, expectedDelivery, err = s.validateCompletedDeliveryProof(ctx, handoff, report.TechnicalEvidence)
		if err != nil {
			return model.PlannerReport{}, OperationResult{}, err
		}
	}
	if handoff.CurrentReportID == "" && report.SupersedesReportID != "" {
		return model.PlannerReport{}, OperationResult{}, fmt.Errorf("report supersedes no current report")
	}
	if handoff.CurrentReportID != "" && report.SupersedesReportID != handoff.CurrentReportID {
		return model.PlannerReport{}, OperationResult{}, fmt.Errorf("report supersession does not match current report")
	}
	if report.ReportType == model.PlannerReportCompleted && handoff.Status != model.DeliveryHandoffInProgress {
		return model.PlannerReport{}, OperationResult{}, fmt.Errorf("completed report requires an in-progress handoff")
	}
	reportDigest, err := model.CanonicalPlannerReportDigest(report)
	if err != nil {
		return model.PlannerReport{}, OperationResult{}, err
	}
	reportState := model.PlannerReportState{SchemaVersion: model.DurableHandoffSchemaVersion, ReportID: report.ID, ReportSHA256: reportDigest, Status: model.PlannerReportPublished, UpdatedAt: report.PublishedAt}
	reportPath := s.plannerReportPath(handoff.ProjectID, report.ID)
	reportStatePath := s.plannerReportStatePath(handoff.ProjectID, report.ID)
	handoffPath := s.deliveryHandoffPath(handoff.ProjectID, handoff.ID)
	nextHandoff := handoff
	nextHandoff.CurrentReportID = report.ID
	nextHandoff.UpdatedAt = report.PublishedAt
	switch report.ReportType {
	case model.PlannerReportCompleted:
		nextHandoff.Status = model.DeliveryHandoffCompleted
	case model.PlannerReportBlocked:
		nextHandoff.Status = model.DeliveryHandoffBlocked
	case model.PlannerReportDecisionRequired:
		nextHandoff.Status = model.DeliveryHandoffAwaitingDecision
	}
	if err := model.ValidateDeliveryHandoff(nextHandoff); err != nil {
		return model.PlannerReport{}, OperationResult{}, err
	}
	tx, err := s.Hub.Transact(ctx, in.ExpectedHubRevision, "delivery: publish planner report "+report.ID, func(worktree string) ([]string, error) {
		changed := make([]string, 0, 4)
		var stored model.DeliveryHandoff
		if err := readWorktreeJSON(worktree, handoffPath, &stored); err != nil {
			return nil, err
		}
		if err := model.ValidateDeliveryHandoff(stored); err != nil {
			return nil, err
		}
		if stored.ID != handoff.ID || stored.Status != handoff.Status || stored.CurrentReportID != handoff.CurrentReportID || stored.UpdatedAt != handoff.UpdatedAt {
			return nil, fmt.Errorf("handoff changed before report publication")
		}
		if report.ReportType == model.PlannerReportCompleted {
			if err := validateCompletedDeliveryProofInWorktree(worktree, s, stored, expectedTask, expectedRun, expectedAgent, expectedDelivery); err != nil {
				return nil, err
			}
		}
		if stored.CurrentReportID != "" {
			oldReport, oldState, err := validatePlannerReportStateInWorktree(worktree, s, stored.ProjectID, stored.CurrentReportID)
			if err != nil {
				return nil, err
			}
			oldStatePath := s.plannerReportStatePath(stored.ProjectID, stored.CurrentReportID)
			if oldReport.ID != stored.CurrentReportID {
				return nil, fmt.Errorf("current planner report identity mismatch")
			}
			if oldState.Status == model.PlannerReportResolved || oldState.Status == model.PlannerReportSuperseded {
				return nil, fmt.Errorf("planner report cannot supersede state %q", oldState.Status)
			}
			oldState.Status = model.PlannerReportSuperseded
			oldState.UpdatedAt = report.PublishedAt
			if err := hub.WriteJSON(worktree, oldStatePath, oldState); err != nil {
				return nil, err
			}
			changed = append(changed, oldStatePath)
		}
		if _, statErr := os.Lstat(filepath.Join(worktree, filepath.FromSlash(reportPath))); statErr == nil {
			return nil, fmt.Errorf("planner report already exists")
		} else if !os.IsNotExist(statErr) {
			return nil, statErr
		}
		if err := hub.WriteJSON(worktree, reportPath, report); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, reportStatePath, reportState); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, handoffPath, nextHandoff); err != nil {
			return nil, err
		}
		extraReportIDs := []string{}
		if handoff.CurrentReportID != "" {
			extraReportIDs = append(extraReportIDs, handoff.CurrentReportID)
		}
		_, journalPaths, err := s.appendOperatorEventInWorktree(worktree, handoffJournalInput(nextHandoff, report.ID, extraReportIDs, "planner report published and handoff advanced", "delivery"))
		if err != nil {
			return nil, err
		}
		changed = append(changed, reportPath, reportStatePath, handoffPath)
		changed = append(changed, journalPaths...)
		return changed, nil
	})
	if err != nil {
		return model.PlannerReport{}, OperationResult{}, err
	}
	return report, OperationResult{Hub: tx, ProjectID: report.ProjectID, Status: nextHandoff.Status}, nil
}

func (s *Service) PlannerReportRead(ctx context.Context, id string) (model.PlannerReport, error) {
	projects, err := s.ProjectList(ctx)
	if err != nil {
		return model.PlannerReport{}, err
	}
	for _, project := range projects {
		var report model.PlannerReport
		if readErr := s.Hub.ReadJSON(ctx, s.plannerReportPath(project.ID, id), &report); readErr == nil {
			if err := model.ValidatePlannerReport(report); err != nil {
				return model.PlannerReport{}, err
			}
			return report, nil
		} else if !IsNotFound(readErr) {
			return model.PlannerReport{}, readErr
		}
	}
	return model.PlannerReport{}, fmt.Errorf("planner report not found: %s", id)
}

func (s *Service) plannerReportStateReadInProject(ctx context.Context, projectID, id string) (model.PlannerReportState, error) {
	var report model.PlannerReport
	if err := s.Hub.ReadJSON(ctx, s.plannerReportPath(projectID, id), &report); err != nil {
		return model.PlannerReportState{}, err
	}
	if err := model.ValidatePlannerReport(report); err != nil {
		return model.PlannerReportState{}, err
	}
	var state model.PlannerReportState
	if err := s.Hub.ReadJSON(ctx, s.plannerReportStatePath(projectID, id), &state); err != nil {
		return model.PlannerReportState{}, err
	}
	if err := model.ValidatePlannerReportState(state); err != nil {
		return model.PlannerReportState{}, err
	}
	digest, err := model.CanonicalPlannerReportDigest(report)
	if err != nil || state.ReportSHA256 != digest || state.ReportID != report.ID {
		return model.PlannerReportState{}, fmt.Errorf("planner report state does not match immutable report")
	}
	return state, nil
}

func validatePlannerReportStateInWorktree(worktree string, s *Service, projectID, id string) (model.PlannerReport, model.PlannerReportState, error) {
	var report model.PlannerReport
	if err := readWorktreeJSON(worktree, s.plannerReportPath(projectID, id), &report); err != nil {
		return model.PlannerReport{}, model.PlannerReportState{}, err
	}
	if err := model.ValidatePlannerReport(report); err != nil {
		return model.PlannerReport{}, model.PlannerReportState{}, err
	}
	var state model.PlannerReportState
	if err := readWorktreeJSON(worktree, s.plannerReportStatePath(projectID, id), &state); err != nil {
		return model.PlannerReport{}, model.PlannerReportState{}, err
	}
	if err := model.ValidatePlannerReportState(state); err != nil {
		return model.PlannerReport{}, model.PlannerReportState{}, err
	}
	digest, err := model.CanonicalPlannerReportDigest(report)
	if err != nil || state.ReportID != report.ID || state.ReportSHA256 != digest {
		return model.PlannerReport{}, model.PlannerReportState{}, fmt.Errorf("planner report state does not match immutable report")
	}
	return report, state, nil
}

func (s *Service) PlannerReportAcknowledge(ctx context.Context, in PlannerReportAcknowledgeInput) (model.PlannerReportState, OperationResult, error) {
	if err := authority.RequirePlanner(ctx); err != nil {
		return model.PlannerReportState{}, OperationResult{}, err
	}
	if strings.TrimSpace(in.AcknowledgedBy) == "" {
		return model.PlannerReportState{}, OperationResult{}, fmt.Errorf("acknowledged_by is required")
	}
	report, err := s.PlannerReportRead(ctx, in.ReportID)
	if err != nil {
		return model.PlannerReportState{}, OperationResult{}, err
	}
	state, err := s.plannerReportStateReadInProject(ctx, report.ProjectID, report.ID)
	if err != nil {
		return model.PlannerReportState{}, OperationResult{}, err
	}
	if state.Status != model.PlannerReportPublished {
		return model.PlannerReportState{}, OperationResult{}, fmt.Errorf("planner report cannot be acknowledged from %q", state.Status)
	}
	now := time.Now().UTC()
	next := state
	next.Status = model.PlannerReportAcknowledged
	next.AcknowledgedBy = in.AcknowledgedBy
	next.AcknowledgedAt = &now
	next.UpdatedAt = now
	path := s.plannerReportStatePath(report.ProjectID, report.ID)
	tx, err := s.Hub.Transact(ctx, in.ExpectedHubRevision, "planner: acknowledge report "+report.ID, func(worktree string) ([]string, error) {
		storedReport, stored, err := validatePlannerReportStateInWorktree(worktree, s, report.ProjectID, report.ID)
		if err != nil {
			return nil, err
		}
		if storedReport.ID != report.ID {
			return nil, fmt.Errorf("planner report identity mismatch")
		}
		if stored.ReportID != state.ReportID || stored.ReportSHA256 != state.ReportSHA256 || stored.Status != model.PlannerReportPublished || !stored.UpdatedAt.Equal(state.UpdatedAt) {
			return nil, fmt.Errorf("planner report changed before acknowledgement")
		}
		if err := hub.WriteJSON(worktree, path, next); err != nil {
			return nil, err
		}
		journalHandoff := model.DeliveryHandoff{ID: report.HandoffID, ProjectID: report.ProjectID, TaskID: report.TaskID, RunID: report.RunID}
		_, journalPaths, err := s.appendOperatorEventInWorktree(worktree, handoffJournalInput(journalHandoff, report.ID, nil, "planner report acknowledged", "planner"))
		if err != nil {
			return nil, err
		}
		return append([]string{path}, journalPaths...), nil
	})
	if err != nil {
		return model.PlannerReportState{}, OperationResult{}, err
	}
	return next, OperationResult{Hub: tx, ProjectID: report.ProjectID, Status: next.Status}, nil
}

func (s *Service) PlannerReportNext(ctx context.Context, in PlannerReportNextInput) (model.PlannerReportState, OperationResult, error) {
	if err := authority.RequirePlanner(ctx); err != nil {
		return model.PlannerReportState{}, OperationResult{}, err
	}
	if strings.TrimSpace(in.ResolvedBy) == "" {
		return model.PlannerReportState{}, OperationResult{}, fmt.Errorf("resolved_by is required")
	}
	report, err := s.PlannerReportRead(ctx, in.ReportID)
	if err != nil {
		return model.PlannerReportState{}, OperationResult{}, err
	}
	state, err := s.plannerReportStateReadInProject(ctx, report.ProjectID, report.ID)
	if err != nil {
		return model.PlannerReportState{}, OperationResult{}, err
	}
	if state.Status != model.PlannerReportAcknowledged {
		return model.PlannerReportState{}, OperationResult{}, fmt.Errorf("planner report cannot be resolved from %q", state.Status)
	}
	now := time.Now().UTC()
	next := state
	next.Status = model.PlannerReportResolved
	next.ResolvedBy = in.ResolvedBy
	next.ResolvedAt = &now
	next.UpdatedAt = now
	path := s.plannerReportStatePath(report.ProjectID, report.ID)
	tx, err := s.Hub.Transact(ctx, in.ExpectedHubRevision, "planner: resolve report "+report.ID, func(worktree string) ([]string, error) {
		storedReport, stored, err := validatePlannerReportStateInWorktree(worktree, s, report.ProjectID, report.ID)
		if err != nil {
			return nil, err
		}
		if storedReport.ID != report.ID {
			return nil, fmt.Errorf("planner report identity mismatch")
		}
		if stored.ReportID != state.ReportID || stored.ReportSHA256 != state.ReportSHA256 || stored.Status != model.PlannerReportAcknowledged || !stored.UpdatedAt.Equal(state.UpdatedAt) {
			return nil, fmt.Errorf("planner report changed before resolution")
		}
		if err := hub.WriteJSON(worktree, path, next); err != nil {
			return nil, err
		}
		journalHandoff := model.DeliveryHandoff{ID: report.HandoffID, ProjectID: report.ProjectID, TaskID: report.TaskID, RunID: report.RunID}
		_, journalPaths, err := s.appendOperatorEventInWorktree(worktree, handoffJournalInput(journalHandoff, report.ID, nil, "planner report resolved", "planner"))
		if err != nil {
			return nil, err
		}
		return append([]string{path}, journalPaths...), nil
	})
	if err != nil {
		return model.PlannerReportState{}, OperationResult{}, err
	}
	return next, OperationResult{Hub: tx, ProjectID: report.ProjectID, Status: next.Status}, nil
}

func plannerReportStatusProjection(item model.PlannerReport, state model.PlannerReportState) model.PlannerReportStatus {
	return model.PlannerReportStatus{SchemaVersion: item.SchemaVersion, ID: item.ID, ProjectID: item.ProjectID, HandoffID: item.HandoffID, TaskID: item.TaskID, RunID: item.RunID, ReportType: item.ReportType, OwnerSummary: item.OwnerSummary, SupersedesReportID: item.SupersedesReportID, PublishedBy: item.PublishedBy, PublishedAt: item.PublishedAt, Status: state.Status}
}

func (s *Service) PlannerReportList(ctx context.Context, projectID string) ([]model.PlannerReportStatus, error) {
	paths, err := s.Hub.List(ctx, s.plannerReportPrefix(projectID), ".json")
	if err != nil {
		return nil, err
	}
	items := make([]model.PlannerReportStatus, 0, len(paths))
	for _, path := range paths {
		if strings.HasSuffix(path, ".state.json") {
			continue
		}
		var report model.PlannerReport
		if err := s.Hub.ReadJSON(ctx, path, &report); err != nil {
			return nil, err
		}
		if err := model.ValidatePlannerReport(report); err != nil {
			return nil, err
		}
		state, err := s.plannerReportStateReadInProject(ctx, projectID, report.ID)
		if err != nil {
			return nil, err
		}
		items = append(items, plannerReportStatusProjection(report, state))
	}
	sort.Slice(items, func(i, j int) bool { return items[i].PublishedAt.After(items[j].PublishedAt) })
	return items, nil
}
