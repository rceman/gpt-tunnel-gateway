package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestPlanCutoverPreservesLegacySemanticsAndIsOneTime(t *testing.T) {
	s, hubRevision, _ := testService(t)
	legacy := legacyPlanV1{SchemaVersion: model.SchemaVersion, ProjectID: "example", Revision: 3, Summary: "Legacy summary", Body: "# Legacy\n\n## Objective\n\nBuild the foundation.\n\n## Queue\n\n- first-task\n- second-task\n\n## Design\n\nKeep the contract exact.", UpdatedBy: "gpt", UpdatedAt: time.Now().UTC()}
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
	cutover, err := s.PlanCutover(context.Background(), PlanCutoverInput{ProjectID: "example", UpdatedBy: "owner"})
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
	if _, err := s.PlanCutover(context.Background(), PlanCutoverInput{ProjectID: "example", UpdatedBy: "owner"}); err == nil {
		t.Fatal("second cutover was accepted")
	}
}

func TestPlanSectionsSupportPartialUpdatesIndependentConflictsAndRender(t *testing.T) {
	s, hubRevision, _ := testService(t)
	title, summary, objective, queue, activeTask := "Plan", "Summary", "Objective", []string{"first", "second"}, "task"
	operation, err := s.PlanUpdate(context.Background(), PlanUpdateInput{ProjectID: "example", Title: &title, Summary: &summary, CurrentObjective: &objective, Queue: &queue, ActiveTaskID: &activeTask, UpdatedBy: "gpt", WriteOptions: WriteOptions{ExpectedHubRevision: hubRevision}})
	if err != nil {
		t.Fatal(err)
	}
	create := func(id, heading string) model.PlanSection {
		result, err := s.PlanSectionCreate(context.Background(), PlanSectionCreateInput{ProjectID: "example", SectionID: id, Title: heading, ShortDescription: "Short " + id, Description: "Description " + id, UpdatedBy: "gpt", WriteOptions: WriteOptions{ExpectedHubRevision: operation.Hub.After}})
		if err != nil {
			t.Fatal(err)
		}
		operation = result
		section, err := s.PlanSectionRead(context.Background(), "example", id)
		if err != nil {
			t.Fatal(err)
		}
		return section
	}
	first := create("first", "First")
	second := create("second", "Second")
	staleHubRevision := operation.Hub.After
	newDescription := "Updated first description"
	newShort := "Updated second short description"
	if _, err := s.PlanSectionUpdate(context.Background(), PlanSectionUpdateInput{ProjectID: "example", SectionID: second.ID, ShortDescription: &newShort, UpdatedBy: "gpt", ExpectedSectionRevision: second.Revision}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PlanSectionUpdate(context.Background(), PlanSectionUpdateInput{ProjectID: "example", SectionID: first.ID, Description: &newDescription, UpdatedBy: "gpt", ExpectedSectionRevision: first.Revision, WriteOptions: WriteOptions{ExpectedHubRevision: staleHubRevision}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PlanSectionUpdate(context.Background(), PlanSectionUpdateInput{ProjectID: "example", SectionID: first.ID, Description: &newDescription, UpdatedBy: "gpt", ExpectedSectionRevision: first.Revision}); err == nil || !strings.Contains(err.Error(), "SECTION_REVISION_CONFLICT") {
		t.Fatalf("stale section revision was not rejected: %v", err)
	}
	updatedPlan, err := s.PlanUpdate(context.Background(), PlanUpdateInput{ProjectID: "example", Summary: planString("Updated summary"), UpdatedBy: "gpt"})
	if err != nil {
		t.Fatal(err)
	}
	_ = updatedPlan
	plan, err := s.PlanRead(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Title != title || plan.CurrentObjective != objective || strings.Join(plan.Queue, ",") != "first,second" {
		t.Fatalf("partial manifest update did not preserve fields: %#v", plan)
	}
	rendered, err := s.PlanRender(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Index(rendered.Text, "## First") > strings.Index(rendered.Text, "## Second") || !strings.Contains(rendered.Text, "Updated first description") || !strings.Contains(rendered.Text, "Updated second short description") {
		t.Fatalf("render ordering/content incorrect: %q", rendered.Text)
	}
	if _, err := s.PlanSectionDelete(context.Background(), PlanSectionDeleteInput{ProjectID: "example", SectionID: first.ID, UpdatedBy: "delete-owner", ExpectedSectionRevision: first.Revision + 1}); err != nil {
		t.Fatal(err)
	}
	deletedPlan, err := s.PlanRead(context.Background(), "example")
	if err != nil || deletedPlan.UpdatedBy != "delete-owner" {
		t.Fatalf("delete actor was not preserved: %v %#v", err, deletedPlan)
	}
	if _, err := s.PlanSectionRead(context.Background(), "example", first.ID); err == nil {
		t.Fatal("deleted section remained readable")
	}
	history, err := s.Hub.History(context.Background(), s.planSectionPath("example", first.ID), 20)
	if err != nil || len(history) < 2 {
		t.Fatalf("section Git history not retained: %v %#v", err, history)
	}
}

func TestProjectStatusReturnsCompactPlanOnly(t *testing.T) {
	s, hubRevision, _ := testService(t)
	title, summary := "Plan", "Summary"
	operation, err := s.PlanUpdate(context.Background(), PlanUpdateInput{ProjectID: "example", Title: &title, Summary: &summary, UpdatedBy: "gpt", WriteOptions: WriteOptions{ExpectedHubRevision: hubRevision}})
	if err != nil {
		t.Fatal(err)
	}
	section, err := s.PlanSectionCreate(context.Background(), PlanSectionCreateInput{ProjectID: "example", SectionID: "compact", Title: "Compact", ShortDescription: "Status line", Description: strings.Repeat("full description ", 100), UpdatedBy: "gpt", WriteOptions: WriteOptions{ExpectedHubRevision: operation.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	_ = section
	status, err := s.ProjectStatus(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Plan.Sections) != 1 || status.Plan.Sections[0] != "* Compact - Status line" {
		t.Fatalf("compact plan missing section index: %#v", status.Plan)
	}
	encoded, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "full description") {
		t.Fatal("project status loaded full section description")
	}
}
