package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func seedMilestonePlanTask(t *testing.T, s *Service, key, title, priority string) model.TaskAuthoring {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond)
	task := model.TaskAuthoring{
		SchemaVersion: model.TaskAuthoringSchemaVersion,
		ID:            key,
		ProjectID:     "example",
		Revision:      1,
		Title:         title,
		Summary:       title + " summary",
		Objective:     title + " objective",
		Type:          model.TaskTypeTask,
		Execution:     model.TaskExecutionCanonical,
		Priority:      priority,
		ADRRelation:   model.TaskADRNoRequired,
		Status:        model.TaskAuthoringPlanned,
		CreatedBy:     "planner",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	var err error
	task.RevisionSHA256, err = model.HashTaskAuthoring(task)
	if err != nil {
		t.Fatal(err)
	}
	if err := model.ValidateTaskAuthoring(task); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Durability.Shared.Exec(context.Background(), `INSERT INTO shared_tasks(id,revision,payload,updated_at) VALUES(?,?,?,?)`, task.ID, task.Revision, payload, task.UpdatedAt.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	return task
}

func TestTSK663MilestonePlanRendersCurrentTracksAndUngroupedTasks(t *testing.T) {
	s, _, projectHead := testServiceSerial(t)
	testServiceWithDurability(t, s)
	tasks := []model.TaskAuthoring{
		seedMilestonePlanTask(t, s, "EXM-TSK1", "First planned task", model.TaskPriorityP2),
		seedMilestonePlanTask(t, s, "EXM-TSK2", "Released task", model.TaskPriorityP1),
		seedMilestonePlanTask(t, s, "EXM-TSK3", "Ordered first task", model.TaskPriorityP0),
		seedMilestonePlanTask(t, s, "EXM-TSK4", "Ungrouped P1 task", model.TaskPriorityP1),
		seedMilestonePlanTask(t, s, "EXM-TSK5", "Archived task", model.TaskPriorityP4),
		seedMilestonePlanTask(t, s, "EXM-TSK6", "Ungrouped P0 task", model.TaskPriorityP0),
		seedMilestonePlanTask(t, s, "EXM-TSK7", "Ungrouped tie task", model.TaskPriorityP1),
	}
	ctx := context.Background()
	milestone, _, err := s.MilestoneLifecycleCreate(ctx, MilestoneCreateInput{
		ProjectID: "example",
		Title:     "Current roadmap",
		Summary:   "A live plan summary.",
		Tasks:     []string{tasks[0].ID, tasks[1].ID, tasks[2].ID, tasks[3].ID, tasks[4].ID, tasks[5].ID, tasks[6].ID},
		CreatedBy: "planner",
	}, "tsk663-milestone")
	if err != nil {
		t.Fatal(err)
	}
	cancelled, _, err := s.TrackLifecycleCreate(ctx, TrackCreateInput{
		ProjectID: "example",
		Milestone: milestone.ID,
		Title:     "Cancelled delivery",
		Tasks:     []string{tasks[1].ID, tasks[0].ID},
		CreatedBy: "planner",
	}, "tsk663-cancelled-track")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.TrackLifecycleCancel(ctx, TrackCancelInput{
		ProjectID: "example",
		Key:       cancelled.ID,
		Actor:     "planner",
		Reason:    "release members",
	}); err != nil {
		t.Fatal(err)
	}
	current, _, err := s.TrackLifecycleCreate(ctx, TrackCreateInput{
		ProjectID: "example",
		Milestone: milestone.ID,
		Title:     "Current delivery",
		Summary:   "Ordered execution intent.",
		Tasks:     []string{tasks[2].ID, tasks[0].ID},
		CreatedBy: "planner",
	}, "tsk663-current-track")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.TaskLifecycleArchive(ctx, "example", tasks[4].ID, "planner", "historical member"); err != nil {
		t.Fatal(err)
	}

	before, err := s.MilestoneLifecyclePlan(ctx, "example", milestone.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(before, "# EXM-MIL1 — Current roadmap\n\n**Status:** planned\n**Summary:** A live plan summary.") {
		t.Fatalf("Milestone header missing: %s", before)
	}
	if !strings.Contains(before, "## EXM-TRK1 — Cancelled delivery\n\n**Status:** cancelled\n") || !strings.Contains(before, "Cancelled Track membership is historical") {
		t.Fatalf("cancelled Track section missing: %s", before)
	}
	if !strings.Contains(before, "## EXM-TRK2 — Current delivery\n\n**Status:** planned\n**Summary:** Ordered execution intent.") {
		t.Fatalf("current Track section missing: %s", before)
	}
	if !strings.Contains(before, "(1 archived members omitted)") || strings.Contains(before, tasks[4].ID) {
		t.Fatalf("archived Task handling is not truthful: %s", before)
	}
	trackTwo := strings.Index(before, "## EXM-TRK2")
	orderedFirst := strings.Index(before, "○ [P0] EXM-TSK3 — Ordered first task")
	orderedSecond := strings.Index(before, "○ [P2] EXM-TSK1 — First planned task")
	ungrouped := strings.Index(before, "## Ungrouped tasks")
	if trackTwo < 0 || orderedFirst < trackTwo || orderedSecond <= orderedFirst || ungrouped <= orderedSecond {
		t.Fatalf("Track Task order is not canonical: %s", before)
	}
	ungroupedP0 := strings.Index(before, "○ [P0] EXM-TSK6 — Ungrouped P0 task")
	ungroupedP1 := strings.Index(before, "○ [P1] EXM-TSK2 — Released task")
	ungroupedP1Tie := strings.Index(before, "○ [P1] EXM-TSK4 — Ungrouped P1 task")
	ungroupedP1Next := strings.Index(before, "○ [P1] EXM-TSK7 — Ungrouped tie task")
	if ungroupedP0 <= ungrouped || ungroupedP1 <= ungroupedP0 || ungroupedP1Tie <= ungroupedP1 || ungroupedP1Next <= ungroupedP1Tie {
		t.Fatalf("ungrouped Tasks are not priority/key sorted: %s", before)
	}
	for index, task := range tasks {
		want := 1
		if index == 4 {
			want = 0
		}
		if got := strings.Count(before, task.ID); got != want {
			t.Errorf("Task %s appears %d times, want %d: %s", task.ID, got, want, before)
		}
	}

	seedTrackExecution(t, s, tasks[2], projectHead, model.TaskExecutionBlocked)
	after, err := s.MilestoneLifecyclePlan(ctx, "example", milestone.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(after, "## EXM-TRK2 — Current delivery\n\n**Status:** active") || !strings.Contains(after, "⊘ [P0] EXM-TSK3 — Ordered first task") {
		t.Fatalf("plan did not reflect live Task/Track status: %s", after)
	}
	repeated, err := s.MilestoneLifecyclePlan(ctx, "example", milestone.ID)
	if err != nil || repeated != after {
		t.Fatalf("plan is not deterministic: err=%v\nfirst:\n%s\nsecond:\n%s", err, after, repeated)
	}
	if before == after {
		t.Fatal("live Task status change did not change the plan")
	}
	if current.ID != "EXM-TRK2" {
		t.Fatalf("unexpected canonical Track allocation: %q", current.ID)
	}
}

func TestTSK663MilestonePlanFailsClosedAtOutputBound(t *testing.T) {
	milestone := model.Milestone{ID: "GTW-MIL1", Title: "Roadmap", Status: model.MilestoneActive, Tasks: []string{"GTW-TSK1"}}
	view := MilestoneView{
		Milestone: milestone,
		Tasks:     []MilestoneTaskProjection{{Key: "GTW-TSK1", Title: "Task", Status: model.TaskAuthoringPlanned, Priority: model.TaskPriorityP1}},
	}
	tracks := make([]TrackView, 100)
	for i := range tracks {
		tracks[i].Track = model.Track{
			ID:        fmt.Sprintf("GTW-TRK%d", i+1),
			Milestone: milestone.ID,
			Title:     strings.Repeat("t", 128),
			Summary:   strings.Repeat("s", 256),
			Tasks:     []string{"GTW-TSK1"},
			Status:    model.TrackAccepted,
		}
	}
	if _, err := renderMilestonePlan(view, tracks); err == nil || !strings.Contains(err.Error(), "bounded Markdown limit") {
		t.Fatalf("oversized plan error=%v", err)
	}
}
