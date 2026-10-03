package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

// tsk696BumpTaskRevision rewrites the stored Task revision so a pinned Track
// review snapshot no longer matches the current member snapshot — the exact
// mechanism that makes an accepted Track project stale.
func tsk696BumpTaskRevision(t *testing.T, s *Service, taskID string) {
	t.Helper()
	entity, err := s.Durability.ReadSharedEntity(context.Background(), "task", taskID)
	if err != nil {
		t.Fatal(err)
	}
	var task model.TaskAuthoring
	if err := json.Unmarshal(entity.Payload, &task); err != nil {
		t.Fatal(err)
	}
	task.Revision++
	task.UpdatedAt = time.Now().UTC().Truncate(time.Microsecond)
	if task.RevisionSHA256, err = model.HashTaskAuthoring(task); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Durability.Shared.Exec(context.Background(), `UPDATE shared_tasks SET revision=?,payload=?,updated_at=? WHERE id=? AND revision=?`, task.Revision, payload, task.UpdatedAt.Format(time.RFC3339Nano), taskID, entity.Revision); err != nil {
		t.Fatal(err)
	}
}

// tsk696TaskDone moves an integrated execution state to done, matching the
// post-integration lifecycle milestone/complete requires.
func tsk696TaskDone(t *testing.T, s *Service, taskID string) {
	t.Helper()
	ctx := context.Background()
	state, found, err := s.Durability.ReadTaskExecutionState(ctx, "example", taskID)
	if err != nil || !found {
		t.Fatalf("execution state found=%v err=%v", found, err)
	}
	previous := state.ExecutionRevision
	state.Status = model.TaskExecutionDone
	state.ExecutionRevision++
	state.UpdatedAt = time.Now().UTC()
	if err := s.Durability.UpdateTaskExecutionState(ctx, state, previous); err != nil {
		t.Fatal(err)
	}
}

// tsk696AcceptedStaleTrack builds a submitted+accepted Track whose review
// snapshot is then drifted so the derived projection is stale while the
// stored status stays accepted.
func tsk696AcceptedStaleTrack(t *testing.T, s *Service, head, milestoneID string, task model.TaskAuthoring, key string) model.Track {
	t.Helper()
	ctx := context.Background()
	track, _, err := s.TrackLifecycleCreate(ctx, TrackCreateInput{
		ProjectID: "example",
		Milestone: milestoneID,
		Title:     "Stale Accepted " + key,
		Tasks:     []string{task.ID},
		CreatedBy: "planner",
	}, key)
	if err != nil {
		t.Fatal(err)
	}
	seedTrackExecution(t, s, task, head, model.TaskExecutionIntegrated)
	submitted, err := s.TrackLifecycleSubmit(ctx, "example", track.ID, "lead")
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := s.TrackLifecycleAccept(ctx, "example", submitted.Track.ID, "planner")
	if err != nil {
		t.Fatal(err)
	}
	// Drift the member snapshot after acceptance: the stored status remains
	// accepted but the derived projection must now be stale.
	tsk696BumpTaskRevision(t, s, task.ID)
	stored, err := s.Durability.ReadSharedEntity(ctx, "track", track.ID)
	if err != nil {
		t.Fatal(err)
	}
	var storedTrack model.Track
	if err := json.Unmarshal(stored.Payload, &storedTrack); err != nil {
		t.Fatal(err)
	}
	if storedTrack.Status != model.TrackAccepted {
		t.Fatalf("stored Track status drifted: %#v", storedTrack)
	}
	derived, err := s.deriveTrackStatus(ctx, storedTrack)
	if err != nil {
		t.Fatal(err)
	}
	if derived != model.TrackStale {
		t.Fatalf("accepted Track did not project stale after member drift: derived=%q", derived)
	}
	return accepted.Track
}

// TestTSK696MilestoneCompleteTreatsStoredAcceptedAsTerminal proves the TSK696
// fix: milestone/complete decides terminality on the durable stored status,
// so an accepted Track blocks completion never — even when the freshness
// projection is stale — while never-accepted stale Tracks still block.
func TestTSK696MilestoneCompleteTreatsStoredAcceptedAsTerminal(t *testing.T) {
	s, _, projectHead := testServiceSerial(t)
	testServiceWithDurability(t, s)
	ctx := context.Background()
	task := seedTrackTask(t, s, "EXM-TSK1", "Accepted Stale Task", model.TaskPriorityP1)

	milestone, _, err := s.MilestoneLifecycleCreate(ctx, MilestoneCreateInput{
		ProjectID: "example",
		Title:     "Stale accepted milestone",
		Tasks:     []string{task.ID},
		CreatedBy: "planner",
	}, "tsk696-stale-accepted")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.MilestoneLifecycleActivate(ctx, "example", milestone.ID, "planner"); err != nil {
		t.Fatal(err)
	}
	tsk696AcceptedStaleTrack(t, s, projectHead, milestone.ID, task, "tsk696-stale-track")
	tsk696TaskDone(t, s, task.ID)
	completed, err := s.MilestoneLifecycleComplete(ctx, MilestoneCompletionInput{
		ProjectID: "example",
		Key:       milestone.ID,
		Evidence:  "accepted terminal even when stale",
		Actor:     "planner",
	})
	if err != nil {
		t.Fatalf("accepted-then-stale Track must not block milestone completion: %v", err)
	}
	if completed.Status != model.MilestoneCompleted {
		t.Fatalf("completed=%#v", completed)
	}
}

