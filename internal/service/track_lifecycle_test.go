package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func seedTrackTask(t *testing.T, s *Service, key, title, priority string) model.TaskAuthoring {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond)
	task := model.TaskAuthoring{SchemaVersion: model.TaskAuthoringSchemaVersion, ID: key, ProjectID: "example", Revision: 1, Title: title, Summary: title + " summary", Objective: title + " objective", Type: model.TaskTypeTask, Execution: model.TaskExecutionCanonical, Status: model.TaskAuthoringDone, Priority: priority, ADRRelation: model.TaskADRNoRequired, CreatedBy: "planner", CreatedAt: now, UpdatedAt: now}
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
	return task
}

func seedTrackExecution(t *testing.T, s *Service, task model.TaskAuthoring, head string, status string) {
	t.Helper()
	state := model.TaskExecutionState{TaskID: task.ID, ProjectID: task.ProjectID, Status: status, Stage: "code", Worktree: "WT-TSK" + strings.TrimPrefix(task.ID, "EXM-TSK") + "-" + strings.ToLower(head[:8]), BaseHead: head, Head: head, Branch: "task/" + task.ID + "-lane", TaskRevision: task.Revision, TaskRevisionSHA256: task.RevisionSHA256, Agent: "agent", ExecutionRevision: 1, UpdatedAt: time.Now().UTC()}
	if err := s.Durability.CreateTaskExecutionState(context.Background(), state); err != nil {
		t.Fatal(err)
	}
}

