package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func loadCurrentPlanFixture(t *testing.T) legacyPlanV1 {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "plan_v1_current.json"))
	if err != nil {
		t.Fatal(err)
	}
	var legacy legacyPlanV1
	if err := decodeStrict(data, &legacy); err != nil {
		t.Fatal(err)
	}
	return legacy
}

func TestPlanCutoverPreservesLegacySemanticsAndIsOneTime(t *testing.T) {
	s, hubRevision, _ := testService(t)
	legacy := legacyPlanV1{
		SchemaVersion: model.SchemaVersion,
		ProjectID:     "example",
		Revision:      3,
		Summary:       "Legacy summary",
		Body:          "# Legacy\n\n## Objective\n\nBuild the foundation.\n\n## Queue\n\n- first-task\n- second-task\n\n## Design\n\nKeep the contract exact.",
		UpdatedBy:     "gpt",
		UpdatedAt:     time.Now().UTC(),
	}
	if _, err := s.Hub.Transact(context.Background(), hubRevision, "test: install legacy plan", func(w string) ([]string, error) {
		path := s.planPath("example")
		if err := hub.WriteJSON(w, path, legacy); err != nil {
			return nil, err
		}
		return []string{path}, nil
	}); err != nil {
		t.Fatal(err)
	}
	beforeRead, err := s.Hub.RemoteRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PlanRead(context.Background(), "example"); err == nil {
		t.Fatal("schema-v1 plan was accepted by a normal read")
	}
	afterRead, err := s.Hub.RemoteRevision(context.Background())
	if err != nil || beforeRead != afterRead {
		t.Fatalf("normal read mutated hub: before=%s after=%s err=%v", beforeRead, afterRead, err)
	}
	cutover, err := s.PlanCutover(context.Background(), PlanCutoverInput{
		ProjectID: "example",
		UpdatedBy: "owner",
	})
	if err != nil {
		t.Fatal(err)
	}
	if cutover.Status != "cut over" {
		t.Fatalf("unexpected cutover result: %#v", cutover)
	}
	plan, err := s.PlanRead(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	if plan.SchemaVersion != model.PlanSchemaVersion || len(plan.Sections) != 4 || plan.CurrentObjective != "Build the foundation." || strings.Join(plan.Queue, ",") != "first-task,second-task" {
		t.Fatalf("unexpected cutover manifest: %#v", plan)
	}
	for _, index := range plan.Sections {
		if _, err := s.PlanSectionRead(context.Background(), "example", index.ID); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := s.Hub.ReadFile(context.Background(), s.planPath("example"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if _, ok := manifest["body"]; ok {
		t.Fatalf("legacy body remains in manifest: %s", raw)
	}
	if _, err := s.PlanCutover(context.Background(), PlanCutoverInput{
		ProjectID: "example",
		UpdatedBy: "owner",
	}); err == nil {
		t.Fatal("second cutover was accepted")
	}
}

func TestPlanCutoverUsesCurrentDurableQueueShape(t *testing.T) {
	s, hubRevision, _ := testService(t)
	legacy := loadCurrentPlanFixture(t)
	if _, err := s.Hub.Transact(context.Background(), hubRevision, "test: install current plan fixture", func(w string) ([]string, error) {
		path := s.planPath("example")
		if err := hub.WriteJSON(w, path, legacy); err != nil {
			return nil, err
		}
		return []string{path}, nil
	}); err != nil {
		t.Fatal(err)
	}
	result, err := s.PlanCutover(context.Background(), PlanCutoverInput{
		ProjectID: "example",
		UpdatedBy: "owner",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "cut over" {
		t.Fatalf("unexpected cutover result: %#v", result)
	}
	plan, err := s.PlanRead(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(plan.Queue, ",") != "P0,P1,P2" {
		t.Fatalf("queue=%#v want exact current queue identities", plan.Queue)
	}
	if plan.ActiveTaskID != legacy.ActiveTaskID {
		t.Fatalf("active references were not preserved: %#v", plan)
	}
	if len(plan.Sections) != 4 || plan.Sections[1].Title != "Current objective" || plan.Sections[2].Title != "Queue — workflow and documentation before optional features" {
		t.Fatalf("named sections/order not preserved: %#v", plan.Sections)
	}
	sections := make([]model.PlanSection, 0, len(plan.Sections))
	for _, index := range plan.Sections {
		section, readErr := s.PlanSectionRead(context.Background(), "example", index.ID)
		if readErr != nil {
			t.Fatal(readErr)
		}
		sections = append(sections, section)
	}
	if err := proveLegacyBodyPreserved(legacy.Body, sections); err != nil {
		t.Fatal(err)
	}
}

func TestPlanMutationsAreRetired(t *testing.T) {
	s, hubRevision, _ := testService(t)
	title, summary := "Plan", "Summary"
	if _, err := s.PlanUpdate(context.Background(), PlanUpdateInput{
		ProjectID: "example",
		Title:     &title,
		Summary:   &summary,
		UpdatedBy: "gpt",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: hubRevision,
		},
	}); err == nil || !strings.Contains(err.Error(), "PLAN_AUTHORITY_RETIRED") {
		t.Fatalf("retired Plan update error=%v", err)
	}
	if _, err := s.PlanSectionCreate(context.Background(), PlanSectionCreateInput{
		ProjectID: "example",
		SectionID: "retired",
		Title:     "Retired",
		UpdatedBy: "gpt",
	}); err == nil || !strings.Contains(err.Error(), "PLAN_AUTHORITY_RETIRED") {
		t.Fatalf("retired Plan section create error=%v", err)
	}
	after, err := s.hubRevision(context.Background())
	if err != nil || after != hubRevision {
		t.Fatalf("rejected Plan mutations changed Hub revision: before=%s after=%s err=%v", hubRevision, after, err)
	}
}

func TestProjectStatusIgnoresRetiredPlanAuthority(t *testing.T) {
	s, hubRevision, _ := testService(t)
	if _, err := s.Hub.Transact(context.Background(), hubRevision, "test: install malformed retired plan", func(worktree string) ([]string, error) {
		path := s.planPath("example")
		if err := hub.WriteText(worktree, path, "{"); err != nil {
			return nil, err
		}
		return []string{path}, nil
	}); err != nil {
		t.Fatal(err)
	}
	status, err := s.ProjectStatus(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	if status.Plan.Revision != 0 || len(status.Plan.Queue) != 0 || len(status.Plan.Sections) != 0 {
		t.Fatalf("project status retained retired Plan projection: %#v", status.Plan)
	}
	if _, err := s.PlanRead(context.Background(), "example"); err == nil {
		t.Fatal("retired Plan authority was readable")
	}
}