func TestTSK696MilestoneCompleteBlockedByNeverAcceptedStale(t *testing.T) {
	s, _, projectHead := testServiceSerial(t)
	testServiceWithDurability(t, s)
	ctx := context.Background()
	task := seedTrackTask(t, s, "EXM-TSK1", "Pending Stale Task", model.TaskPriorityP1)

	milestone, _, err := s.MilestoneLifecycleCreate(ctx, MilestoneCreateInput{
		ProjectID: "example",
		Title:     "Pending stale milestone",
		Tasks:     []string{task.ID},
		CreatedBy: "planner",
	}, "tsk696-pending-stale")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.MilestoneLifecycleActivate(ctx, "example", milestone.ID, "planner"); err != nil {
		t.Fatal(err)
	}
	track, _, err := s.TrackLifecycleCreate(ctx, TrackCreateInput{
		ProjectID: "example",
		Milestone: milestone.ID,
		Title:     "Never accepted",
		Tasks:     []string{task.ID},
		CreatedBy: "planner",
	}, "tsk696-pending-track")
	if err != nil {
		t.Fatal(err)
	}
	seedTrackExecution(t, s, task, projectHead, model.TaskExecutionIntegrated)
	if _, err := s.TrackLifecycleSubmit(ctx, "example", track.ID, "lead"); err != nil {
		t.Fatal(err)
	}
	tsk696BumpTaskRevision(t, s, task.ID)
	current, err := s.TrackLifecycleRead(ctx, "example", track.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if current.Status != model.TrackStale {
		t.Fatalf("pending Track should project stale after member drift: %#v", current)
	}
	if _, err := s.MilestoneLifecycleComplete(ctx, MilestoneCompletionInput{
		ProjectID: "example",
		Key:       milestone.ID,
		Evidence:  "must not complete",
		Actor:     "planner",
	}); err == nil || !strings.Contains(err.Error(), "nonterminal Track") {
		t.Fatalf("never-accepted stale Track must block milestone completion: %v", err)
	}
}

func TestTSK696MilestoneCompleteCancelledAndArchivedTracks(t *testing.T) {
	s, _, projectHead := testServiceSerial(t)
	testServiceWithDurability(t, s)
	ctx := context.Background()
	task1 := seedTrackTask(t, s, "EXM-TSK1", "Cancelled Task", model.TaskPriorityP1)
	task2 := seedTrackTask(t, s, "EXM-TSK2", "Archived Task", model.TaskPriorityP1)
	task3 := seedTrackTask(t, s, "EXM-TSK3", "Accepted Task", model.TaskPriorityP1)

	milestone, _, err := s.MilestoneLifecycleCreate(ctx, MilestoneCreateInput{
		ProjectID: "example",
		Title:     "Terminal tracks milestone",
		Tasks:     []string{task1.ID, task2.ID, task3.ID},
		CreatedBy: "planner",
	}, "tsk696-terminal")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.MilestoneLifecycleActivate(ctx, "example", milestone.ID, "planner"); err != nil {
		t.Fatal(err)
	}
	cancelledTrack, _, err := s.TrackLifecycleCreate(ctx, TrackCreateInput{
		ProjectID: "example",
		Milestone: milestone.ID,
		Title:     "Cancelled",
		Tasks:     []string{task1.ID},
		CreatedBy: "planner",
	}, "tsk696-cancelled-track")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.TrackLifecycleCancel(ctx, TrackCancelInput{
		ProjectID: "example",
		Key:       cancelledTrack.ID,
		Actor:     "planner",
		Reason:    "superseded",
	}); err != nil {
		t.Fatal(err)
	}
	// Seed the archived member Track directly: terminality is decided on the
	// stored record, and the fixture does not exercise the archive event path.
	now := time.Now().UTC().Truncate(time.Microsecond)
	archivedTrack := model.Track{
		SchemaVersion: model.TrackSchemaVersion, ID: "EXM-TRK91", ProjectID: "example", Revision: 1,
		Milestone: milestone.ID, Title: "Archived", Tasks: []string{task2.ID}, Status: model.TrackArchived,
		CreatedBy: "planner", CreatedAt: now, UpdatedBy: "planner", UpdatedAt: now,
	}
	if err := model.ValidateTrack(archivedTrack); err != nil {
		t.Fatal(err)
	}
	archivedPayload, err := json.Marshal(archivedTrack)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Durability.Shared.Exec(ctx, `INSERT INTO shared_tracks(id,revision,payload,updated_at) VALUES(?,?,?,?)`, archivedTrack.ID, archivedTrack.Revision, archivedPayload, archivedTrack.UpdatedAt.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	// Accepted-current Track: terminal without any drift.
	seedTrackExecution(t, s, task3, projectHead, model.TaskExecutionIntegrated)
	currentTrack, _, err := s.TrackLifecycleCreate(ctx, TrackCreateInput{
		ProjectID: "example",
		Milestone: milestone.ID,
		Title:     "Accepted current",
		Tasks:     []string{task3.ID},
		CreatedBy: "planner",
	}, "tsk696-current-track")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.TrackLifecycleSubmit(ctx, "example", currentTrack.ID, "lead"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TrackLifecycleAccept(ctx, "example", currentTrack.ID, "planner"); err != nil {
		t.Fatal(err)
	}
	tsk696TaskDone(t, s, task3.ID)
	completed, err := s.MilestoneLifecycleComplete(ctx, MilestoneCompletionInput{
		ProjectID: "example",
		Key:       milestone.ID,
		Evidence:  "cancelled+archived+accepted terminal",
		Actor:     "planner",
	})
	if err != nil {
		t.Fatalf("terminal Tracks must not block milestone completion: %v", err)
	}
	if completed.Status != model.MilestoneCompleted {
		t.Fatalf("completed=%#v", completed)
	}
}
