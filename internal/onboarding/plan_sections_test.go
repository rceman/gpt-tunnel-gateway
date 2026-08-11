package onboarding

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
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

func TestPublicRecoverRepairsMissingSectionAfterPlanAdvanced(t *testing.T) {
	fixture, orchestrator := newPublicTestFixture(t)
	fixture.request.InitialPlan.Sections = []InitialPlanSection{{ID: "architecture-review", Title: "Architecture Review", ShortDescription: "Architecture decisions", Revision: 1}}
	input := PublicInput{
		OperationID: fixture.operation,
		Request:     fixture.request,
	}
	ctx := authority.WithPlanner(context.Background())
	if result, err := orchestrator.Onboard(ctx, input); err != nil || result.State != StateActivated {
		t.Fatalf("initial onboarding = %#v, err=%v", result, err)
	}
	sectionPath := onboardingSectionPath(fixture.request.ProjectID, "architecture-review")
	planPath := canonicalOnboardingPaths(fixture.request.ProjectID)[1]
	identifiersPath := canonicalOnboardingPaths(fixture.request.ProjectID)[2]
	originalPlanBytes, err := fixture.coordinator.Hub.ReadFile(ctx, planPath)
	if err != nil {
		t.Fatal(err)
	}
	originalIdentifiersBytes, err := fixture.coordinator.Hub.ReadFile(ctx, identifiersPath)
	if err != nil {
		t.Fatal(err)
	}
	var advanced model.Plan
	if err := fixture.coordinator.Hub.ReadJSON(ctx, planPath, &advanced); err != nil {
		t.Fatal(err)
	}
	advanced.Revision = 2
	advanced.Summary = "Advanced after onboarding"
	advanced.UpdatedBy = "planner"
	advanced.UpdatedAt = time.Now().UTC()
	advancedBytes, err := json.MarshalIndent(advanced, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	advancedBytes = append(advancedBytes, '\n')
	var advancedIdentifiers model.ProjectIdentifiers
	if err := fixture.coordinator.Hub.ReadJSON(ctx, identifiersPath, &advancedIdentifiers); err != nil {
		t.Fatal(err)
	}
	advancedIdentifiers.NextTaskNumber = 6
	advancedIdentifiers.NextADRNumber = 5
	advancedIdentifiersBytes, err := json.MarshalIndent(advancedIdentifiers, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	advancedIdentifiersBytes = append(advancedIdentifiersBytes, '\n')
	hubRevision, err := fixture.coordinator.Hub.RemoteRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.coordinator.Hub.Transact(ctx, hubRevision, "test: advance plan and remove onboarding section", func(worktree string) ([]string, error) {
		if err := os.Remove(filepath.Join(worktree, filepath.FromSlash(sectionPath))); err != nil {
			return nil, err
		}
		if err := os.WriteFile(filepath.Join(worktree, filepath.FromSlash(planPath)), advancedBytes, 0o600); err != nil {
			return nil, err
		}
		if err := os.WriteFile(filepath.Join(worktree, filepath.FromSlash(identifiersPath)), advancedIdentifiersBytes, 0o600); err != nil {
			return nil, err
		}
		return []string{sectionPath, planPath, identifiersPath}, nil
	}); err != nil {
		t.Fatal(err)
	}
	beforePlan, err := fixture.coordinator.Hub.ReadFile(ctx, planPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(beforePlan, advancedBytes) {
		t.Fatal("advanced plan fixture was not written byte-for-byte")
	}
	if bytes.Equal(beforePlan, originalPlanBytes) {
		t.Fatal("advanced plan fixture did not advance")
	}

	result, err := orchestrator.Recover(ctx, input)
	if err != nil || result.State != StateActivated {
		t.Fatalf("advanced-plan recovery = %#v, err=%v", result, err)
	}
	afterPlan, err := fixture.coordinator.Hub.ReadFile(ctx, planPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(afterPlan, beforePlan) {
		t.Fatal("recovery rewrote advanced plan/current")
	}
	afterIdentifiers, err := fixture.coordinator.Hub.ReadFile(ctx, identifiersPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(afterIdentifiers, advancedIdentifiersBytes) || bytes.Equal(afterIdentifiers, originalIdentifiersBytes) {
		t.Fatal("recovery rewrote or regressed advanced identifiers")
	}
	var section model.PlanSection
	if err := fixture.coordinator.Hub.ReadJSON(ctx, sectionPath, &section); err != nil {
		t.Fatalf("repaired section read: %v", err)
	}
	if section.ID != "architecture-review" || section.Title != "Architecture Review" || section.ShortDescription != "Architecture decisions" || section.Revision != 1 {
		t.Fatalf("repaired section = %#v", section)
	}
}

func TestPublicRecoverRejectsPrimaryHubCommitDriftBeforeSectionRepair(t *testing.T) {
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
	project, _, _, _, err := buildDurableObjects(fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	sectionPath := onboardingSectionPath(fixture.request.ProjectID, "history")
	hubRevision, err := fixture.coordinator.Hub.RemoteRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	drifted := project
	drifted.RepositoryURL = "git@example.invalid:owner/temporary.git"
	if first, err := fixture.coordinator.Hub.Transact(ctx, hubRevision, "test: drift primary onboarding commit", func(worktree string) ([]string, error) {
		if err := os.Remove(filepath.Join(worktree, filepath.FromSlash(sectionPath))); err != nil {
			return nil, err
		}
		primaryPath := canonicalOnboardingPaths(fixture.request.ProjectID)[0]
		if err := hub.WriteJSON(worktree, primaryPath, drifted); err != nil {
			return nil, err
		}
		return []string{sectionPath, primaryPath}, nil
	}); err != nil {
		t.Fatal(err)
	} else {
		primaryPath := canonicalOnboardingPaths(fixture.request.ProjectID)[0]
		if _, err := fixture.coordinator.Hub.Transact(ctx, first.After, "test: restore primary content at a later commit", func(worktree string) ([]string, error) {
			if err := hub.WriteJSON(worktree, primaryPath, project); err != nil {
				return nil, err
			}
			return []string{primaryPath}, nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := orchestrator.Recover(ctx, input); err == nil || !strings.Contains(err.Error(), ErrOnboardingRecoveryRequired.Error()) {
		t.Fatalf("primary drift recovery error = %v, want recovery required", err)
	}
	if _, err := fixture.coordinator.Hub.ReadFile(ctx, sectionPath); err == nil {
		t.Fatal("primary drift recovery unexpectedly repaired section")
	}
}
