package onboarding

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestPublicRecoverRejectsAdvancedPlanSectionDescriptorMismatch(t *testing.T) {
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
	var advanced model.Plan
	if err := fixture.coordinator.Hub.ReadJSON(ctx, planPath, &advanced); err != nil {
		t.Fatal(err)
	}
	advanced.Revision = 2
	advanced.Sections[0].Title = "Conflicting Architecture Review"
	advancedBytes, err := json.MarshalIndent(advanced, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	advancedBytes = append(advancedBytes, '\n')
	hubRevision, err := fixture.coordinator.Hub.RemoteRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.coordinator.Hub.Transact(ctx, hubRevision, "test: create conflicting advanced plan", func(worktree string) ([]string, error) {
		if err := os.Remove(filepath.Join(worktree, filepath.FromSlash(sectionPath))); err != nil {
			return nil, err
		}
		if err := os.WriteFile(filepath.Join(worktree, filepath.FromSlash(planPath)), advancedBytes, 0o600); err != nil {
			return nil, err
		}
		return []string{sectionPath, planPath}, nil
	}); err != nil {
		t.Fatal(err)
	}
	beforeSection, err := fixture.coordinator.Hub.ReadFile(ctx, sectionPath)
	if err == nil || beforeSection != nil {
		t.Fatalf("section unexpectedly exists before rejected recovery: %q, %v", beforeSection, err)
	}
	if _, err := orchestrator.Recover(ctx, input); err == nil || !strings.Contains(err.Error(), ErrOnboardingRecoveryRequired.Error()) {
		t.Fatalf("descriptor mismatch recovery error = %v, want recovery required", err)
	}
	if _, err := fixture.coordinator.Hub.ReadFile(ctx, sectionPath); err == nil {
		t.Fatal("descriptor mismatch recovery created a section")
	}
	afterPlan, err := fixture.coordinator.Hub.ReadFile(ctx, planPath)
	if err != nil || !bytes.Equal(afterPlan, advancedBytes) {
		t.Fatalf("descriptor mismatch recovery changed plan: err=%v", err)
	}
}
