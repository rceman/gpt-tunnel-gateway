package onboarding

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestPublicOnboardMaterializesReferencedPlanSectionsAtomically(t *testing.T) {
	fixture, orchestrator := newPublicTestFixture(t)
	fixture.request.InitialPlan.Sections = []InitialPlanSection{
		{ID: "intro", Title: "Introduction", ShortDescription: "Project introduction", Revision: 1},
		{ID: "operations", Title: "Operations", ShortDescription: "Operational procedures", Revision: 1},
	}
	input := PublicInput{
		OperationID: fixture.operation,
		Request:     fixture.request,
	}
	result, err := orchestrator.Onboard(authority.WithPlanner(context.Background()), input)
	if err != nil || result.State != StateActivated {
		t.Fatalf("onboard with sections = %#v, err=%v", result, err)
	}
	var plan model.Plan
	if err := fixture.coordinator.Hub.ReadJSON(context.Background(), canonicalOnboardingPaths(fixture.request.ProjectID)[1], &plan); err != nil {
		t.Fatal(err)
	}
	if len(plan.Sections) != 2 {
		t.Fatalf("plan sections = %#v", plan.Sections)
	}
	for _, index := range plan.Sections {
		var section model.PlanSection
		if err := fixture.coordinator.Hub.ReadJSON(context.Background(), onboardingSectionPath(fixture.request.ProjectID, index.ID), &section); err != nil {
			t.Fatalf("read materialized section %s: %v", index.ID, err)
		}
		if err := model.ValidatePlanSection(section); err != nil {
			t.Fatalf("validate materialized section %s: %v", index.ID, err)
		}
		if section.ProjectID != fixture.request.ProjectID || section.Revision != index.Revision || section.Description != "" {
			t.Fatalf("materialized section %s = %#v", index.ID, section)
		}
	}
}

func TestPublicRecoverRepairsActivatedDanglingPlanSectionFromRequestEvidence(t *testing.T) {
	fixture, orchestrator := newPublicTestFixture(t)
	fixture.request.InitialPlan.Sections = []InitialPlanSection{{ID: "history", Title: "History", ShortDescription: "Project history", Revision: 1}}
	input := PublicInput{
		OperationID: fixture.operation,
		Request:     fixture.request,
	}
	ctx := authority.WithPlanner(context.Background())
	if result, err := orchestrator.Onboard(ctx, input); err != nil || result.State != StateActivated {
		t.Fatalf("initial onboarding = %#v, err=%v", result, err)
	}
	sectionPath := onboardingSectionPath(fixture.request.ProjectID, "history")
	hubRevision, err := fixture.coordinator.Hub.RemoteRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.coordinator.Hub.Transact(ctx, hubRevision, "test: remove legacy onboarding section", func(worktree string) ([]string, error) {
		if err := os.Remove(filepath.Join(worktree, filepath.FromSlash(sectionPath))); err != nil {
			return nil, err
		}
		return []string{sectionPath}, nil
	}); err != nil {
		t.Fatal(err)
	}
	result, err := orchestrator.Recover(ctx, input)
	if err != nil || result.State != StateActivated {
		t.Fatalf("repair recovery = %#v, err=%v", result, err)
	}
	var section model.PlanSection
	if err := fixture.coordinator.Hub.ReadJSON(ctx, sectionPath, &section); err != nil {
		t.Fatalf("repaired section read: %v", err)
	}
	if section.ID != "history" || section.Title != "History" || section.ShortDescription != "Project history" {
		t.Fatalf("repaired section = %#v", section)
	}
}
