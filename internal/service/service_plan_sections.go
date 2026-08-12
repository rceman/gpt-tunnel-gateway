package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

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
	if err := rejectPlanMutationAfterTrainV2(ctx, s, in.ProjectID); err != nil {
		return OperationResult{}, err
	}
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
	return OperationResult{
		Hub:       tx,
		ProjectID: in.ProjectID,
		Status:    "created",
	}, nil
}

func (s *Service) PlanSectionUpdate(ctx context.Context, in PlanSectionUpdateInput) (OperationResult, error) {
	if err := rejectPlanMutationAfterTrainV2(ctx, s, in.ProjectID); err != nil {
		return OperationResult{}, err
	}
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
	return OperationResult{
		Hub:       tx,
		ProjectID: in.ProjectID,
		Status:    "updated",
	}, nil
}
