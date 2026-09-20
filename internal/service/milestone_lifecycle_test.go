package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func seedMilestoneTask(t *testing.T, s *Service, status string, priority string) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond)
	task := model.TaskAuthoring{SchemaVersion: model.TaskAuthoringSchemaVersion, ID: "EXM-TSK1", ProjectID: "example", Revision: 1, Title: "Ship release", Summary: "Ship release summary", Objective: "Ship the release", Type: model.TaskTypeTask, Execution: model.TaskExecutionCanonical, Status: status, Priority: priority, ADRRelation: model.TaskADRNoRequired, CreatedBy: "planner", CreatedAt: now, UpdatedAt: now}
	var err error
	task.RevisionSHA256, err = model.HashTaskAuthoring(task)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Durability.Shared.Exec(context.Background(), `INSERT INTO shared_tasks(id,revision,payload,updated_at) VALUES(?,?,?,?)`, task.ID, task.Revision, payload, task.UpdatedAt.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
}

func TestMilestoneLifecycleCASMembershipCompletionAndArchive(t *testing.T) {
	s, _, _ := testServiceSerial(t)
	db := testServiceWithDurability(t, s)
	seedMilestoneTask(t, s, model.TaskAuthoringDone, "P2")
	ctx := context.Background()
	created, _, err := s.MilestoneLifecycleCreate(ctx, MilestoneCreateInput{
		ProjectID: "example",
		Title:     "Release",
		Summary:   "First release",
		Tasks:     []string{"EXM-TSK1"},
		CreatedBy: "planner",
	}, "milestone-test-create")
	if err != nil {
		t.Fatal(err)
	}
	if created.ID != "EXM-MIL1" || created.Status != model.MilestonePlanned {
		t.Fatalf("created=%#v", created)
	}
	view, err := s.MilestoneLifecycleView(ctx, "example", created.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Tasks) != 1 || view.Tasks[0].Priority != "P2" || view.Tasks[0].Status != model.TaskAuthoringDone || view.Report == "" {
		t.Fatalf("view=%#v", view)
	}
	newTitle := "Release 1"
	updated, _, err := s.MilestoneLifecycleUpdate(ctx, MilestoneUpdateInput{
		ProjectID:        "example",
		Key:              created.ID,
		Title:            &newTitle,
		Tasks:            []string{"EXM-TSK1"},
		ExpectedRevision: 1,
		UpdatedBy:        "planner",
		Reason:           "refine",
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != 2 || updated.Title != newTitle {
		t.Fatalf("updated=%#v", updated)
	}
	if _, _, err := s.MilestoneLifecycleUpdate(ctx, MilestoneUpdateInput{
		ProjectID:        "example",
		Key:              created.ID,
		Tasks:            []string{"EXM-TSK1"},
		ExpectedRevision: 1,
		UpdatedBy:        "planner",
		Reason:           "stale",
	}); err == nil {
		t.Fatal("stale Milestone update succeeded")
	}
	active, err := s.MilestoneLifecycleActivate(ctx, "example", created.ID, "planner")
	if err != nil {
		t.Fatal(err)
	}
	if active.Status != model.MilestoneActive {
		t.Fatalf("active=%#v", active)
	}
	completed, err := s.MilestoneLifecycleComplete(ctx, MilestoneCompletionInput{
		ProjectID:    "example",
		Key:          created.ID,
		Evidence:     "Task verified",
		EvidenceRefs: []string{"EXM-TSK1"},
		Actor:        "planner",
	})
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != model.MilestoneCompleted || completed.CompletionEvidence != "Task verified" {
		t.Fatalf("completed=%#v", completed)
	}
	archived, err := s.MilestoneLifecycleArchive(ctx, "example", created.ID, "planner", "retained")
	if err != nil {
		t.Fatal(err)
	}
	if archived.Status != model.MilestoneArchived {
		t.Fatalf("archived=%#v", archived)
	}
	if _, err := s.MilestoneLifecycleActivate(ctx, "example", created.ID, "planner"); err == nil {
		t.Fatal("archived Milestone reactivated")
	}
	history, err := s.MilestoneLifecycleHistory(ctx, "example", created.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(history.Records) != 5 {
		t.Fatalf("history=%#v", history.Records)
	}
	if _, err := db.ReadSharedEntity(ctx, "milestone", created.ID); err != nil {
		t.Fatal(err)
	}
}

func TestMilestoneCompletionFailsClosedForNonDoneTask(t *testing.T) {
	s, _, _ := testServiceSerial(t)
	testServiceWithDurability(t, s)
	seedMilestoneTask(t, s, model.TaskAuthoringPlanned, "P2")
	created, _, err := s.MilestoneLifecycleCreate(context.Background(), MilestoneCreateInput{
		ProjectID: "example",
		Title:     "Blocked",
		Tasks:     []string{"EXM-TSK1"},
		CreatedBy: "planner",
	}, "milestone-test-blocked")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.MilestoneLifecycleActivate(context.Background(), "example", created.ID, "planner"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MilestoneLifecycleComplete(context.Background(), MilestoneCompletionInput{
		ProjectID: "example",
		Key:       created.ID,
		Evidence:  "attempt",
		Actor:     "planner",
	}); err == nil {
		t.Fatal("planned Task allowed completion")
	}
	current, err := s.MilestoneLifecycleRead(context.Background(), "example", created.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if current.Status != model.MilestoneActive {
		t.Fatalf("failed completion mutated status: %#v", current)
	}
}
