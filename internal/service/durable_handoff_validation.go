package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func handoffJournalInput(handoff model.DeliveryHandoff, reportID string, extraIdentities []string, summary, actor string) OperatorRecordInput {
	identities := []string{handoff.ID}
	identities = append(identities, extraIdentities...)
	if reportID != "" {
		identities = append(identities, reportID)
	}
	return OperatorRecordInput{
		ProjectID:  handoff.ProjectID,
		Kind:       model.OperatorOperation,
		Summary:    summary,
		Content:    model.OperatorJournalContent{Facts: []string{summary}},
		References: model.OperatorJournalReferences{PlanSections: append([]string(nil), handoff.PlanSectionRefs...), Tasks: []string{handoff.TaskID}, Runs: []string{handoff.RunID}, Identities: identities},
		Actor:      actor,
	}
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

func activeDeliveryHandoffStatus(status string) bool {
	switch status {
	case model.DeliveryHandoffPending, model.DeliveryHandoffAcknowledged, model.DeliveryHandoffInProgress, model.DeliveryHandoffBlocked, model.DeliveryHandoffAwaitingDecision:
		return true
	default:
		return false
	}
}

func (s *Service) rejectDuplicateActiveHandoffInWorktree(worktree, projectID, taskID, runID string) error {
	prefix := s.deliveryHandoffPrefix(projectID)
	root := filepath.Join(worktree, filepath.FromSlash(prefix))
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := prefix + "/" + entry.Name()
		var existing model.DeliveryHandoff
		if err := readWorktreeJSON(worktree, path, &existing); err != nil {
			return fmt.Errorf("validate existing delivery handoff %q: %w", entry.Name(), err)
		}
		if err := model.ValidateDeliveryHandoff(existing); err != nil {
			return fmt.Errorf("validate existing delivery handoff %q: %w", entry.Name(), err)
		}
		if existing.TaskID == taskID && existing.RunID == runID && activeDeliveryHandoffStatus(existing.Status) {
			return fmt.Errorf("active delivery handoff already exists for task %s and run %s", taskID, runID)
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