func TestTSK660MilestoneUnorderedSetAndTrackLifecycle(t *testing.T) {
	s, _, projectHead := testServiceSerial(t)
	testServiceWithDurability(t, s)
	task1 := seedTrackTask(t, s, "EXM-TSK1", "First Task", model.TaskPriorityP2)
	task2 := seedTrackTask(t, s, "EXM-TSK2", "Second Task", model.TaskPriorityP0)
	ctx := context.Background()
	if _, _, err := s.MilestoneLifecycleCreate(ctx, MilestoneCreateInput{
		ProjectID: "example",
		Title:     "Duplicate Membership",
		Tasks:     []string{task1.ID, task1.ID},
		CreatedBy: "planner",
	}, "tsk660-milestone-duplicate"); err == nil {
		t.Fatal("duplicate Milestone membership was accepted")
	}
	milestone, _, err := s.MilestoneLifecycleCreate(ctx, MilestoneCreateInput{
		ProjectID: "example",
		Title:     "Roadmap",
		Tasks:     []string{task1.ID, task2.ID},
		CreatedBy: "planner",
	}, "tsk660-milestone")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(milestone.Tasks, ",") != "EXM-TSK1,EXM-TSK2" {
		t.Fatalf("Milestone persisted order=%v", milestone.Tasks)
	}
	view, err := s.MilestoneLifecycleView(ctx, "example", milestone.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Tasks) != 2 || view.Tasks[0].Key != task2.ID || view.Tasks[1].Key != task1.ID {
		t.Fatalf("priority projection=%#v", view.Tasks)
	}
	if _, _, err := s.TrackLifecycleCreate(ctx, TrackCreateInput{
		ProjectID: "example",
		Milestone: milestone.ID,
		Title:     "Duplicate Track",
		Tasks:     []string{task1.ID, task1.ID},
		CreatedBy: "planner",
	}, "tsk660-track-duplicate-membership"); err == nil {
		t.Fatal("duplicate Track membership was accepted")
	}
	track, _, err := s.TrackLifecycleCreate(ctx, TrackCreateInput{
		ProjectID: "example",
		Milestone: milestone.ID,
		Title:     "Delivery",
		Tasks:     []string{task2.ID, task1.ID},
		CreatedBy: "planner",
	}, "tsk660-track")
	if err != nil {
		t.Fatal(err)
	}
	if track.ID != "EXM-TRK1" || strings.Join(track.Tasks, ",") != "EXM-TSK2,EXM-TSK1" || track.Status != model.TrackPlanned {
		t.Fatalf("Track create=%#v", track)
	}
	if _, _, err := s.TrackLifecycleCreate(ctx, TrackCreateInput{
		ProjectID: "example",
		Milestone: milestone.ID,
		Title:     "Duplicate",
		Tasks:     []string{task1.ID},
		CreatedBy: "planner",
	}, "tsk660-track-duplicate"); err == nil {
		t.Fatal("nonterminal Track exclusivity was not enforced")
	}
	newTitle := "Delivery Revised"
	updated, _, err := s.TrackLifecycleUpdate(ctx, TrackUpdateInput{
		ProjectID: "example",
		Key:       track.ID,
		Title:     &newTitle,
		Tasks:     &[]string{task1.ID, task2.ID},
		Actor:     "planner",
		Reason:    "reorder",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(updated.Tasks, ",") != "EXM-TSK1,EXM-TSK2" || updated.Revision != 2 {
		t.Fatalf("Track update=%#v", updated)
	}
	if _, _, err := s.TrackLifecycleAppendTasks(ctx, TrackMembershipInput{
		ProjectID: "example",
		Key:       track.ID,
		Tasks:     []string{task1.ID},
		Actor:     "lead",
		Reason:    "too early",
	}); err == nil {
		t.Fatal("planned Track append was accepted")
	}
	cancelled, _, err := s.TrackLifecycleCancel(ctx, TrackCancelInput{
		ProjectID: "example",
		Key:       track.ID,
		Actor:     "planner",
		Reason:    "superseded",
	})
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.Status != model.TrackCancelled || cancelled.CancelledBy != "planner" || cancelled.CancelledAt == nil {
		t.Fatalf("cancelled Track=%#v", cancelled)
	}
	if _, _, err := s.MilestoneLifecycleRemoveTasks(ctx, MilestoneMembershipInput{
		ProjectID: "example",
		Key:       milestone.ID,
		Tasks:     []string{task1.ID},
		Actor:     "planner",
		Reason:    "remove",
	}); err != nil {
		t.Fatalf("cancelled Track should release Milestone removal: %v", err)
	}

	milestone2, _, err := s.MilestoneLifecycleCreate(ctx, MilestoneCreateInput{
		ProjectID: "example",
		Title:     "Roadmap Two",
		Tasks:     []string{task1.ID, task2.ID},
		CreatedBy: "planner",
	}, "tsk660-milestone-two")
	if err != nil {
		t.Fatal(err)
	}
	activeTrack, _, err := s.TrackLifecycleCreate(ctx, TrackCreateInput{
		ProjectID: "example",
		Milestone: milestone2.ID,
		Title:     "Active Delivery",
		Tasks:     []string{task1.ID, task2.ID},
		CreatedBy: "planner",
	}, "tsk660-track-two")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.MilestoneLifecycleRemoveTasks(ctx, MilestoneMembershipInput{
		ProjectID: "example",
		Key:       milestone2.ID,
		Tasks:     []string{task1.ID},
		Actor:     "planner",
		Reason:    "blocked",
	}); err == nil {
		t.Fatal("Milestone removal ignored nonterminal Track")
	}
	if _, err := s.MilestoneLifecycleActivate(ctx, "example", milestone2.ID, "planner"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MilestoneLifecycleComplete(ctx, MilestoneCompletionInput{
		ProjectID: "example",
		Key:       milestone2.ID,
		Evidence:  "Track boundary",
		Actor:     "planner",
	}); err == nil || !strings.Contains(err.Error(), "nonterminal Track") {
		t.Fatalf("Milestone completion was not blocked by Track: %v", err)
	}
	seedTrackExecution(t, s, task1, projectHead, model.TaskExecutionIntegrated)
	seedTrackExecution(t, s, task2, projectHead, model.TaskExecutionIntegrated)
	ready, err := s.TrackLifecycleRead(ctx, "example", activeTrack.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if ready.Status != model.TrackReady {
		t.Fatalf("derived readiness=%#v", ready)
	}
	submitted, err := s.TrackLifecycleSubmit(ctx, "example", activeTrack.ID, "lead")
	if err != nil {
		t.Fatal(err)
	}
	if submitted.Track.Status != model.TrackReviewPending || submitted.Track.Review == nil || submitted.Track.Review.TrackRevision != submitted.Track.Revision || submitted.Track.Review.Head == "" || submitted.Track.Review.Tree == "" || submitted.Track.Review.Digest == "" {
		t.Fatalf("review snapshot=%#v", submitted.Track)
	}
	accepted, err := s.TrackLifecycleAccept(ctx, "example", activeTrack.ID, "planner")
	if err != nil {
		t.Fatal(err)
	}
	if accepted.Track.Status != model.TrackAccepted || accepted.Track.Revision != submitted.Track.Revision {
		t.Fatalf("accepted Track=%#v", accepted.Track)
	}
	if _, _, err := s.TrackLifecycleAppendTasks(ctx, TrackMembershipInput{
		ProjectID: "example",
		Key:       activeTrack.ID,
		Tasks:     []string{task1.ID},
		Actor:     "lead",
		Reason:    "immutable",
	}); err == nil {
		t.Fatal("accepted Track mutation was accepted")
	}
}
